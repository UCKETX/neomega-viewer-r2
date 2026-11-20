package survivalcraft

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"image"
	"image/draw"
	"maps"
	"math"
	"slices"
	"strconv"

	"neomega-blocks/ir"
	"neomega-r2link"

	r2 "neomega-view-utils/r2assets"
)

// RenderTextures is a complete R2 snapshot based on Survivalcraft's atlas.
// Java tables contribute vertex/plane geometry only; none of their texels,
// tints, PBR channels or material layer indices enter this snapshot.
func (w *World) RenderTextures(ctx context.Context, atlas image.Image) (*r2.TextureSet, map[uint32]uint32, error) {
	words, err := w.BlockValues(ctx)
	if err != nil {
		return nil, nil, err
	}
	shapes, err := r2.OpenStates()
	if err != nil {
		return nil, nil, err
	}
	base, err := r2.OpenCatalog()
	if err != nil {
		return nil, nil, err
	}
	refs, err := r2.Files.ReadFile("assets/quad-references.bin")
	if err != nil {
		return nil, nil, err
	}
	quads, err := r2.Files.ReadFile("assets/base-quad-records.bin")
	if err != nil {
		return nil, nil, err
	}

	rgba := image.NewNRGBA(image.Rect(0, 0, atlas.Bounds().Dx(), atlas.Bounds().Dy()))
	draw.Draw(rgba, rgba.Bounds(), atlas, atlas.Bounds().Min, draw.Src)
	tiles := makeTiles(rgba)
	packed, err := packSCTextures(rgba, w.staticMods)
	if err != nil {
		return nil, nil, err
	}
	b := materialBuilder{
		base: base, baseRefs: refs, baseQuads: quads, tiles: tiles,
		catalog:     r2.Catalog{Models: []r2.Model{{}}, Statistics: map[string]int{}},
		models:      map[modelKey]uint32{},
		baseWidth:   rgba.Bounds().Dx(),
		atlasWidth:  packed.atlas.Bounds().Dx(),
		atlasHeight: packed.atlas.Bounds().Dy(),
	}
	b.catalog.Water = b.addModel(0, w.Material(18))
	hashes := make(map[uint32]uint32, len(words))
	states := map[uint32]uint32{r2link.AirHash: 0}
	modModels := map[uint32]uint32{}
	for _, word := range words {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		content := word & 1023
		var id uint32
		if mod, found := w.staticMods[content]; found {
			if id = modModels[content]; id == 0 {
				id, err = b.addSCMod(mod, packed.boxes[content], packed.atlas)
				if err != nil {
					return nil, nil, fmt.Errorf("模组方块 %d: %w", content, err)
				}
				modModels[content] = id
			}
		} else {
			state := w.BlockState(word)
			if state == "minecraft:air []" {
				continue
			}
			m := w.Material(word)
			shape := shapes[ir.IRBlock(state).Hash()]
			id = b.addModel(shape, m)
		}
		// Send ordinary R2Link hashes, but allocate in an SC namespace. An
		// uncommon collision must not cause one cell to use another's model.
		key := "survivalcraft:cell_" + strconv.FormatUint(uint64(word), 16)
		for {
			hash := ir.IRBlock(key + " []").Hash()
			if _, exists := states[hash]; !exists && !r2link.IsAir(hash) {
				hashes[word], states[hash] = hash, id
				break
			}
			key += "_"
		}
		if len(b.catalog.Models) > 65536 || len(b.references) > 16<<20 || len(b.quads) > 32<<20 {
			return nil, nil, fmt.Errorf("生存战争模型超过渲染预算")
		}
	}
	set, err := b.snapshot(packed.atlas)
	if err != nil {
		return nil, nil, err
	}
	for _, hash := range slices.Sorted(maps.Keys(states)) {
		set.Files["states.bin"] = binary.LittleEndian.AppendUint32(set.Files["states.bin"], hash)
		set.Files["states.bin"] = binary.LittleEndian.AppendUint32(set.Files["states.bin"], states[hash])
	}
	set.Files["textures-info.json"], err = json.Marshal(set.Info)
	return set, hashes, err
}

type tile struct {
	layer int
	color []byte
	holes bool
	mean  [3]float64
	alpha float32
}

func makeTiles(atlas *image.NRGBA) [256]tile {
	side := atlas.Rect.Dx() / 16
	var tiles [256]tile
	for i := range tiles {
		x, y := i%16*side, i/16*side
		t := tile{layer: -1, color: make([]byte, side*side*4)}
		var alpha float64
		for v := range side {
			row := atlas.Pix[(y+v)*atlas.Stride+x*4:]
			for u := range side {
				in := row[u*4 : u*4+4]
				out := t.color[(v*side+u)*4:]
				a := float64(in[3]) / 255
				t.holes = t.holes || a < .996
				alpha += a
				for c := range 3 {
					l := linear[in[c]] * a
					t.mean[c] += l
					out[c] = srgb(l)
				}
				out[3] = in[3]
			}
		}
		if alpha > 0 {
			for c := range 3 {
				t.mean[c] /= alpha
			}
		}
		t.alpha = float32(alpha / float64(side*side))
		tiles[i] = t
	}
	return tiles
}

