package survivalcraft

import (
	"bufio"
	"bytes"
	"encoding/xml"
	"fmt"
	"math"
	"strconv"
	"strings"
)

const maxSCModTriangles = 256

type scVertex struct {
	p  [3]float64
	uv [2]float64
}
type scTriangle [3]scVertex

func scUnitCube() []scTriangle {
	var out []scTriangle
	for axis := range 3 {
		for side := range 2 {
			var face [4]scVertex
			for i, uv := range [4][2]float64{{0, 0}, {1, 0}, {1, 1}, {0, 1}} {
				p := [3]float64{}
				p[axis] = float64(side)
				b, c := (axis+1)%3, (axis+2)%3
				if axis != 1 {
					if b == 1 {
						b, c = c, b
					}
					p[b], p[c] = uv[0], 1-uv[1]
				} else {
					p[b], p[c] = uv[0], uv[1]
				}
				face[i] = scVertex{p, uv}
			}
			out = append(out, scTriangle{face[0], face[1], face[2]}, scTriangle{face[0], face[2], face[3]})
		}
	}
	return out
}

func scFloats(fields []string, limit int) ([]float64, error) {
	if len(fields) > limit {
		return nil, fmt.Errorf("模型坐标过多")
	}
	out := make([]float64, len(fields))
	for i, s := range fields {
		v, e := strconv.ParseFloat(s, 64)
		if e != nil || math.IsNaN(v) || math.Abs(v) > 1e6 {
			return nil, fmt.Errorf("无效模型坐标")
		}
		out[i] = v
	}
	return out, nil
}

func readSCObj(raw []byte) ([]scTriangle, error) {
	var pos [][3]float64
	var uv [][2]float64
	var out []scTriangle
	s := bufio.NewScanner(bytes.NewReader(trimBOM(raw)))
	for n := 1; s.Scan(); n++ {
		before, _, _ := strings.Cut(s.Text(), "#")
		f := strings.Fields(before)
		if len(f) == 0 {
			continue
		}
		switch f[0] {
		case "v", "vt":
			need := 3
			if f[0] == "vt" {
				need = 2
			}
			if len(f) < need+1 || len(pos) >= 4096 || len(uv) >= 4096 {
				return nil, fmt.Errorf("无效 OBJ 顶点 (行 %d)", n)
			}
			v, e := scFloats(f[1:need+1], need)
			if e != nil {
				return nil, e
			}
			if need == 3 {
				pos = append(pos, [3]float64{v[0], v[1], v[2]})
			} else {
				uv = append(uv, [2]float64{v[0], 1 - v[1]})
			}
		case "f":
			if len(f) < 4 || len(f) > 65 || len(out)+len(f)-3 > maxSCModTriangles {
				return nil, fmt.Errorf("OBJ 面无效或超过 %d 个三角面", maxSCModTriangles)
			}
			var face []scVertex
			for _, s := range f[1:] {
				parts := strings.Split(s, "/")
				idx := func(s string, ln int) (int, bool) {
					i, e := strconv.Atoi(s)
					if i < 0 {
						i = ln + i
					} else {
						i--
					}
					return i, e == nil && i >= 0 && i < ln
				}
				a, ok := idx(parts[0], len(pos))
				if !ok || len(parts) < 2 {
					return nil, fmt.Errorf("OBJ 面必须有有效顶点和 UV (行 %d)", n)
				}
				b, ok := idx(parts[1], len(uv))
				if !ok {
					return nil, fmt.Errorf("OBJ UV 无效 (行 %d)", n)
				}
				face = append(face, scVertex{pos[a], uv[b]})
			}
			for i := 1; i+1 < len(face); i++ {
				out = append(out, scTriangle{face[0], face[i], face[i+1]})
			}
		}
	}
	return out, s.Err()
}

type scDaeInput struct {
	Semantic string `xml:"semantic,attr"`
	Source   string `xml:"source,attr"`
	Offset   int    `xml:"offset,attr"`
	Set      int    `xml:"set,attr"`
}
type scDaePrimitive struct {
	Inputs  []scDaeInput `xml:"input"`
	Indices string       `xml:"p"`
	Sizes   string       `xml:"vcount"`
}
type scDaeMesh struct {
	Sources []struct {
		ID       string `xml:"id,attr"`
		Array    string `xml:"float_array"`
		Accessor struct {
			Stride int `xml:"stride,attr"`
			Offset int `xml:"offset,attr"`
		} `xml:"technique_common>accessor"`
	} `xml:"source"`
	Vertices []struct {
		ID     string       `xml:"id,attr"`
		Inputs []scDaeInput `xml:"input"`
	} `xml:"vertices"`
	Triangles []scDaePrimitive `xml:"triangles"`
	Polylist  []scDaePrimitive `xml:"polylist"`
}

