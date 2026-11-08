package appui

import (
	"bytes"
	"context"
	"errors"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/Team-Quaver/quaver-astra/internal/colorprobe"
	"github.com/Team-Quaver/quaver-astra/internal/player"

	"github.com/egoist/mygo/ui"
)

// CoverCache 按 URL 缓存封面 *ui.Bitmap；未命中时后台拉取，完成后触发重绘。
type CoverCache struct {
	mu       sync.Mutex
	m        map[string]*ui.Bitmap
	raw      map[string][]byte // 原始字节，供派生模糊底图
	blur     map[string]*ui.Bitmap
	inflight map[string]bool
	// epoch 让 Invalidate 后仍在路上的旧请求作废：旧图不能在失效之后
	// 又被写回缓存，否则刚导入的封面会被上一张盖住。
	epoch map[string]uint64
	// pick 记录候选链（首项为指纹）最终命中的档位；"" 表示全部不可用。
	// pickRetry 是整条链都失败后的重试时间，避免每帧都重打一遍 CDN。
	pick      map[string]string
	pickRetry map[string]time.Time
	pickBusy  map[string]bool
	// picked 按挑定顺序记录候选链命中的大图。1500 档解码后约 9MB，
	// 长时间连播不能无限攒着，只保留最近 coverKeep 张。
	picked []string
	hc     *http.Client
	ctx    context.Context
	cancel context.CancelFunc
	slots  chan struct{}
}

const (
	// maxCoverBytes 限制单张封面响应。异常大图在读取阶段直接拒绝，避免
	// io.ReadAll 把不可信响应一次性放大成超大临时切片。
	maxCoverBytes = 4 << 20
	// coverWorkers 限制同时下载/解码的封面数量，快速滚动列表时不会瞬间
	// 创建几十个大图解码缓冲区。
	coverWorkers = 3
	// coverPickRetry 是整条候选链都取不到时的冷却时间。正在播放页每帧都会
	// 问一次封面，没有这个窗口就会变成对 CDN 的连续 404 轰炸。
	coverPickRetry = 10 * time.Minute
	// coverPickRetryTransient 是网络类失败（不是「这张专辑没有该档」）的
	// 冷却时间：断网恢复后封面该很快回来，不该等满 10 分钟。
	coverPickRetryTransient = 30 * time.Second
	// coverKeep 是候选链大图（正在播放页封面）的缓存上限。
	coverKeep = 4
)

var (
	errCoverTooLarge = errors.New("cover response too large")
	// errCoverNotFound 是 CDN 明确没有这一档（4xx）。正在播放页按它决定
	// 是继续降级还是按网络失败短暂冷却。
	errCoverNotFound = errors.New("cover not found")
)

func NewCoverCache() *CoverCache {
	ctx, cancel := context.WithCancel(context.Background())
	return &CoverCache{
		m:         map[string]*ui.Bitmap{},
		raw:       map[string][]byte{},
		blur:      map[string]*ui.Bitmap{},
		inflight:  map[string]bool{},
		epoch:     map[string]uint64{},
		pick:      map[string]string{},
		pickRetry: map[string]time.Time{},
		pickBusy:  map[string]bool{},
		hc:        &http.Client{Timeout: 20 * time.Second},
		ctx:       ctx,
		cancel:    cancel,
		slots:     make(chan struct{}, coverWorkers),
	}
}

// Close 取消尚未完成的封面下载，并阻止新任务开始。幂等。
func (cc *CoverCache) Close() {
	if cc.cancel != nil {
		cc.cancel()
	}
}

// Get 返回封面位图；没有则触发异步加载并返回 nil（视图画占位）。
// redraw 在加载完成后被调用（需切回主线程）。
func (cc *CoverCache) Get(url string, redraw func()) *ui.Bitmap {
	if url == "" {
		return nil
	}
	cc.mu.Lock()
	if bmp, ok := cc.m[url]; ok {
		cc.mu.Unlock()
		return bmp
	}
	busy := cc.inflight[url]
	cc.inflight[url] = true
	epoch := cc.epoch[url]
	cc.mu.Unlock()
	if !busy {
		go cc.load(url, epoch, redraw)
	}
	return nil
}

