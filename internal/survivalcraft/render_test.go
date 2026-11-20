package survivalcraft

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"testing"

	"neomega-blocks/ir"

	r2 "neomega-view-utils/r2assets"
)

func TestVanillaPlacedBlockCoverage(t *testing.T) {
	// IsPlaceable IDs from Survivalcraft 2.4's block table, plus saved
	// bedrock, fire and the two invisible world markers.
	ids := []uint32{
		1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20, 21, 24, 25, 26, 27, 28, 31,
		39, 41, 44, 45, 46, 47, 48, 49, 50, 51, 52, 53, 54, 55, 56, 57, 58, 59, 60, 61, 62, 63, 64, 65, 66, 67, 68, 69, 70, 71, 72, 73, 75, 76, 83, 84, 86, 87, 92, 94, 95, 96, 97, 98, 99,
		100, 101, 104, 105, 106, 107, 112, 119, 120, 121, 126, 127, 131, 132, 133, 134, 135, 136, 137, 138, 139, 140, 141, 142, 143, 144, 145, 146, 147, 148, 150, 151, 152, 153, 154, 155, 156, 157,
		163, 164, 166, 167, 168, 173, 174, 179, 180, 181, 182, 183, 184, 185, 186, 187, 188, 189, 190, 193, 194, 197, 199, 202, 204, 208, 209, 210, 211, 213, 216, 217, 223, 224, 225, 226, 227, 229, 231, 232, 233, 234, 235, 236, 237, 238, 243, 244, 253, 254, 255, 256, 257, 258, 259, 260, 261, 262, 263,
	}
	w := &World{palette: defaultPalette, furniture: map[uint32]furnitureDesign{
		0: {material: 71, resolution: 2, high: [3]int{1, 1, 1}},
	}}
	for _, id := range ids {
		st := w.BlockState(id)
		if st == Unknown || (st != state("air") && !w.Material(id).Known) {
			t.Errorf("missing vanilla terrain block %d: %s", id, st)
		}
	}
	if w.BlockState(1023) != Unknown || w.Material(1023).Known {
		t.Fatal("unmapped mod blocks must stay diagnostic")
	}
	variants := map[uint32][6]uint8{
		231:          {78, 78, 78, 78, 78, 78},
		154 | 1<<14:  {184, 184, 1, 1, 1, 1},
		154 | 2<<14:  {1, 1, 184, 184, 1, 1},
		216 | 4<<14:  {42, 42, 42, 59, 42, 42},
		45 | 1<<14:   {26, 27, 42, 42, 25, 25},
		227:          {179, 179, 179, 179, 179, 179},
		174 | 15<<14: {87, 87, 87, 87, 87, 87},
	}
	for word, faces := range variants {
		if got := w.Material(word).Faces; got != faces {
			t.Errorf("block %d data %d: faces %v, want %v", word&1023, word>>14, got, faces)
		}
	}
	shapes, err := r2.OpenStates()
	if err != nil {
		t.Fatal(err)
	}
	for _, word := range []uint32{56, 57 | 13<<14, 63, 86, 104, 119, 133 | 4<<14, 133 | 16<<14, 138, 139 | 4<<14, 141 | 7<<14, 144, 174 | 15<<14, 197, 209, 226, 232, 233, 237, 238, 261} {
		st := w.BlockState(word)
		if id := shapes[ir.IRBlock(st).Hash()]; id == 0 {
			t.Errorf("geometry not in R2 catalog for block %d: %s", word&1023, st)
		}
	}
}

