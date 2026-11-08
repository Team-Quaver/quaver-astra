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
	epoch  map[string]uint64
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
)

var errCoverTooLarge = errors.New("cover response too large")

func NewCoverCache() *CoverCache {
	ctx, cancel := context.WithCancel(context.Background())
	return &CoverCache{
		m:        map[string]*ui.Bitmap{},
		raw:      map[string][]byte{},
		blur:     map[string]*ui.Bitmap{},
		inflight: map[string]bool{},
		epoch:    map[string]uint64{},
		hc:       &http.Client{Timeout: 20 * time.Second},
		ctx:      ctx,
		cancel:   cancel,
		slots:    make(chan struct{}, coverWorkers),
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
