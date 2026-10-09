//go:build ignore

// 生成 Gleam 应用图标。图形来源是 assets/gleam-logo.svg 里的同一条路径（官方折角 "<" 标记，
// 黄绿 #d7fb58，底色 #0b0b0a），这里只做栅格化，改图形请先改 SVG 再同步 markPath。
//
// 产物：
//
//	assets/gleam.ico                     Windows 图标：16/24/32/48/64/128/256（PNG 压缩 ICO）
//	assets/icons/gleam-<N>.png           圆角方块 PNG：16…1024（构建、托盘、Electron 都从这里取）
//	assets/icons/gleam-macos-1024.png    macOS 网格版：主体 824/1024 居中留边
//	assets/gleam.icns                    macOS 图标：ic07…ic14（内含 PNG）
//
// 用法：go run scripts/make-icon.go
// Windows 资源（exe 图标）再跑一次：rsrc -ico assets/gleam.ico -arch amd64 -o cmd/gleam/rsrc_windows_amd64.syso
package main

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// markPath 与 assets/gleam-logo.svg 完全相同（1024×1024 坐标系，由参考稿矢量化得到）。
const markPath = "M239.1 488.5 L243.2 453.7 L247.3 443.5 L257.5 433.3 L288.2 419 L290.2 414.9 L304.5 410.8 L339.3 392.4 L345.4 392.4 L353.6 386.3 L369.9 382.2 L378.1 376.1 L419 361.8 L453.7 343.4 L459.9 343.4 L472.1 335.2 L478.3 335.2 L490.5 327 L496.7 327 L508.9 318.9 L515.1 318.9 L582.5 286.1 L588.6 286.1 L633.6 263.7 L654.1 257.5 L666.3 249.4 L701.1 237.1 L713.3 228.9 L729.7 226.9 L739.9 231 L750.1 243.2 L752.2 320.9 L748.1 345.4 L737.9 357.7 L733.8 357.7 L729.7 363.8 L715.4 369.9 L713.3 374 L664.3 398.6 L658.1 398.6 L645.9 406.7 L639.7 406.7 L607 423.1 L600.9 423.1 L547.8 449.7 L541.6 449.7 L529.4 457.8 L504.8 466 L488.5 476.2 L482.4 476.2 L470.1 484.4 L445.6 492.6 L429.2 502.8 L423.1 502.8 L410.8 511 L386.3 519.2 L369.9 529.4 L408.8 547.8 L414.9 547.8 L423.1 553.9 L429.2 553.9 L437.4 560 L443.5 560 L451.7 566.2 L457.8 566.2 L466 572.3 L472.1 572.3 L480.3 578.4 L486.5 578.4 L494.6 584.6 L500.8 584.6 L508.9 590.7 L515.1 590.7 L523.2 596.8 L529.4 596.8 L537.5 603 L543.7 603 L551.9 609.1 L558 609.1 L566.2 615.2 L572.3 615.2 L580.5 621.3 L586.6 621.3 L613.2 635.7 L619.3 635.7 L631.6 643.8 L637.7 643.8 L654.1 654.1 L660.2 654.1 L680.6 666.3 L686.8 666.3 L723.5 684.7 L725.6 688.8 L739.9 694.9 L744 701.1 L754.2 750.1 L754.2 778.7 L746 809.4 L731.7 823.7 L709.2 821.7 L688.8 811.4 L682.7 811.4 L674.5 805.3 L668.4 805.3 L656.1 797.1 L650 797.1 L641.8 791 L635.7 791 L627.5 784.9 L621.3 784.9 L613.2 778.7 L582.5 768.5 L574.3 762.4 L568.2 762.4 L560 756.2 L553.9 756.2 L545.7 750.1 L539.6 750.1 L531.4 744 L525.3 744 L517.1 737.9 L511 737.9 L502.8 731.7 L496.7 731.7 L488.5 725.6 L482.4 725.6 L423.1 697 L417 697 L404.7 688.8 L380.2 680.6 L363.8 670.4 L357.7 670.4 L337.2 658.1 L331.1 658.1 L269.8 627.5 L267.8 623.4 L259.6 621.3 L249.4 613.2 L243.2 596.8 L239.1 562.1 L239.1 490.5Z"

var (
	bgColor   = color.NRGBA{0x0b, 0x0b, 0x0a, 0xff}
	markColor = color.NRGBA{0xd7, 0xfb, 0x58, 0xff}
)

