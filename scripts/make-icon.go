//go:build ignore

// 生成 Gleam 应用图标 assets/gleam.ico（多尺寸 PNG 压缩 ICO）。
// 新 logo：黄绿色 "<" 折角符号 + 深色圆角背景。
// 用法：go run scripts/make-icon.go
package main

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"sort"
)

// drawIcon 绘制 Gleam 折角图标：深色圆角底 + 黄绿色 "<" 折角。
func drawIcon(size int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	s := float64(size)

	// 背景色 #1a1a1a
	bgR, bgG, bgB := uint8(0x1a), uint8(0x1a), uint8(0x1a)
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			a := uint8(roundedAlpha(float64(x)+0.5, float64(y)+0.5, s, s*0.18))
			img.SetRGBA(x, y, color.RGBA{bgR, bgG, bgB, a})
		}
	}

	thick := s * 0.18
	halfT := thick / 2
	vx, vy := s*0.20, s*0.50
	tx, ty := s*0.80, s*0.20
	bx, by := s*0.80, s*0.80

	// 上臂方向及法向量
	tDX, tDY := tx-vx, ty-vy
	tLen := math.Hypot(tDX, tDY)
	tNX, tNY := -tDY/tLen, tDX/tLen

	// 下臂方向及法向量
	bDX, bDY := bx-vx, by-vy
	bLen := math.Hypot(bDX, bDY)
	bNX, bNY := -bDY/bLen, bDX/bLen

	chevCol := color.RGBA{0xC8, 0xE8, 0x4D, 255}

	// 上臂平行四边形四角
	topArm := [4][2]float64{
		{vx - tNX*halfT, vy - tNY*halfT},
		{tx - tNX*halfT, ty - tNY*halfT},
		{tx + tNX*halfT, ty + tNY*halfT},
		{vx + tNX*halfT, vy + tNY*halfT},
	}
	// 下臂平行四边形四角
	botArm := [4][2]float64{
		{vx - bNX*halfT, vy - bNY*halfT},
		{bx - bNX*halfT, by - bNY*halfT},
		{bx + bNX*halfT, by + bNY*halfT},
		{vx + bNX*halfT, vy + bNY*halfT},
	}

	fillPoly(img, topArm[:], chevCol)
	fillPoly(img, botArm[:], chevCol)

	// 臂端圆头
	drawCap(img, tx, ty, halfT, chevCol)
	drawCap(img, bx, by, halfT, chevCol)

	return img
}

// fillPoly 用扫描线填充凸多边形。
func fillPoly(img *image.RGBA, poly [][2]float64, col color.RGBA) {
	n := len(poly)
	size := img.Bounds().Dx()
	for y := 0; y < size; y++ {
		py := float64(y) + 0.5
		var xs []float64
		for i := 0; i < n; i++ {
			j := (i + 1) % n
			y0, y1 := poly[i][1], poly[j][1]
			if (y0 <= py && py < y1) || (y1 <= py && py < y0) {
				t := (py - y0) / (y1 - y0)
				xs = append(xs, poly[i][0]+t*(poly[j][0]-poly[i][0]))
			}
		}
		sort.Float64s(xs)
		for k := 0; k+1 < len(xs); k += 2 {
			x0 := int(math.Ceil(xs[k] - 0.5))
			x1 := int(math.Floor(xs[k+1] - 0.5))
			for x := x0; x <= x1; x++ {
				if x >= 0 && x < size && img.At(x, y).(color.RGBA).A > 0 {
					img.SetRGBA(x, y, col)
				}
			}
		}
	}
}

// drawCap 在指定位置画一个实心圆，用于臂端圆头。
func drawCap(img *image.RGBA, cx, cy, r float64, col color.RGBA) {
	size := img.Bounds().Dx()
	ri := int(math.Ceil(r))
	for dy := -ri; dy <= ri; dy++ {
		for dx := -ri; dx <= ri; dx++ {
			x, y := int(cx)+dx, int(cy)+dy
			if x < 0 || x >= size || y < 0 || y >= size {
				continue
			}
			if math.Hypot(float64(x)+0.5-cx, float64(y)+0.5-cy) <= r {
				if img.At(x, y).(color.RGBA).A > 0 {
					img.SetRGBA(x, y, col)
				}
			}
		}
	}
}

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

func main() {
	sizes := []int{256, 64, 48, 32, 16}
	type entry struct {
		size  int
		bytes []byte
	}
	var entries []entry
	for _, s := range sizes {
		var buf bytes.Buffer
		if err := png.Encode(&buf, drawIcon(s)); err != nil {
			panic(err)
		}
		entries = append(entries, entry{s, buf.Bytes()})
	}

	_ = os.MkdirAll("assets", 0o755)
	f, err := os.Create(filepath.Join("assets", "gleam.ico"))
	if err != nil {
		panic(err)
	}
	defer f.Close()
	be := newBina(f)
	be.u16(0)
	be.u16(1)
	be.u16(uint16(len(entries)))
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
