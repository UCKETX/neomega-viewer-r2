package survivalcraft

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	r2 "neomega-view-utils/r2assets"
)

const testSCObj = `v 0.05 0 0.15
v 0.95 0 0.15
v 0.5 0.9 0.15
vt 0 0
vt 1 0
vt 0.5 1
f -3/-3 -2/-2 -1/-1
`

const testSCDae = `<COLLADA><asset><up_axis>Y_UP</up_axis></asset><library_geometries><geometry id="test"><mesh>
<source id="pos"><float_array>0.12 0.1 0.4 0.92 0.1 0.4 0.52 0.88 0.4</float_array><technique_common><accessor stride="3"/></technique_common></source>
<source id="uv"><float_array>0 0 1 0 0.5 1</float_array><technique_common><accessor stride="2"/></technique_common></source>
<vertices id="v"><input semantic="POSITION" source="#pos"/></vertices>
<triangles count="1"><input semantic="VERTEX" source="#v" offset="0"/><input semantic="TEXCOORD" source="#uv" offset="1"/><p>0 0 1 1 2 2</p></triangles>
</mesh></geometry></library_geometries></COLLADA>`

func scModFixture(t *testing.T, entries map[string][]byte) string {
	t.Helper()
	from := fixture(t, entries)
	p := filepath.Join(t.TempDir(), "example.scmod")
	if err := os.Rename(from, p); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestStaticSCModImport(t *testing.T) {
	ctx := context.Background()
	raw := encodeRun(make([]byte, 256), 901, 1)
	raw = encodeRun(raw, 902, 1)
	raw = encodeRun(raw, 903, 1)
	raw = encodeRun(raw, 0, cellCount-3)
	world := fixture(t, map[string][]byte{
		"Project.xml":            []byte(`<Project Version="2.4"><Subsystems><Values Name="BlocksManager"><Value Name="901" Value="PreviewTriangleBlock"/></Values></Subsystems></Project>`),
		"Regions/Region 0,0.dat": region(0, deflate(t, raw)),
	})
	w, err := Open(ctx, world)
	if err != nil {
		t.Fatal(err)
	}
	imagePNG := image.NewNRGBA(image.Rect(0, 0, 11, 13))
	imagePNG.SetNRGBA(0, 0, color.NRGBA{11, 218, 67, 255})
	var art bytes.Buffer
	if err := png.Encode(&art, imagePNG); err != nil {
		t.Fatal(err)
	}
	mod := scModFixture(t, map[string][]byte{
		"modinfo.json":                []byte(`{"Name":"Native static fixtures","PackageName":"demo.fixture"}`),
		"r2-blocks.json":              []byte(`{"version":1,"blocks":[{"class":"PreviewTriangleBlock","texture":"Assets/Textures/Fixture.png","model":"Assets/Models/Fixture.obj"},{"id":902,"atlasSlot":78,"model":"Assets/Models/Fixture.dae"}]}`),
		"Assets/Textures/Fixture.png": art.Bytes(),
		"Assets/Models/Fixture.obj":   []byte(testSCObj),
		"Assets/Models/Fixture.dae":   []byte(testSCDae),
		"Example.dll":                 []byte("this would not be a loadable assembly"),
	})
	csv := scModFixture(t, map[string][]byte{
		"modinfo.json":   []byte(`{"Name":"CSV fixtures"}`),
		"BlocksData.csv": []byte("Class Name;BlockIndex;DefaultTextureSlot;\nPreviewCubeBlock;903;78;\n"),
		"Example.js":     []byte("throw new Error('must never run')"),
	})
	names, n, err := w.ImportMods(ctx, []string{mod, csv})
	if err != nil || n != 3 || len(names) != 2 {
		t.Fatal(names, n, err)
	}
	base := image.NewNRGBA(image.Rect(0, 0, 128, 128))
	base.SetNRGBA(0, 0, color.NRGBA{197, 10, 23, 255})
	tex, hashes, err := w.RenderTextures(ctx, base)
	if err != nil {
		t.Fatal(err)
	}
	if tex.Info.AtlasWidth != 128 || tex.Info.AtlasHeight != 141 {
		t.Fatal(tex.Info)
	}
	p := tex.Files["atlas-color.bin"]
	if !bytes.Equal(p[:4], []byte{197, 10, 23, 255}) || !bytes.Equal(p[128*128*4:128*128*4+4], []byte{11, 218, 67, 255}) {
		t.Fatal("original tile or imported PNG not preserved")
	}
	var cat r2.Catalog
	if err = json.Unmarshal(tex.Files["catalog.json"], &cat); err != nil {
		t.Fatal(err)
	}
	byHash := map[uint32]uint32{}
	for s := tex.Files["states.bin"]; len(s) >= 8; s = s[8:] {
		byHash[binary.LittleEndian.Uint32(s)] = binary.LittleEndian.Uint32(s[4:])
	}
	for id, count := range map[uint32]uint32{901: 1, 902: 1, 903: 12} {
		m := cat.Models[byHash[hashes[id]]]
		if m.Kind != 2 || m.Count != count {
			t.Fatalf("%v model %+v", id, m)
		}
		ref := tex.Files["quad-references.bin"][int(m.Offset)*16:]
		if binary.LittleEndian.Uint32(ref[12:]) != 2 {
			t.Fatal("not marked as a clipped triangle")
		}
	}
	for _, bad := range []struct {
		entries map[string][]byte
		want    string
	}{
		{map[string][]byte{"modinfo.json": []byte(`{}`), "../escape.png": nil}, "路径"},
		{map[string][]byte{"modinfo.json": []byte(`{}`), "r2-blocks.json": []byte(`{"version":1,"blocks":[{"id":901,"class":"DifferentBlock","atlasSlot":1}]}`)}, "登记为"},
		{map[string][]byte{"modinfo.json": []byte(`{}`), "script.js": []byte("throw 1")}, "没有可与该存档对应"},
	} {
		_, _, err := w.ImportMods(ctx, []string{scModFixture(t, bad.entries)})
		if err == nil || !strings.Contains(err.Error(), bad.want) || len(w.staticMods) != 3 {
			t.Fatal(err, len(w.staticMods))
		}
	}
}