func readSCDae(raw []byte) ([]scTriangle, error) {
	var doc struct {
		XMLName xml.Name    `xml:"COLLADA"`
		Up      string      `xml:"asset>up_axis"`
		Meshes  []scDaeMesh `xml:"library_geometries>geometry>mesh"`
	}
	if e := xml.Unmarshal(trimBOM(raw), &doc); e != nil {
		return nil, e
	}
	var out []scTriangle
	for _, m := range doc.Meshes {
		type data struct {
			v           []float64
			stride, off int
		}
		src := map[string]data{}
		for _, s := range m.Sources {
			stride := s.Accessor.Stride
			if stride <= 0 || stride > 16 || s.Accessor.Offset < 0 {
				return nil, fmt.Errorf("DAE 的数据步长无效")
			}
			v, e := scFloats(strings.Fields(s.Array), 65536)
			if e != nil {
				return nil, e
			}
			src["#"+s.ID] = data{v, stride, s.Accessor.Offset}
		}
		pmap := map[string]string{}
		for _, v := range m.Vertices {
			for _, i := range v.Inputs {
				if i.Semantic == "POSITION" {
					pmap["#"+v.ID] = i.Source
				}
			}
		}
		for _, g := range append(m.Triangles, m.Polylist...) {
			var a, b scDaeInput
			stride := 0
			for _, i := range g.Inputs {
				if i.Offset < 0 || i.Offset > 15 {
					return nil, fmt.Errorf("DAE 输入偏移无效")
				}
				stride = max(stride, i.Offset+1)
				switch i.Semantic {
				case "VERTEX":
					a = i
					a.Source = pmap[i.Source]
				case "POSITION":
					a = i
				case "TEXCOORD":
					if i.Set == 0 {
						b = i
					}
				}
			}
			aData, bData := src[a.Source], src[b.Source]
			ix := strings.Fields(g.Indices)
			if len(ix) == 0 || stride == 0 || len(ix)%stride != 0 || aData.stride < 3 || bData.stride < 2 || len(ix)/stride > 3*maxSCModTriangles {
				return nil, fmt.Errorf("DAE 需要有效的 POSITION 和 TEXCOORD 基元")
			}
			fetch := func(s data, elem, offset, n int) ([]float64, error) {
				index, e := strconv.Atoi(ix[elem*stride+offset])
				if e != nil || index < 0 || index >= len(s.v) || s.off > len(s.v) {
					return nil, fmt.Errorf("DAE 顶点索引无效")
				}
				j := index*s.stride + s.off
				if j+n > len(s.v) {
					return nil, fmt.Errorf("DAE 顶点索引越界")
				}
				return s.v[j : j+n], nil
			}
			var verts []scVertex
			for i := 0; i < len(ix)/stride; i++ {
				p, e := fetch(aData, i, a.Offset, 3)
				if e != nil {
					return nil, e
				}
				u, e := fetch(bData, i, b.Offset, 2)
				if e != nil {
					return nil, e
				}
				v := [3]float64{p[0], p[1], p[2]}
				switch strings.TrimSpace(doc.Up) {
				case "Z_UP":
					v = [3]float64{p[0], p[2], -p[1]}
				case "X_UP":
					v = [3]float64{-p[1], p[0], p[2]}
				}
				verts = append(verts, scVertex{v, [2]float64{u[0], 1 - u[1]}})
			}
			sizes := strings.Fields(g.Sizes)
			if len(sizes) == 0 {
				if len(verts)%3 != 0 {
					return nil, fmt.Errorf("DAE 三角面顶点数量不符")
				}
				sizes = make([]string, len(verts)/3)
				for i := range sizes {
					sizes[i] = "3"
				}
			}
			j := 0
			for _, s := range sizes {
				n, e := strconv.Atoi(s)
				if e != nil || n < 3 || n > 64 || n > len(verts)-j || len(out)+n-2 > maxSCModTriangles {
					return nil, fmt.Errorf("DAE 面无效或超过 %d 个三角面", maxSCModTriangles)
				}
				for i := 1; i+1 < n; i++ {
					out = append(out, scTriangle{verts[j], verts[j+i], verts[j+i+1]})
				}
				j += n
			}
			if j != len(verts) {
				return nil, fmt.Errorf("DAE 基元长度不符")
			}
		}
	}
	return out, nil
}

func fitSCModel(tri []scTriangle, scale float64, offset [3]float64) error {
	if len(tri) == 0 {
		return fmt.Errorf("模型没有可用的静态三角面")
	}
	if scale < 0 || scale > 1e6 {
		return fmt.Errorf("模型 scale 无效")
	}
	if scale == 0 {
		lo, hi := [3]float64{math.Inf(1), math.Inf(1), math.Inf(1)}, [3]float64{math.Inf(-1), math.Inf(-1), math.Inf(-1)}
		for _, t := range tri {
			for _, v := range t {
				for a := range 3 {
					lo[a] = min(lo[a], v.p[a])
					hi[a] = max(hi[a], v.p[a])
				}
			}
		}
		if lo[0] >= 0 && lo[1] >= 0 && lo[2] >= 0 && hi[0] <= 1 && hi[1] <= 1 && hi[2] <= 1 {
			scale = 1
		} else {
			size := max(hi[0]-lo[0], hi[1]-lo[1], hi[2]-lo[2])
			if size < 1e-8 {
				return fmt.Errorf("模型尺寸无效")
			}
			scale = 1 / size
			offset[0] += .5 - (hi[0]+lo[0])*scale/2
			offset[1] -= lo[1] * scale
			offset[2] += .5 - (hi[2]+lo[2])*scale/2
		}
	}
	for i := range tri {
		for j := range tri[i] {
			for a := range 3 {
				x := tri[i][j].p[a]*scale + offset[a]
				if x < -1e-6 || x > 1+1e-6 {
					return fmt.Errorf("静态模型超出单格范围")
				}
				tri[i][j].p[a] = max(0, min(1, x))
			}
		}
	}
	return nil
}