const cornerRatio = 0.229 // 圆角半径 / 边长

type pt struct{ x, y float64 }

// parsePath 只认 potrace 输出用到的 M / C / Z（绝对坐标），返回展平后的多边形。
func parsePath(d string) [][]pt {
	r := strings.NewReplacer("M", " M ", "L", " L ", "C", " C ", "Z", " Z ", ",", " ")
	tok := strings.Fields(r.Replace(d))
	var polys [][]pt
	var cur []pt
	num := func(i int) float64 { v, _ := strconv.ParseFloat(tok[i], 64); return v }
	for i := 0; i < len(tok); {
		switch tok[i] {
		case "M":
			if len(cur) > 0 {
				polys = append(polys, cur)
			}
			cur = []pt{{num(i + 1), num(i + 2)}}
			i += 3
		case "L":
			i++
			for i+1 < len(tok) && !isCmd(tok[i]) {
				cur = append(cur, pt{num(i), num(i + 1)})
				i += 2
			}
		case "C":
			i++
			for i+5 < len(tok) && !isCmd(tok[i]) {
				p0 := cur[len(cur)-1]
				p1, p2, p3 := pt{num(i), num(i + 1)}, pt{num(i + 2), num(i + 3)}, pt{num(i + 4), num(i + 5)}
				for k := 1; k <= 24; k++ {
					t := float64(k) / 24
					u := 1 - t
					cur = append(cur, pt{
						u*u*u*p0.x + 3*u*u*t*p1.x + 3*u*t*t*p2.x + t*t*t*p3.x,
						u*u*u*p0.y + 3*u*u*t*p1.y + 3*u*t*t*p2.y + t*t*t*p3.y,
					})
				}
				i += 6
			}
		case "Z":
			polys = append(polys, cur)
			cur = nil
			i++
		default:
			i++
		}
	}
	if len(cur) > 0 {
		polys = append(polys, cur)
	}
	return polys
}

func isCmd(s string) bool { return s == "M" || s == "L" || s == "C" || s == "Z" }

// coverage 用 n×n 子采样 + 非零环绕扫描线算每个像素被多边形覆盖的比例。
func coverage(polys [][]pt, size int, scale, offX, offY float64) []float64 {
	const n = 8
	cov := make([]float64, size*size)
	for y := 0; y < size; y++ {
		for sy := 0; sy < n; sy++ {
			py := float64(y) + (float64(sy)+0.5)/n
			type cross struct {
				x float64
				w int
			}
			var xs []cross
			for _, poly := range polys {
				for i := range poly {
					a, b := poly[i], poly[(i+1)%len(poly)]
					ay, by := a.y*scale+offY, b.y*scale+offY
					if (ay <= py) == (by <= py) {
						continue
					}
					t := (py - ay) / (by - ay)
					x := a.x*scale + offX + t*(b.x-a.x)*scale
					w := 1
					if by < ay {
						w = -1
					}
					xs = append(xs, cross{x, w})
				}
			}
			sort.Slice(xs, func(i, j int) bool { return xs[i].x < xs[j].x })
			wind := 0
			for k := 0; k+1 <= len(xs); k++ {
				wind += xs[k].w
				if wind == 0 || k+1 == len(xs) {
					continue
				}
				x0, x1 := xs[k].x, xs[k+1].x
				for sx := 0; sx < n; sx++ {
					off := (float64(sx) + 0.5) / n
					for x := int(math.Floor(x0 - off)); x <= int(math.Ceil(x1)); x++ {
						px := float64(x) + off
						if px >= x0 && px < x1 && x >= 0 && x < size {
							cov[y*size+x] += 1.0 / (n * n)
						}
					}
				}
			}
		}
	}
	return cov
}

// roundedCoverage 圆角方块的覆盖率（同样子采样，边缘抗锯齿）。
func roundedCoverage(size int, x0, y0, w, rad float64) []float64 {
	const n = 8
	cov := make([]float64, size*size)
	inside := func(px, py float64) bool {
		if px < x0 || py < y0 || px > x0+w || py > y0+w {
			return false
		}
		cx := math.Max(x0+rad, math.Min(px, x0+w-rad))
		cy := math.Max(y0+rad, math.Min(py, y0+w-rad))
		return math.Hypot(px-cx, py-cy) <= rad
	}
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			c := 0
			for sy := 0; sy < n; sy++ {
				for sx := 0; sx < n; sx++ {
					if inside(float64(x)+(float64(sx)+0.5)/n, float64(y)+(float64(sy)+0.5)/n) {
						c++
					}
				}
			}
			cov[y*size+x] = float64(c) / (n * n)
		}
	}
	return cov
}

