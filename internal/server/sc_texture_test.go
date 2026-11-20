package server

import (
	"archive/zip"
	"bytes"
	"compress/flate"
	"context"
	"encoding/binary"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"

	"neomega-viewer-r2/internal/worldsource"
)

func scAtlasFixture(t *testing.T, n int, pixel color.NRGBA) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, n, n))
	img.SetNRGBA(0, 0, pixel)
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func scWorldFixture(t *testing.T, atlas []byte) string {
	t.Helper()
	const headerLen = 2052
	raw := make([]byte, 256)
	raw = binary.LittleEndian.AppendUint32(raw, 231)
	for cells := 65535; cells > 0; {
		n := min(271, cells)
		raw = binary.LittleEndian.AppendUint32(raw, uint32(min(16, n)-1)<<10)
		if n >= 16 {
			raw = append(raw, byte(n-16))
		}
		cells -= n
	}
	var packed bytes.Buffer
	z, err := flate.NewWriter(&packed, flate.BestSpeed)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := z.Write(raw); err != nil {
		t.Fatal(err)
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	header := make([]byte, headerLen)
	copy(header, "RGN1")
	binary.LittleEndian.PutUint32(header[4:8], headerLen)
	binary.LittleEndian.PutUint32(header[8:12], uint32(packed.Len()))
	dir := t.TempDir()
	f := filepath.Join(dir, "世界.scworld")
	var b bytes.Buffer
	zipper := zip.NewWriter(&b)
	for name, data := range map[string][]byte{
		"Project.xml":                 []byte(`<Project Version="2.4"><Subsystems><Values Name="GameInfo"><Value Name="BlockTextureName" Value="Test"/></Values></Subsystems></Project>`),
		"EmbeddedContent/Test.scbtex": atlas,
		"Regions/Region 0,0.dat":      append(append(header, "CHK1"...), packed.Bytes()...),
	} {
		entry, err := zipper.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err := zipper.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f, b.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	return f
}

func TestSurvivalcraftTextureApplyReset(t *testing.T) {
	original := color.NRGBA{220, 140, 15, 255}
	override := color.NRGBA{10, 140, 220, 255}
	file := scWorldFixture(t, scAtlasFixture(t, 128, original))
	atlas := filepath.Join(t.TempDir(), "覆盖.SCBTEX")
	if err := os.WriteFile(atlas, scAtlasFixture(t, 256, override), 0600); err != nil {
		t.Fatal(err)
	}
	service := New(nil, nil)
	defer service.Close()
	handler := service.Handler(fstest.MapFS{})
	request := func(id uint64, path string) *httptest.ResponseRecorder {
		t.Helper()
		body, _ := json.Marshal(map[string]any{"world": id, "path": path})
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest("POST", "/api/world/sc-texture", bytes.NewReader(body)))
		return w
	}
	if res := request(service.Info().ID, atlas); res.Code != 400 {
		t.Fatal("non-SC world accepted an SC atlas", res.Code)
	}
	before, err := service.Open(context.Background(), OpenOptions{Path: file})
	if err != nil {
		t.Fatal(err)
	}
	// The loader retains a read-only terrain snapshot; neither applying nor
	// resetting the atlas should reopen the map or load mod files again.
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	if res := request(before.ID, atlas); res.Code != 200 {
		t.Fatal(res.Code, res.Body.String())
	}
	custom := service.Info()
	customAtlas := service.current.source.(*worldsource.World).Textures
	if custom.Path != before.Path || custom.Position != before.Position || custom.ID == before.ID ||
		custom.SCTexturePath != atlas || !custom.SCTextureUpdate || customAtlas.Info.AtlasWidth != 256 ||
		!bytes.Equal(customAtlas.Files["atlas-color.bin"][:4], []byte{override.R, override.G, override.B, override.A}) {
		t.Fatal(custom, customAtlas.Info)
	}
	if res := request(before.ID, ""); res.Code != 409 {
		t.Fatal("stale world request should be rejected", res.Code)
	}
	if res := request(custom.ID, "minecraft.zip"); res.Code != 400 || service.Info().ID != custom.ID {
		t.Fatal("invalid format changed the current session", res.Code)
	}
	if res := request(custom.ID, ""); res.Code != 200 {
		t.Fatal(res.Code, res.Body.String())
	}
	reset := service.Info()
	resetAtlas := service.current.source.(*worldsource.World).Textures
	if reset.ID == custom.ID || reset.SCTexturePath != "" || reset.TextureName != "地图内置生存战争材质" ||
		resetAtlas.Info.AtlasWidth != 128 || !bytes.Equal(resetAtlas.Files["atlas-color.bin"][:4], []byte{original.R, original.G, original.B, original.A}) {
		t.Fatal(reset, resetAtlas.Info)
	}
}
