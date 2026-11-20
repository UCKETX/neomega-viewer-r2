package survivalcraft

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestJSONProjectWorld(t *testing.T) {
	raw := encodeRun(make([]byte, 256), 231, 1)
	raw = encodeRun(raw, 0, cellCount-1)
	path := fixture(t, map[string][]byte{
		"Project.json": []byte(`{
			"Version":["string","2.3"],
			"Subsystems":{
				"GameInfo":{"WorldName":["string","瓦伦格"],"BlockTextureName":["string","瓦.scbtex"],
					"Palette":{"Colors":["string",";;;;;;;;;;;;;;147,154,200;"]}},
				"Players":{"Players":{"0":{"SpawnPosition":["Vector3","4,15,5"]}}},
				"BlocksManager":{"300":["string","RedGlassBlock"]},
				"FurnitureBlockBehavior":{"FurnitureDesigns":{"0":{"Resolution":["int",2],"Values":["string","8*231,"]}}}
			},
			"Entities":{
				"1":{"Name":["string","Chest"],"Overrides":{"Body":{"Position":["Vector3","1,12,1"]}}},
				"2":{"Name":["string","MalePlayer"],"Overrides":{"Body":{"Position":["Vector3","2.5,21,3.5"]}}}
			}
		}`),
		"Project.temp":             []byte("stale temp"),
		"Project.bak":              []byte("stale backup"),
		"EmbeddedContent/瓦.scbtex": []byte("native atlas bytes"),
		"Regions/Region 0,0.dat":   region(0, deflate(t, raw)),
	})
	ctx := context.Background()
	w, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if w.Name != "瓦伦格" || w.Position != ([3]float64{2.5, 33, 3.5}) {
		t.Fatalf("unexpected metadata: %v %v", w.Name, w.Position)
	}
	if !bytes.Equal(w.EmbeddedAtlas, []byte("native atlas bytes")) {
		t.Fatal("did not select embedded atlas")
	}
	if m := w.Material(300); w.BlockState(300) != state("red_stained_glass") || m.Tint[0] != ([3]uint8{147, 154, 200}) {
		t.Fatalf("did not read mod block and palette: %v", m)
	}
	if m := w.Material(227); !m.Known || m.Faces[0] != 78 {
		t.Fatalf("did not read furniture: %v", m)
	}
	cells, err := w.ReadChunk(ctx, Pos{})
	if err != nil || len(cells) != cellCount || cells[0] != 231 {
		t.Fatalf("RGN1 terrain not loaded: %d cells, %v", len(cells), err)
	}
}

func TestProjectMetadataSelection(t *testing.T) {
	goodTerrain := region(0, deflate(t, encodeRun(make([]byte, 256), 0, cellCount)))
	ctx := context.Background()
	w, err := Open(ctx, fixture(t, map[string][]byte{
		"Project.xml":            []byte(`<Project Version="2.4"><Subsystems><Values Name="GameInfo"><Value Name="WorldName" Value="XML 首选"/></Values></Subsystems></Project>`),
		"Project.json":           []byte("{"),
		"Regions/Region 0,0.dat": goodTerrain,
	}))
	if err != nil || w.Name != "XML 首选" {
		t.Fatalf("XML should have precedence: %v %v", w, err)
	}
	for _, tc := range []struct{ data, want string }{
		{"{", "Project.json:"},
		{`{"Version":["string","2.3"]}`, "Version 或 Subsystems"},
		{`{"Version":["string","2.3"],"Subsystems":{"GameInfo":[]}}`, "GameInfo:"},
	} {
		_, err := Open(ctx, fixture(t, map[string][]byte{
			"Project.json":           []byte(tc.data),
			"Project.bak":            []byte(`{"Version":["string","2.3"],"Subsystems":{}}`),
			"Regions/Region 0,0.dat": goodTerrain,
		}))
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("want explicit metadata failure %q, got %v", tc.want, err)
		}
	}
}
