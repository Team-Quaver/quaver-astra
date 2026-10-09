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

	"github.com/egoist/mygo/ui"
)

// CoverCache 按 URL 缓存封面 *ui.Bitmap；未命中时后台拉取，完成后触发重绘。
type CoverCache struct {
	mu       sync.Mutex
	m        map[string]*ui.Bitmap
	raw      map[string][]byte // 原始字节，供派生模糊底图
	blur     map[string]*ui.Bitmap
	inflight map[string]bool
	hc       *http.Client
	ctx      context.Context
	cancel   context.CancelFunc
	slots    chan struct{}
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
	cc.mu.Unlock()
	if !busy {
		go cc.load(url, redraw)
	}
	return nil
}

func (cc *CoverCache) load(url string, redraw func()) {
	defer func() {
		cc.mu.Lock()
		delete(cc.inflight, url)
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
	cc.m[url] = bmp
	cc.raw[url] = data
	cc.mu.Unlock()
	if redraw != nil {
		redraw()
	}
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
