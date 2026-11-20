package survivalcraft

import (
	"encoding/binary"
	"fmt"
	"image"
	"image/draw"
	"maps"
	"math"
	"slices"

	r2 "neomega-view-utils/r2assets"
)

type scTextures struct {
	atlas *image.NRGBA
	boxes map[uint32]image.Rectangle
}

func packSCTextures(base *image.NRGBA, mods map[uint32]scModBlock) (scTextures, error) {
	t := scTextures{atlas: base, boxes: map[uint32]image.Rectangle{}}
	width := base.Bounds().Dx()
	for _, m := range mods {
		if m.tex != nil {
			width = max(width, m.tex.Bounds().Dx())
		}
	}
	width = (width + 63) &^ 63 // uniform row alignment for the two backends
	x, y, h := 0, base.Bounds().Dy(), 0
	for _, id := range slices.Sorted(maps.Keys(mods)) {
		m := mods[id]
		if m.tex == nil {
			n := base.Bounds().Dx() / 16
			slot := int(m.slot)
			t.boxes[id] = image.Rect(slot%16*n, slot/16*n, (slot%16+1)*n, (slot/16+1)*n)
			continue
		}
		bounds := m.tex.Bounds()
		w, d := bounds.Dx(), bounds.Dy()
		if x+w > width {
			x = 0
			y += h
			h = 0
		}
		t.boxes[id] = image.Rect(x, y, x+w, y+d)
		x += w
		h = max(h, d)
	}
	if h == 0 {
		return t, nil
	}
	if y+h > 4096 || width*(y+h) > 4<<20 {
		return t, fmt.Errorf("模组贴图超出 GPU 图集预算")
	}
	t.atlas = image.NewNRGBA(image.Rect(0, 0, width, y+h))
	draw.Draw(t.atlas, base.Bounds(), base, base.Bounds().Min, draw.Src)
	for id, m := range mods {
		if m.tex != nil {
			draw.Draw(t.atlas, t.boxes[id], m.tex, m.tex.Bounds().Min, draw.Src)
		}
	}
	return t, nil
}

func scDot(a, b [3]float64) float64              { return a[0]*b[0] + a[1]*b[1] + a[2]*b[2] }
func scSub(a, b [3]float64) [3]float64           { return [3]float64{a[0] - b[0], a[1] - b[1], a[2] - b[2]} }
func scScale(a [3]float64, k float64) [3]float64 { return [3]float64{a[0] * k, a[1] * k, a[2] * k} }
func scCross(a, b [3]float64) [3]float64 {
	return [3]float64{a[1]*b[2] - a[2]*b[1], a[2]*b[0] - a[0]*b[2], a[0]*b[1] - a[1]*b[0]}
}

func scOctahedron(n [3]float64) (uint32, [3]float64) {
	sign := func(x float64) float64 {
		if x < 0 {
			return -1
		}
		return 1
	}
	w := math.Abs(n[0]) + math.Abs(n[1]) + math.Abs(n[2])
	x, y := n[0]/w, n[1]/w
	if n[2] < 0 {
		x, y = (1-math.Abs(y))*sign(x), (1-math.Abs(x))*sign(y)
	}
	sx, sy := int16(math.Round(x*32767)), int16(math.Round(y*32767))
	packed := uint32(uint16(sx)) | uint32(uint16(sy))<<16
	x, y = float64(sx)/32767, float64(sy)/32767
	z := 1 - math.Abs(x) - math.Abs(y)
	t := max(-z, 0)
	d := [3]float64{x - t*sign(x), y - t*sign(y), z}
	return packed, scScale(d, 1/math.Sqrt(scDot(d, d)))
}

func (b *materialBuilder) addSCMod(block scModBlock, box image.Rectangle, atlas *image.NRGBA) (uint32, error) {
	m := r2.Model{Kind: 2, Offset: uint32(len(b.references) / 16)}
	mode := float32(0)
	for y := box.Min.Y; y < box.Max.Y; y++ {
		for x := box.Min.X; x < box.Max.X; x++ {
			a := atlas.NRGBAAt(x, y).A
			if a != 255 {
				mode = max(mode, 1)
			}
			if a != 0 && a != 255 {
				mode = 2
			}
		}
	}
	w, h := float32(atlas.Bounds().Dx()), float32(atlas.Bounds().Dy())
	for _, tr := range block.mesh {
		p := tr[0].p
		a, c := scSub(tr[1].p, p), scSub(tr[2].p, p)
		n := scCross(a, c)
		n2 := scDot(n, n)
		if n2 < 1e-12 {
			continue
		}
		u, v := scScale(scCross(c, n), 1/n2), scScale(scCross(n, a), 1/n2)
		normal := scScale(n, 1/math.Sqrt(n2))
		packed, decoded := scOctahedron(normal)
		p0, u0, v0 := tr[0].uv, tr[1].uv, tr[2].uv
		du, dv := [2]float64{u0[0] - p0[0], u0[1] - p0[1]}, [2]float64{v0[0] - p0[0], v0[1] - p0[1]}
		if math.Abs(du[0]*dv[1]-du[1]*dv[0]) < 1e-12 {
			// Keep the sampled texel and leave a non-singular tangent frame.
			du, dv = [2]float64{1e-5, 0}, [2]float64{0, 1e-5}
		}
		for _, i := range []uint32{packed, math.Float32bits(float32(scDot(decoded, p))), uint32(len(b.quads) / 112), 2} {
			b.references = binary.LittleEndian.AppendUint32(b.references, i)
		}
		b.quads = appendFloats(b.quads,
			float32(u[0]), float32(u[1]), float32(u[2]), float32(-scDot(u, p)),
			float32(v[0]), float32(v[1]), float32(v[2]), float32(-scDot(v, p)),
			float32(p0[0]), float32(p0[1]), float32(du[0]), float32(du[1]),
			float32(dv[0]), float32(dv[1]), mode, 0,
			float32(box.Min.X)/w, float32(box.Min.Y)/h, float32(box.Dx())/w, float32(box.Dy())/h,
			1, 1, 1, 1, float32(normal[0]), float32(normal[1]), float32(normal[2]), 0)
		m.Count++
	}
	if m.Count == 0 {
		return 0, fmt.Errorf("模型没有非退化三角面")
	}
	id := uint32(len(b.catalog.Models))
	b.catalog.Models = append(b.catalog.Models, m)
	return id, nil
}