func (cc *CoverCache) load(url string, epoch uint64, redraw func()) {
	defer func() {
		cc.mu.Lock()
		if cc.epoch[url] == epoch {
			delete(cc.inflight, url)
		}
		cc.mu.Unlock()
	}()
	select {
	case cc.slots <- struct{}{}:
		defer func() { <-cc.slots }()
	case <-cc.ctx.Done():
		return
	}
	data, err := cc.fetch(url)
	if err != nil {
		return
	}
	bmp, err := ui.DecodeBitmap(data)
	if err != nil {
		return
	}
	cc.mu.Lock()
	if cc.epoch[url] != epoch {
		cc.mu.Unlock()
		return
	}
	cc.m[url] = bmp
	cc.raw[url] = data
	cc.mu.Unlock()
	if redraw != nil {
		redraw()
	}
}

// GetBest 在候选链（高 → 低）里挑第一个能取到的封面。首项作为指纹，
// 命中结果会记住；换曲才重新从最高档开始试。全部不可用时返回 nil，
// 并在 coverPickRetry 内不再重复探测，视图画占位。
//
// 为什么需要它：QQ 音乐的专辑封面并非每张都有全部档位，正在播放页
// 直接固定 500 档会拿到放大的糊图，而固定 1500 档又会在缺档时整张空白。
func (cc *CoverCache) GetBest(urls []string, redraw func()) *ui.Bitmap {
	if len(urls) == 0 {
		return nil
	}
	key := urls[0]
	if key == "" {
		return nil
	}
	cc.mu.Lock()
	if picked := cc.pick[key]; picked != "" {
		if bmp, ok := cc.m[picked]; ok {
			cc.mu.Unlock()
			return bmp
		}
		// 命中过但位图已被清理（例如缓存失效）：回到链上重新确认。
		delete(cc.pick, key)
	}
	if cc.pickBusy[key] || time.Now().Before(cc.pickRetry[key]) {
		// 正在挑/冷却中：先用链上已有档位垫着（小图放大总比空白好），
		// 挑定后 redraw 会换成命中档。
		bmp := cachedBitmapLocked(cc, urls)
		cc.mu.Unlock()
		return bmp
	}
	cc.pickBusy[key] = true
	placeholder := cachedBitmapLocked(cc, urls)
	cc.mu.Unlock()
	go cc.pickBest(key, urls, redraw)
	return placeholder
}

// GetBestBlur 与 GetBest 同一条候选链，但返回命中档位的模糊底图
// （正在播放页背景）。链还没定档时先触发挑选并返回 nil。
func (cc *CoverCache) GetBestBlur(urls []string, redraw func()) *ui.Bitmap {
	if len(urls) == 0 || urls[0] == "" {
		return nil
	}
	cc.mu.Lock()
	picked := cc.pick[urls[0]]
	needPick := picked == "" && !cc.pickBusy[urls[0]] && !time.Now().Before(cc.pickRetry[urls[0]])
	if picked == "" {
		// 还没定档：先用链上已缓存的最高档做底图，定档后自动换。
		picked = highestCachedLocked(cc, urls)
	}
	cc.mu.Unlock()
	if needPick {
		cc.GetBest(urls, redraw)
	}
	if picked == "" {
		return nil
	}
	return cc.GetBlur(picked, redraw)
}

// highestCachedLocked 返回候选链上已缓存（原始字节在位）的最高档 URL。
func highestCachedLocked(cc *CoverCache, urls []string) string {
	for _, url := range urls {
		if _, ok := cc.raw[url]; ok {
			return url
		}
	}
	return ""
}

// cachedBitmapLocked 返回候选链上已解码的最高档位图。
func cachedBitmapLocked(cc *CoverCache, urls []string) *ui.Bitmap {
	for _, url := range urls {
		if bmp, ok := cc.m[url]; ok {
			return bmp
		}
	}
	return nil
}

