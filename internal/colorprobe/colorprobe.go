// Package colorprobe 从封面图提取主题色（移植主项目 ui/src/lib/color.ts 的
// 思路：量化统计 + 按票数与饱和度打分），并生成正在播放页的模糊底图。
package colorprobe

import (
	"bytes"
	"image"
	"image/color"
	"image/draw"
	_ "image/jpeg" // 封面是 JPEG
	_ "image/png"
	"math"

	xdraw "golang.org/x/image/draw"
)

// Sample 把图片缩到 small×small 内再做统计，降低开销。
const small = 32

// Result 是提取出的主题色：HSL 各分量 0..1。
type Result struct{ H, S, L float64 }

// DominantFromBytes 解码图片字节并提取主色。
func DominantFromBytes(data []byte) (Result, bool) {
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return Result{}, false
	}
	return Dominant(img)
}

// Dominant 提取封面主色。没有足够彩色像素时返回 ok=false。
func Dominant(img image.Image) (Result, bool) {
	src := shrink(img, small)
	b := src.Bounds()
	const buckets = 16
	var votes [buckets]int
	var sSum, lSum [buckets]float64
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			h, s, l := rgbToHSL(src.At(x, y))
			if s < 0.14 || l < 0.08 || l > 0.94 {
				continue // 跳过灰白黑
			}
			i := int(h*float64(buckets)/360) % buckets
			votes[i]++
			sSum[i] += s
			lSum[i] += l
		}
	}
	best, bestScore := -1, 0.0
	for i, n := range votes {
		if n == 0 {
			continue
		}
		// 票数为主，饱和度加成（对齐 color.ts 的 votes*64 + saturation 思路）
		score := float64(n)*64 + (sSum[i]/float64(n))*32
		if score > bestScore {
			bestScore, best = score, i
		}
	}
	if best < 0 || votes[best] < 2 {
		return Result{}, false
	}
	return Result{
		H: (float64(best) + 0.5) * 360 / buckets,
		S: clamp(sSum[best]/float64(votes[best]), 0.35, 0.62),
		L: clamp(lSum[best]/float64(votes[best]), 0, 1),
	}, true
}

// Accent 由主色调出 UI 强调色。dark 表示当前深色主题（深色下要更亮）。
func Accent(r Result, dark bool) (r8, g8, b8 uint8) {
	if dark {
		return hslToRGB(r.H, clamp(r.S, 0.4, 0.62), clamp(r.L, 0.5, 0.66))
	}
	return hslToRGB(r.H, clamp(r.S, 0.38, 0.6), clamp(r.L, 0.3, 0.42))
}

// Glow 是进度条/氛围用的辅色：色相往青色靠，更亮。
func Glow(r Result) (r8, g8, b8 uint8) {
	h := r.H
	// 往 190°（青）方向拉 35%
	if d := 190 - h; math.Abs(d) > 1 {
		if d > 180 {
			d -= 360
		} else if d < -180 {
			d += 360
		}
		h += d * 0.35
	}
	return hslToRGB(norm360(h), clamp(r.S, 0.5, 0.68), clamp(r.L+0.08, 0.42, 0.55))
}

// Blurred 生成小尺寸强模糊图（缩小→盒式模糊），交给 GPU 放大铺底。
func Blurred(img image.Image) *image.NRGBA {
	src := shrink(img, 48)
	b := src.Bounds()
	out := image.NewNRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(out, out.Bounds(), src, b.Min, draw.Src)
	// 两轮盒式模糊近似高斯
	boxBlur(out, 2)
	boxBlur(out, 2)
	return out
}

// ---- 内部实现 ----

func shrink(img image.Image, maxSide int) *image.NRGBA {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= 0 || h <= 0 {
		return image.NewNRGBA(image.Rect(0, 0, 1, 1))
	}
	scale := 1.0
	if w > h {
		scale = float64(maxSide) / float64(w)
	} else {
		scale = float64(maxSide) / float64(h)
	}
	if scale > 1 {
		scale = 1
	}
	dw, dh := max(1, int(float64(w)*scale)), max(1, int(float64(h)*scale))
	dst := image.NewNRGBA(image.Rect(0, 0, dw, dh))
	xdraw.ApproxBiLinear.Scale(dst, dst.Bounds(), img, b, xdraw.Src, nil)
	return dst
}

func boxBlur(img *image.NRGBA, r int) {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	src := image.NewNRGBA(b)
	copy(src.Pix, img.Pix)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			var rs, gs, bs, as, n int
			for dy := -r; dy <= r; dy++ {
				for dx := -r; dx <= r; dx++ {
					xx, yy := clampInt(x+dx, 0, w-1), clampInt(y+dy, 0, h-1)
					i := src.PixOffset(b.Min.X+xx, b.Min.Y+yy)
					rs += int(src.Pix[i])
					gs += int(src.Pix[i+1])
					bs += int(src.Pix[i+2])
					as += int(src.Pix[i+3])
					n++
				}
			}
			i := img.PixOffset(b.Min.X+x, b.Min.Y+y)
			img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] =
				uint8(rs/n), uint8(gs/n), uint8(bs/n), uint8(as/n)
		}
	}
}

func rgbToHSL(c color.Color) (h, s, l float64) {
	r32, g32, b32, _ := c.RGBA()
	r, g, bl := float64(r32>>8)/255, float64(g32>>8)/255, float64(b32>>8)/255
	mx, mn := math.Max(r, math.Max(g, bl)), math.Min(r, math.Min(g, bl))
	l = (mx + mn) / 2
	if mx == mn {
		return 0, 0, l
	}
	d := mx - mn
	if l > 0.5 {
		s = d / (2 - mx - mn)
	} else {
		s = d / (mx + mn)
	}
	switch mx {
	case r:
		h = (g - bl) / d
		if g < bl {
			h += 6
		}
	case g:
		h = (bl-r)/d + 2
	default:
		h = (r-g)/d + 4
	}
	return h * 60, s, l
}

func hslToRGB(h, s, l float64) (r8, g8, b8 uint8) {
	h = norm360(h) / 360
	var r, g, b float64
	if s == 0 {
		r, g, b = l, l, l
	} else {
		var q float64
		if l < 0.5 {
			q = l * (1 + s)
		} else {
			q = l + s - l*s
		}
		p := 2*l - q
		r = hueToRGB(p, q, h+1.0/3.0)
		g = hueToRGB(p, q, h)
		b = hueToRGB(p, q, h-1.0/3.0)
	}
	return uint8(clamp(r, 0, 1) * 255), uint8(clamp(g, 0, 1) * 255), uint8(clamp(b, 0, 1) * 255)
}

func hueToRGB(p, q, t float64) float64 {
	if t < 0 {
		t++
	}
	if t > 1 {
		t--
	}
	switch {
	case t < 1.0/6.0:
		return p + (q-p)*6*t
	case t < 0.5:
		return q
	case t < 2.0/3.0:
		return p + (q-p)*(2.0/3.0-t)*6
	}
	return p
}

func norm360(h float64) float64 {
	h = math.Mod(h, 360)
	if h < 0 {
		h += 360
	}
	return h
}

func clamp(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