var linear = func() (table [256]float64) {
	for i := range table {
		v := float64(i) / 255
		if v <= .04045 {
			table[i] = v / 12.92
		} else {
			table[i] = math.Pow((v+.055)/1.055, 2.4)
		}
	}
	return
}()

func srgb(v float64) uint8 {
	if v <= .0031308 {
		return uint8(math.Round(255 * v * 12.92))
	}
	return uint8(math.Round(255 * (1.055*math.Pow(min(1, v), 1/2.4) - .055)))
}

type modelKey struct {
	shape uint32
	m     Material
}

type materialBuilder struct {
	baseWidth, atlasWidth, atlasHeight int
	base                               *r2.Catalog
	baseRefs, baseQuads                []byte
	catalog                            r2.Catalog
	models                             map[modelKey]uint32
	tiles                              [256]tile
	layers                             []uint8
	fast, quads, references            []byte
}

func (b *materialBuilder) addModel(shape uint32, m Material) uint32 {
	var geometry r2.Model
	if m.Known && shape != 0 && int(shape) < len(b.base.Models) {
		geometry = b.base.Models[shape]
	}
	if geometry.Kind != 2 {
		shape = 0
	}
	key := modelKey{shape, m}
	if id, ok := b.models[key]; ok {
		return id
	}
	id := uint32(len(b.catalog.Models))
	b.models[key] = id
	b.catalog.Models = append(b.catalog.Models, r2.Model{})
	var result r2.Model
	if shape == 0 {
		result = b.cube(m)
	} else {
		result = b.mesh(geometry, m)
		for _, part := range geometry.Fragments {
			result.Fragments = append(result.Fragments, r2.Fragment{Offset: part.Offset, Model: b.addModel(part.Model, m)})
		}
	}
	b.catalog.Models[id] = result
	return id
}

func appendFloats(dst []byte, fs ...float32) []byte {
	for _, f := range fs {
		dst = binary.LittleEndian.AppendUint32(dst, math.Float32bits(f))
	}
	return dst
}

func (b *materialBuilder) cube(m Material) r2.Model {
	out := r2.Model{Kind: 1, Offset: uint32(len(b.fast) / 16), Count: 6, Group: m.Group}
	for i, slot := range m.Faces {
		if !m.Known {
			// Renderer diagnostics use an explicit, non-textured solid.
			b.fast = appendFloats(b.fast, 0, 0, 1, 0, 0, 1, -1, 0, 1, 1, 1, 1, 1, 0, 1, 1)
			continue
		}
		t := &b.tiles[slot]
		if t.layer < 0 {
			t.layer = len(b.layers)
			b.layers = append(b.layers, slot)
		}
		mode := float32(0)
		if m.Opacity < 255 {
			mode = 1
		} else if t.holes {
			out.Kind = 3
		}
		var tint, avg [3]float32
		for j := range 3 {
			l := linear[m.Tint[i][j]]
			tint[j] = float32(l)
			avg[j] = float32(srgb(t.mean[j]*l)) / 255
		}
		opacity := float32(m.Opacity) / 255
		// Across for X is Y,Z, for Y is Z,X, and for Z is X,Y.
		// Vertical surfaces use texture V=1-Y so the native art is upright.
		uv := [6]float32{0, 1, 0, -1, 1, 0}
		switch i / 2 {
		case 1:
			uv = [6]float32{0, 0, 0, 1, 1, 0}
		case 2:
			uv = [6]float32{0, 1, 1, 0, 0, -1}
		}
		b.fast = appendFloats(b.fast, uv[:]...)
		b.fast = appendFloats(b.fast, float32(t.layer), mode, tint[0], tint[1], tint[2], opacity, avg[0], avg[1], avg[2], t.alpha*opacity)
	}
	return out
}

