package appui

import (
	"bytes"
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
}

func NewCoverCache() *CoverCache {
	return &CoverCache{
		m:        map[string]*ui.Bitmap{},
		raw:      map[string][]byte{},
		blur:     map[string]*ui.Bitmap{},
		inflight: map[string]bool{},
		hc:       &http.Client{Timeout: 20 * time.Second},
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
	resp, err := cc.hc.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, io.EOF
	}
	return io.ReadAll(resp.Body)
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
