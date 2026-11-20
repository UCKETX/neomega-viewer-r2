package worldsource

import (
	"context"
	"encoding/binary"
	"os"
	"testing"

	"neomega-converter-core/worldinput"
	"neomega-r2link"
)

func TestSurvivalcraftSample(t *testing.T) {
	path := os.Getenv("R2_SURVIVALCRAFT_SAMPLE")
	if path == "" {
		t.Skip("set R2_SURVIVALCRAFT_SAMPLE to test a native .scworld")
	}
	ctx := context.Background()
	w, err := Open(ctx, Options{Options: worldinput.Options{Path: path}, SurvivalcraftTexture: os.Getenv("R2_SURVIVALCRAFT_TEXTURE")})
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	if w.Info.Kind != "scworld" || w.TextureName == "" || w.Textures == nil {
		t.Fatal(w.Info, w.TextureName)
	}
	states := w.Textures.Files["states.bin"]
	known := map[uint32]bool{}
	for i := 0; i < len(states); i += 8 {
		known[binary.LittleEndian.Uint32(states[i:])] = true
	}
	chunks, err := w.ScanChunks(ctx)
	if err != nil {
		t.Fatal(err)
	}
	regions := map[[2]int]bool{}
	for _, p := range chunks {
		regions[[2]int{int(p.X >> 3), int(p.Z >> 3)}] = true
	}
	sections := 0
	for r := range regions {
		region, err := w.Region(ctx, r[0], r[1])
		if err != nil {
			t.Fatal(r, err)
		}
		for _, s := range region.Sections {
			for _, hash := range s.Palette {
				if !known[hash] && !r2link.IsAir(hash) {
					t.Fatalf("unknown hash %08x", hash)
				}
			}
		}
		sections += len(region.Sections)
	}
	if sections == 0 {
		t.Fatal("no rendered chunks")
	}
	t.Logf("%s: %d chunks, %d R2Link regions, %d sections, %s", w.Info.Name, len(chunks), len(regions), sections, w.TextureName)
}