func (b *materialBuilder) mesh(g r2.Model, m Material) r2.Model {
	out := r2.Model{Kind: 2, Offset: uint32(len(b.references) / 16), Count: g.Count}
	for i := range g.Count {
		src := (g.Offset + i) * 16
		ref := b.baseRefs[src : src+16]
		row := binary.LittleEndian.Uint32(ref[8:12]) * 112
		record := b.baseQuads[row : row+112]
		n := func(i int) float64 {
			return math.Abs(float64(math.Float32frombits(binary.LittleEndian.Uint32(record[96+i*4:]))))
		}
		// Axis-aligned geometry uses the same SC face mapping as full cubes.
		axis := 0
		if n(1) > n(axis) {
			axis = 1
		}
		if n(2) > n(axis) {
			axis = 2
		}
		face := axis * 2
		if math.Float32frombits(binary.LittleEndian.Uint32(record[96+axis*4:])) > 0 {
			face++
		}
		slot := m.Faces[face]
		mode := float32(0)
		if m.Opacity < 255 {
			mode = 2
		} else if b.tiles[slot].holes {
			mode = 1
		}
		b.references = append(b.references, ref[:8]...)
		b.references = binary.LittleEndian.AppendUint32(b.references, uint32(len(b.quads)/112))
		b.references = append(b.references, ref[12:16]...)
		b.quads = append(b.quads, record[:56]...)
		b.quads = appendFloats(b.quads, mode, 0,
			float32(int(slot)%16*b.baseWidth/16)/float32(b.atlasWidth), float32(int(slot)/16*b.baseWidth/16)/float32(b.atlasHeight),
			float32(b.baseWidth/16)/float32(b.atlasWidth), float32(b.baseWidth/16)/float32(b.atlasHeight),
			float32(linear[m.Tint[face][0]]), float32(linear[m.Tint[face][1]]), float32(linear[m.Tint[face][2]]), float32(m.Opacity)/255)
		b.quads = append(b.quads, record[96:112]...)
	}
	return out
}

func (b *materialBuilder) snapshot(atlas *image.NRGBA) (*r2.TextureSet, error) {
	side := b.baseWidth / 16
	layers := make([][]byte, len(b.layers))
	emission := map[uint8]uint8{212: 190, 215: 190, 141: 190}
	for i, slot := range b.layers {
		layers[i] = b.tiles[slot].color
	}
	levels := 0
	var colors, normals, specular []byte
	for size := side; size >= 1; size /= 2 {
		levels++
		for i, pixels := range layers {
			colors = append(colors, pixels...)
			for range size * size {
				normals = append(normals, 128, 128, 255, 255)
				specular = append(specular, 0, 0, 0, emission[b.layers[i]])
			}
			if size > 1 {
				layers[i] = downsample(pixels, size)
			}
		}
	}
	atlasNormals, atlasSpecular := make([]byte, len(atlas.Pix)), make([]byte, len(atlas.Pix))
	for y := range atlas.Rect.Dy() {
		for x := range atlas.Rect.Dx() {
			i := (y*atlas.Rect.Dx() + x) * 4
			atlasNormals[i], atlasNormals[i+1], atlasNormals[i+2], atlasNormals[i+3] = 128, 128, 255, 255
			if x < b.baseWidth && y < b.baseWidth {
				atlasSpecular[i+3] = emission[uint8(y/side*16+x/side)]
			}
		}
	}
	var headers []byte
	for _, m := range b.catalog.Models {
		for _, v := range [4]uint32{m.Kind, m.Offset, m.Count, m.Group} {
			headers = binary.LittleEndian.AppendUint32(headers, v)
		}
	}
	b.catalog.Statistics["surfaceLayers"] = len(b.layers)
	data, err := json.Marshal(b.catalog)
	if err != nil {
		return nil, err
	}
	set := &r2.TextureSet{Files: map[string][]byte{
		"catalog.json": data, "headers.bin": headers, "fast.bin": b.fast,
		"quad-records.bin": b.quads, "quad-references.bin": b.references,
		"atlas-color.bin": atlas.Pix, "atlas-normals.bin": atlasNormals, "atlas-specular.bin": atlasSpecular,
		"surface-layers.bin": colors, "surface-normals.bin": normals, "surface-specular.bin": specular,
	}}
	set.Info.Side, set.Info.Levels, set.Info.Layers = side, levels, len(b.layers)
	set.Info.AtlasWidth, set.Info.AtlasHeight = atlas.Rect.Dx(), atlas.Rect.Dy()
	return set, nil
}

func downsample(src []byte, size int) []byte {
	half := size / 2
	out := make([]byte, half*half*4)
	for y := range half {
		for x := range half {
			p := ((y*2)*size + x*2) * 4
			q := (y*half + x) * 4
			for c := range 3 {
				out[q+c] = srgb((linear[src[p+c]] + linear[src[p+4+c]] + linear[src[p+size*4+c]] + linear[src[p+size*4+4+c]]) / 4)
			}
			c := p + 3
			out[q+3] = uint8((int(src[c]) + int(src[c+4]) + int(src[c+size*4]) + int(src[c+size*4+4]) + 2) / 4)
		}
	}
	return out
}