// pickBest 依次尝试候选 URL，把第一个成功的档位写回 pick。
//
// 全部失败时按失败类型决定冷却：CDN 明确 404 说明这张专辑没有更高档，
// 没必要反复问；网络错误/超时只是暂时不可达，短冷却后应尽快重试。
func (cc *CoverCache) pickBest(key string, urls []string, redraw func()) {
	done := func(picked string, retryAfter time.Duration) {
		cc.mu.Lock()
		delete(cc.pickBusy, key)
		if picked != "" {
			cc.pick[key] = picked
			delete(cc.pickRetry, key)
			cc.rememberPickedLocked(picked)
		} else if retryAfter > 0 {
			cc.pickRetry[key] = time.Now().Add(retryAfter)
		}
		cc.mu.Unlock()
		if picked != "" && redraw != nil {
			redraw()
		}
	}

	select {
	case cc.slots <- struct{}{}:
		defer func() { <-cc.slots }()
	case <-cc.ctx.Done():
		done("", 0)
		return
	}

	transient := false
	for _, url := range urls {
		if url == "" {
			continue
		}
		cc.mu.Lock()
		_, cached := cc.m[url]
		cc.mu.Unlock()
		if cached {
			// 别的视图（播放条、队列）已经拉过这一档，直接沿用。
			done(url, 0)
			return
		}
		cc.mu.Lock()
		epoch := cc.epoch[url]
		cc.mu.Unlock()
		data, err := cc.fetch(url)
		if err != nil {
			if cc.ctx.Err() != nil {
				done("", 0)
				return
			}
			if !errors.Is(err, errCoverNotFound) {
				transient = true
			}
			continue
		}
		bmp, err := ui.DecodeBitmap(data)
		if err != nil {
			// 200 但不是可解码的图片：当缺档处理，换下一档。
			continue
		}
		cc.mu.Lock()
		stale := cc.epoch[url] != epoch
		if !stale {
			cc.m[url] = bmp
			cc.raw[url] = data
		}
		cc.mu.Unlock()
		if stale {
			// 期间被 Invalidate（换封面）：这一档的结论不再可信，留给下一次重挑。
			done("", 0)
			return
		}
		done(url, 0)
		return
	}
	if transient {
		done("", coverPickRetryTransient)
	} else {
		done("", coverPickRetry)
	}
}

// rememberPickedLocked 把命中的大图放进有界缓存，超出上限的旧图连同
// 原始字节、模糊底图一起丢掉（仍在使用它的视图持有指针，不受影响）。
func (cc *CoverCache) rememberPickedLocked(url string) {
	for i, u := range cc.picked {
		if u == url {
			cc.picked = append(cc.picked[:i], cc.picked[i+1:]...)
			break
		}
	}
	cc.picked = append(cc.picked, url)
	for len(cc.picked) > coverKeep {
		old := cc.picked[0]
		cc.picked = cc.picked[1:]
		delete(cc.m, old)
		delete(cc.raw, old)
		delete(cc.blur, old)
	}
}

// Invalidate 丢掉一张封面及其模糊底图，下一次 Get 会重新拉取。网盘
// 导入/替换封面后地址不变，只能靠显式失效让新图立即生效。
func (cc *CoverCache) Invalidate(url string) {
	if url == "" {
		return
	}
	cc.mu.Lock()
	cc.epoch[url]++
	delete(cc.m, url)
	delete(cc.raw, url)
	delete(cc.blur, url)
	delete(cc.inflight, url)
	// 候选链的命中结果也依赖这张图：地址不变时（网盘换封面）必须重挑，
	// 否则会一直指向刚被作废的那一档。
	for key, picked := range cc.pick {
		if key == url || picked == url {
			delete(cc.pick, key)
			delete(cc.pickRetry, key)
		}
	}
	cc.mu.Unlock()
}

func (cc *CoverCache) fetch(url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(cc.ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := cc.hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		if resp.StatusCode == http.StatusNotFound {
			return nil, errCoverNotFound
		}
		return nil, io.EOF
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxCoverBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxCoverBytes {
		return nil, errCoverTooLarge
	}
	return data, nil
}

// GetBlur 返回模糊底图（正在播放页背景）；独立缓存，仅派生一次。
func (cc *CoverCache) GetBlur(url string, redraw func()) *ui.Bitmap {
	if url == "" {
		return nil
	}
	cc.mu.Lock()
	if bmp, ok := cc.blur[url]; ok {
		cc.mu.Unlock()
		return bmp
	}
	_, hasRaw := cc.raw[url]
	cc.mu.Unlock()

	if !hasRaw {
		cc.Get(url, func() { cc.deriveBlur(url, redraw) })
		return nil
	}
	cc.deriveBlur(url, redraw)
	cc.mu.Lock()
	bmp := cc.blur[url]
	cc.mu.Unlock()
	return bmp
}

func (cc *CoverCache) deriveBlur(url string, redraw func()) {
	cc.mu.Lock()
	raw := cc.raw[url]
	_, done := cc.blur[url]
	cc.mu.Unlock()
	if raw == nil || done {
		return
	}
	img, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return
	}
	cc.mu.Lock()
	cc.blur[url] = ui.NewBitmap(colorprobe.Blurred(img))
	cc.mu.Unlock()
	if redraw != nil {
		redraw()
	}
}

