// 生成 Windows 安装程序的两张品牌图（NSIS 安装页的固定尺寸，改不了）：
//
//	assets/installer-header.bmp   150×57   （安装页右上角那条）
//	assets/installer-sidebar.bmp  164×314  （欢迎页/完成页左侧那一条）
//
// 为什么要自己写：这两张图必须是**不压缩的 24 位 BMP**，而标准库只有 PNG 解码、没有 BMP 编码；
// 引一个图像库进来只为写两张图，与本仓库"零第三方依赖"的前提不成比例，所以自己拼头。
// 为什么不用 SVG 源图：光栅化 SVG 要引库；这里用已经打好的一张 PNG 做最近邻缩放即可——
// 安装页上它是几十像素高的小标，插值与否看不出来。
//
//	go run scripts/make-installer-art.go
package main

import (
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
)

const (
	headerW, headerH   = 150, 57
	sidebarW, sidebarH = 164, 314
)

// 品牌底色：与 assets/gleam.ico 的浅色底一致的一层米白，深色图上放标才不糊。
var bgColor = color.RGBA{R: 0xF5, G: 0xF5, B: 0xF2, A: 0xFF}

func main() {
	logo := loadPNG("assets/icons/gleam-256.png")
	if err := writeBMP("assets/installer-header.bmp", compose(headerW, headerH, logo, 40)); err != nil {
		fail(err)
	}
	if err := writeBMP("assets/installer-sidebar.bmp", compose(sidebarW, sidebarH, logo, 96)); err != nil {
		fail(err)
	}
	fmt.Println("已生成 assets/installer-header.bmp（150×57）与 assets/installer-sidebar.bmp（164×314）")
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "生成安装图失败：", err)
	os.Exit(1)
}

func loadPNG(path string) image.Image {
	f, err := os.Open(path)
	if err != nil {
		fail(err)
	}
	defer f.Close()
	img, err := png.Decode(f)
	if err != nil {
		fail(err)
	}
	return img
}

// compose 铺底 + 把 logo 缩到 want 高、水平居中。缩放用最近邻：
// 目标是几十像素的小标，插值只会多写代码。
func compose(w, h int, logo image.Image, want int) *image.RGBA {
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			dst.Set(x, y, bgColor)
		}
	}
	b := logo.Bounds()
	scale := float64(want) / float64(b.Dy())
	sw, sh := int(float64(b.Dx())*scale), want
	ox, oy := (w-sw)/2, (h-sh)/2
	for y := 0; y < sh; y++ {
		for x := 0; x < sw; x++ {
			sx := b.Min.X + x*b.Dx()/sw
			sy := b.Min.Y + y*b.Dy()/sh
			src := color.NRGBAModel.Convert(logo.At(sx, sy)).(color.NRGBA)
			if src.A == 0 {
				continue
			}
			// 源图有透明边：按 alpha 混到底上，免得边缘出现黑框。
			base := dst.RGBAAt(ox+x, oy+y)
			a := float64(src.A) / 255
			dst.SetRGBA(ox+x, oy+y, color.RGBA{
				R: uint8(float64(src.R)*a + float64(base.R)*(1-a)),
				G: uint8(float64(src.G)*a + float64(base.G)*(1-a)),
				B: uint8(float64(src.B)*a + float64(base.B)*(1-a)),
				A: 0xFF,
			})
		}
	}
	return dst
}

// writeBMP 写一张 24 位、不压缩的 BMP（NSIS 只吃这一种）。行按 4 字节对齐、像素顺序是 BGR、
// 且**自下而上**——这三条少一条，装出来的图就是花屏。
func writeBMP(path string, img *image.RGBA) error {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	rowPad := (4 - (w*3)%4) % 4
	pixelBytes := (w*3 + rowPad) * h
	fileSize := 14 + 40 + pixelBytes

	buf := make([]byte, 0, fileSize)
	// BITMAPFILEHEADER
	buf = append(buf, 'B', 'M')
	buf = appendLE(buf, uint32(fileSize))
	buf = appendLE(buf, uint32(0)) // 保留
	buf = appendLE(buf, uint32(14+40))
	// BITMAPINFOHEADER
	buf = appendLE(buf, uint32(40))
	buf = appendLE(buf, int32(w))
	buf = appendLE(buf, int32(h))
	buf = appendLE(buf, uint16(1))  // planes
	buf = appendLE(buf, uint16(24)) // bpp
	buf = appendLE(buf, uint32(0))  // BI_RGB，不压缩
	buf = appendLE(buf, uint32(pixelBytes))
	buf = appendLE(buf, int32(2835)) // 72 DPI
	buf = appendLE(buf, int32(2835))
	buf = appendLE(buf, uint32(0)) // 调色板
	buf = appendLE(buf, uint32(0)) // 重要色数
	pad := make([]byte, rowPad)
	for y := h - 1; y >= 0; y-- {
		for x := 0; x < w; x++ {
			c := img.RGBAAt(b.Min.X+x, b.Min.Y+y)
			buf = append(buf, c.B, c.G, c.R)
		}
		buf = append(buf, pad...)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, buf, 0o644)
}

func appendLE(buf []byte, v any) []byte {
	switch t := v.(type) {
	case uint16:
		return binary.LittleEndian.AppendUint16(buf, t)
	case uint32:
		return binary.LittleEndian.AppendUint32(buf, t)
	case int32:
		return binary.LittleEndian.AppendUint32(buf, uint32(t))
	default:
		panic("unsupported")
	}
}