func TestNativeMaterialTable(t *testing.T) {
	ctx := context.Background()
	const unpainted = uint32(72)
	const painted = uint32(72 | 29<<14)
	const stair = uint32(49 | (32+4*64)<<14)
	raw := make([]byte, 256)
	for _, value := range []uint32{unpainted, painted, stair, 231, 1023} {
		raw = encodeRun(raw, value, 1)
	}
	raw = encodeRun(raw, 0, cellCount-5)
	img := image.NewNRGBA(image.Rect(0, 0, 128, 128))
	for _, slot := range []int{8, 15, 23, 78, 189} {
		x, y := slot%16*8, slot/16*8
		for v := range 8 {
			for u := range 8 {
				img.SetNRGBA(x+u, y+v, color.NRGBA{uint8(slot), uint8(u), uint8(v), 255})
			}
		}
	}
	var atlasPNG bytes.Buffer
	if err := png.Encode(&atlasPNG, img); err != nil {
		t.Fatal(err)
	}
	path := fixture(t, map[string][]byte{
		"Project.xml": []byte(`<Project Version="2.4"><Subsystems><Values Name="GameInfo">
			<Value Name="BlockTextureName" Value="檐.scbtex"/><Values Name="Palette">
			<Value Name="Colors" Value=";;;;;;;;;;;;;;40,80,120;"/>
			</Values></Values></Subsystems></Project>`),
		"EmbeddedContent/檐.scbtex": atlasPNG.Bytes(),
		"Regions/Region 0,0.dat":   region(0, deflate(t, raw)),
	})
	world, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	atlas, name, err := world.Atlas(ctx, "")
	if err != nil || name != "地图内置生存战争材质" {
		t.Fatal(name, err)
	}
	m := world.Material(painted)
	if m.Faces[0] != 15 || m.Tint[0] != ([3]uint8{40, 80, 120}) {
		t.Fatal(m)
	}
	set, hashes, err := world.RenderTextures(ctx, atlas)
	if err != nil {
		t.Fatal(err)
	}
	if hashes[painted] == hashes[unpainted] || hashes[painted] == 0 {
		t.Fatal(hashes)
	}
	states := set.Files["states.bin"]
	ids := map[uint32]uint32{}
	for i := 0; i < len(states); i += 8 {
		ids[binary.LittleEndian.Uint32(states[i:])] = binary.LittleEndian.Uint32(states[i+4:])
	}
	var cat r2.Catalog
	if err := json.Unmarshal(set.Files["catalog.json"], &cat); err != nil {
		t.Fatal(err)
	}

	if m := cat.Models[ids[hashes[stair]]]; m.Kind != 2 || m.Count == 0 {
		t.Fatal(m)
	}
	if !bytes.Equal(set.Files["atlas-color.bin"], img.Pix) {
		t.Fatal("quad texture is not the embedded native atlas")
	}
	if set.Info.Side != 8 || set.Info.Levels != 4 {
		t.Fatal(set.Info)
	}
	cube := cat.Models[ids[hashes[unpainted]]]
	fast := set.Files["fast.bin"]
	get := func(i int) float32 {
		return math.Float32frombits(binary.LittleEndian.Uint32(fast[int(cube.Offset)*16+i*4:]))
	}
	layer := int(get(6))
	start := layer * 8 * 8 * 4
	x, y := 8%16*8, 8/16*8
	src := img.NRGBAAt(x+2, y+3)
	got := set.Files["surface-layers.bin"][start+(3*8+2)*4:]
	if !bytes.Equal(got[:4], []byte{src.R, src.G, src.B, src.A}) {
		t.Fatal("cube sampled wrong atlas slot", got[:4], src)
	}
	if cat.Models[ids[hashes[1023]]].Kind != 1 {
		t.Fatal("diagnostic fallback was not retained")
	}
	semiconductor := cat.Models[ids[hashes[231]]]
	off := int(semiconductor.Offset)*16 + 24
	slotLayer := int(math.Float32frombits(binary.LittleEndian.Uint32(fast[off:])))
	pixel := set.Files["surface-layers.bin"][slotLayer*8*8*4:]
	if semiconductor.Kind != 1 || pixel[0] != 78 || pixel[3] != 255 {
		t.Fatalf("semiconductor did not use native tile 78: %v %v", semiconductor, pixel[:4])
	}

	override := filepath.Join(t.TempDir(), "override.png")
	if err := os.WriteFile(override, atlasPNG.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	if _, name, err := world.Atlas(ctx, override); name != "override.png" || err != nil {
		t.Fatal(name, err)
	}
	if _, err := decodeAtlas([]byte("not a PNG")); err == nil {
		t.Fatal("expected PNG validation")
	}
}