// PutRaw 直接写入一张已就绪的图片（如登录二维码）。
func (cc *CoverCache) PutRaw(url string, raw []byte) {
	if len(raw) == 0 {
		return
	}
	bmp, err := ui.DecodeBitmap(raw)
	if err != nil {
		return
	}
	cc.mu.Lock()
	cc.m[url] = bmp
	cc.raw[url] = raw
	cc.mu.Unlock()
}

// FetchRaw 返回封面原始字节（已缓存的直接给，没有的返回 nil 不触发加载）。
func (cc *CoverCache) FetchRaw(url string) []byte {
	if url == "" {
		return nil
	}
	cc.mu.Lock()
	defer cc.mu.Unlock()
	return cc.raw[url]
}

// songCoverURL 是播放器曲目的封面地址：微云网盘条目优先用后端保存的
// 自定义/刮削封面，其余情况回退 QQ 曲库专辑封面。
func (a *App) songCoverURL(s player.Song, size int) string {
	return a.cloudAwareCoverURL(s.CloudFileID, s.CloudKey, s.CloudHasCover, s.AlbumPmid, s.AlbumMid, size)
}

// npCoverSizes 是正在播放页大封面的候选档位（高 → 低）。1500 是 QQ 音乐
// 专辑封面的最高档，且并非每张专辑都有全部档位，所以按序回落而不是固定一档。
var npCoverSizes = []int{1500, 1200, 800, 500, 300, 150, 90}

// npBlurSizes 是正在播放页背景模糊底图的候选档位。底图最终只有 48px 宽，
// 用 1500 档原图派生纯属浪费（解码一张 1500×1500 会卡住一帧）。
var npBlurSizes = []int{500, 300, 150, 90}

// songCoverURLs 返回一张封面的候选地址（高 → 低），供 CoverCache.GetBest
// 逐级降级。微云/网盘封面只有一个地址，去重后仍是单元素。
func (a *App) songCoverURLs(s player.Song, sizes []int) []string {
	if s.CloudFileID != "" && s.CloudKey != "" && (s.CloudHasCover || a.cloudCoverJustImported(s.CloudKey)) && a.API != nil {
		return []string{a.API.CloudCoverURL(s.CloudKey)}
	}
	urls := make([]string, 0, len(sizes))
	for _, size := range sizes {
		if url := coverURL(s.AlbumPmid, s.AlbumMid, size); url != "" {
			urls = append(urls, url)
		}
	}
	return urls
}

// queueRowCoverURL 是队列行视图的封面地址（QueueRowView 只带渲染字段）。
func (a *App) queueRowCoverURL(r player.QueueRowView, size int) string {
	return a.cloudAwareCoverURL(r.CloudFileID, r.CloudKey, r.CloudHasCover, r.AlbumPmid, r.AlbumMid, size)
}

func (a *App) cloudAwareCoverURL(cloudFileID, cloudKey string, hasCover bool, pmid, mid string, size int) string {
	if cloudFileID != "" && cloudKey != "" && (hasCover || a.cloudCoverJustImported(cloudKey)) && a.API != nil {
		return a.API.CloudCoverURL(cloudKey)
	}
	return coverURL(pmid, mid, size)
}

func (a *App) cloudCoverJustImported(key string) bool {
	if key == "" {
		return false
	}
	_, ok := a.cloudCoverFresh.Load(key)
	return ok
}

// noteCloudCoverImported 让导入的封面立刻参与地址选择，并作废旧缓存。
// 后端库列表稍后才会把 hasCover 置为 true，当前播放曲目不能等它。
func (a *App) noteCloudCoverImported(key string) {
	if key == "" {
		return
	}
	a.cloudCoverFresh.Store(key, true)
	if a.Covers != nil && a.API != nil {
		a.Covers.Invalidate(a.API.CloudCoverURL(key))
	}
}

// coverURL 拼封面地址（pmid 优先，mid 兜底；均空返回 ""）。
func coverURL(pmid, mid string, size int) string {
	id := pmid
	if id == "" {
		id = mid
	}
	if id == "" {
		return ""
	}
	return "https://y.gtimg.cn/music/photo_new/T002R" + strconv.Itoa(size) + "x" + strconv.Itoa(size) + "M000" + id + ".jpg"
}