// drawIcon 画一个 size 像素的图标；body 是方块占边长的比例（macOS 网格用 824/1024），rounded 控制是否圆角。
func drawIcon(size int, body float64, rounded bool) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	s := float64(size)
	w := s * body
	x0 := (s - w) / 2
	rad := 0.0
	if rounded {
		rad = w * cornerRatio
	}
	tile := roundedCoverage(size, x0, x0, w, rad)
	mark := coverage(parsePath(markPath), size, w/1024, x0, x0)
	for i := range tile {
		ta := tile[i]
		ma := math.Min(mark[i], ta)
		if ta <= 0 {
			continue
		}
		mix := func(b, m uint8) uint8 {
			if ta == 0 {
				return 0
			}
			v := (float64(b)*(ta-ma) + float64(m)*ma) / ta
			return uint8(math.Round(v))
		}
		img.Pix[i*4+0] = mix(bgColor.R, markColor.R)
		img.Pix[i*4+1] = mix(bgColor.G, markColor.G)
		img.Pix[i*4+2] = mix(bgColor.B, markColor.B)
		img.Pix[i*4+3] = uint8(math.Round(ta * 255))
	}
	return img
}

func encode(img image.Image) []byte {
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		panic(err)
	}
	return buf.Bytes()
}

func writeICO(path string, sizes []int) {
	var out bytes.Buffer
	le := binary.LittleEndian
	_ = binary.Write(&out, le, [3]uint16{0, 1, uint16(len(sizes))})
	var blobs [][]byte
	for _, s := range sizes {
		blobs = append(blobs, encode(drawIcon(s, 1, true)))
	}
	offset := 6 + 16*len(sizes)
	for i, s := range sizes {
		dim := uint8(s)
		if s >= 256 {
			dim = 0
		}
		out.Write([]byte{dim, dim, 0, 0})
		_ = binary.Write(&out, le, uint16(1))
		_ = binary.Write(&out, le, uint16(32))
		_ = binary.Write(&out, le, uint32(len(blobs[i])))
		_ = binary.Write(&out, le, uint32(offset))
		offset += len(blobs[i])
	}
	for _, b := range blobs {
		out.Write(b)
	}
	must(os.WriteFile(path, out.Bytes(), 0o644))
}

// writeICNS 写 macOS 图标：每个条目是「4 字节类型 + 4 字节大端长度 + PNG」。
func writeICNS(path string) {
	entries := []struct {
		typ  string
		size int
	}{{"ic11", 32}, {"ic12", 64}, {"ic07", 128}, {"ic08", 256}, {"ic13", 256}, {"ic09", 512}, {"ic14", 512}, {"ic10", 1024}}
	var body bytes.Buffer
	for _, e := range entries {
		data := encode(drawIcon(e.size, 824.0/1024, true))
		body.WriteString(e.typ)
		_ = binary.Write(&body, binary.BigEndian, uint32(8+len(data)))
		body.Write(data)
	}
	var out bytes.Buffer
	out.WriteString("icns")
	_ = binary.Write(&out, binary.BigEndian, uint32(8+body.Len()))
	out.Write(body.Bytes())
	must(os.WriteFile(path, out.Bytes(), 0o644))
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}

func main() {
	must(os.MkdirAll(filepath.Join("assets", "icons"), 0o755))
	writeICO(filepath.Join("assets", "gleam.ico"), []int{16, 24, 32, 48, 64, 128, 256})
	for _, s := range []int{16, 24, 32, 48, 64, 128, 256, 512, 1024} {
		must(os.WriteFile(filepath.Join("assets", "icons", "gleam-"+strconv.Itoa(s)+".png"), encode(drawIcon(s, 1, true)), 0o644))
	}
	must(os.WriteFile(filepath.Join("assets", "icons", "gleam-macos-1024.png"), encode(drawIcon(1024, 824.0/1024, true)), 0o644))
	writeICNS(filepath.Join("assets", "gleam.icns"))
}
