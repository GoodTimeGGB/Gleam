//go:build ignore

// 生成 Gleam 应用图标 assets/gleam.ico（多尺寸 PNG 压缩 ICO）。
// 用法：go run scripts/make-icon.go
package main

import (
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"path/filepath"
)

// drawIcon 逐像素绘制"微光"图标：深色圆角底 + 中心光点 + 光晕 + 八向光线。
func drawIcon(size int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	cx := float64(size) * 0.5
	cy := float64(size) * 0.42
	core := float64(size) * 0.20
	// 预计算光线段（8 向）
	type seg struct{ x1, y1, x2, y2 float64 }
	var rays []seg
	for a := 0; a < 360; a += 45 {
		rad := float64(a) * math.Pi / 180
		inner, outer := core*1.45, core*2.15
		rays = append(rays, seg{
			cx + math.Cos(rad)*inner, cy + math.Sin(rad)*inner,
			cx + math.Cos(rad)*outer, cy + math.Sin(rad)*outer,
		})
	}
	rayW := math.Max(1.0, float64(size)*0.035)

	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			px, py := float64(x)+0.5, float64(y)+0.5
			// 背景垂直渐变 (20,26,42) → (10,14,24)
			t := py / float64(size)
			bgR := uint8(20*(1-t) + 10*t)
			bgG := uint8(26*(1-t) + 14*t)
			bgB := uint8(42*(1-t) + 24*t)
			r, g, b, a := float64(bgR), float64(bgG), float64(bgB), 255.0

			// 光晕（径向衰减）
			d := math.Hypot(px-cx, py-cy)
			if glow := 1 - d/(core*2.4); glow > 0 {
				amt := glow * glow * 0.55
				r += (150 - r) * amt
				g += (235 - g) * amt
				b += (195 - b) * amt
			}
			// 中心光点（边缘平滑）
			if d < core {
				amt := smoothstep(1 - d/core)
				r += (245 - r) * amt
				g += (255 - g) * amt
				b += (250 - b) * amt
			}
			// 光线（点到线段距离）
			for _, s := range rays {
				if distSeg(px, py, s.x1, s.y1, s.x2, s.y2) < rayW/2 {
					r += (200-r)*0.85
					g += (245-g)*0.85
					b += (215-b)*0.85
				}
			}
			// 圆角遮罩（半径 = size*0.18）
			rad := float64(size) * 0.18
			a = roundedAlpha(px, py, float64(size), rad)
			img.SetRGBA(x, y, color.RGBA{uint8(r), uint8(g), uint8(b), uint8(a)})
		}
	}
	return img
}

func smoothstep(t float64) float64 {
	t = math.Max(0, math.Min(1, t))
	return t * t * (3 - 2*t)
}

func distSeg(px, py, x1, y1, x2, y2 float64) float64 {
	dx, dy := x2-x1, y2-y1
	l2 := dx*dx + dy*dy
	if l2 == 0 {
		return math.Hypot(px-x1, py-y1)
	}
	t := ((px-x1)*dx + (py-y1)*dy) / l2
	t = math.Max(0, math.Min(1, t))
	return math.Hypot(px-(x1+t*dx), py-(y1+t*dy))
}

// roundedAlpha 计算圆角矩形外为 0 的 alpha。
func roundedAlpha(px, py, size, rad float64) float64 {
	in := func(v, lo, hi float64) bool { return v >= lo && v <= hi }
	if in(px, rad, size-rad) || in(py, rad, size-rad) {
		return 255
	}
	cx := math.Max(rad, math.Min(px, size-rad))
	cy := math.Max(rad, math.Min(py, size-rad))
	if d := math.Hypot(px-cx, py-cy); d <= rad {
		return 255
	}
	return 0
}

// appendPNG 将图像以 PNG 压缩形式追加进 ICO 并写入目录项。
func main() {
	sizes := []int{256, 64, 48, 32, 16}
	type entry struct {
		size  int
		bytes []byte
	}
	var entries []entry
	for _, s := range sizes {
		var buf []byte // png.Encode 需要 io.Writer
		w := &bytesWriter{}
		if err := png.Encode(w, drawIcon(s)); err != nil {
			panic(err)
		}
		buf = w.b
		entries = append(entries, entry{s, buf})
	}

	_ = os.MkdirAll("assets", 0o755)
	f, err := os.Create(filepath.Join("assets", "gleam.ico"))
	if err != nil {
		panic(err)
	}
	defer f.Close()
	// ICONDIR
	be := newBina(f)
	be.u16(0)
	be.u16(1)
	be.u16(uint16(len(entries)))
	// ICONDIRENTRY
	offset := 6 + 16*len(entries)
	for _, e := range entries {
		dim := e.size
		if dim >= 256 {
			dim = 0
		}
		be.u8(dim)
		be.u8(dim)
		be.u8(0)
		be.u8(0)
		be.u16(1)
		be.u16(32)
		be.u32(uint32(len(e.bytes)))
		be.u32(uint32(offset))
		offset += len(e.bytes)
	}
	for _, e := range entries {
		if _, err := f.Write(e.bytes); err != nil {
			panic(err)
		}
	}
}

type bytesWriter struct{ b []byte }

func (w *bytesWriter) Write(p []byte) (int, error) {
	w.b = append(w.b, p...)
	return len(p), nil
}

type binary struct{ f *os.File }

func newBina(f *os.File) *binary { return &binary{f: f} }

func (b *binary) u8(v int) {
	var p [1]byte
	p[0] = byte(v)
	_, _ = b.f.Write(p[:])
}
func (b *binary) u16(v uint16) {
	var p [2]byte
	p[0], p[1] = byte(v), byte(v>>8)
	_, _ = b.f.Write(p[:])
}
func (b *binary) u32(v uint32) {
	var p [4]byte
	for i := 0; i < 4; i++ {
		p[i] = byte(v >> (8 * i))
	}
	_, _ = b.f.Write(p[:])
}
