package filebrowse

import (
	"os"
	"path/filepath"
	"testing"
)

// 粘贴文件路径后仍应保留选中项，材质包与世界来源在各自面板中识别。
func TestSourceKindsAndSelectedFile(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	for _, name := range []string{"world.pack", "a.schem", "b.schematic", "c.litematic", "d.nbt", "e.mcstructure", "f.mcworld", "texture.zip", "ignore.txt"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte{1}, 0644); err != nil {
			t.Fatal(err)
		}
	}
	for _, dir := range []string{"java/region", "bedrock/db", "texture/assets"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0755); err != nil {
			t.Fatal(err)
		}
	}
	selected := filepath.Join(root, "world.pack")
	listing, err := List(selected, false)
	if err != nil {
		t.Fatal(err)
	}
	if listing.Selected != selected || listing.Path != root || listing.Kind != "pack" {
		t.Fatal(listing)
	}
	expected := map[string]string{"world.pack": "pack", "a.schem": "schem", "b.schematic": "schematic", "c.litematic": "litematic", "d.nbt": "nbt", "e.mcstructure": "mcstructure", "f.mcworld": "mcworld", "java": "directory", "bedrock": "bedrock", "texture": "folder"}
	if len(listing.Entries) != len(expected) {
		t.Fatal(listing.Entries)
	}
	for _, entry := range listing.Entries {
		if expected[entry.Name] != entry.Kind || entry.Openable != (entry.Kind != "folder") {
			t.Fatal(entry)
		}
	}
	listing, err = List("", true)
	if err != nil {
		t.Fatal(err)
	}
	if listing.Path != root || listing.Roots[0].Name != "工作目录" || listing.Roots[0].Path != root {
		t.Fatal(listing)
	}
	// 普通目录 bedrock 的名称排在前面，可打开的材质包仍应优先显示。
	if listing.Entries[0].Name != "texture" || listing.Entries[1].Name != "texture.zip" {
		t.Fatal(listing.Entries)
	}
	for _, entry := range listing.Entries {
		if entry.Openable && entry.Kind != "texture" {
			t.Fatal(entry)
		}
	}
	for _, name := range []string{"texture", "texture.zip"} {
		path := filepath.Join(root, name)
		selected, err := List(path, true)
		if err != nil || selected.Kind != "texture" {
			t.Fatalf("材质包路径 %q 的识别结果为 %q，错误为 %v", path, selected.Kind, err)
		}
	}
}

func TestSurvivalcraftSource(t *testing.T) {
	path := filepath.Join(t.TempDir(), "故宫.SCWORLD")
	if err := os.WriteFile(path, nil, 0600); err != nil {
		t.Fatal(err)
	}
	files, err := List(path, false)
	if err != nil {
		t.Fatal(err)
	}
	if files.Kind != "scworld" || files.Selected != path || len(files.Entries) != 1 || !files.Entries[0].Openable {
		t.Fatalf("unexpected listing: %+v", files)
	}
}

func TestSurvivalcraftTexturePicker(t *testing.T) {
	root := t.TempDir()
	for _, file := range []string{"地图.scworld", "mod.scmod", "方块.SCBTEX", "图集.png", "MC.zip"} {
		if err := os.WriteFile(filepath.Join(root, file), nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(root, "folder"), 0700); err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(root, "方块.SCBTEX")
	files, err := ListSCTextures(want)
	if err != nil {
		t.Fatal(err)
	}
	if files.Kind != "sc-texture" || files.Selected != want || len(files.Entries) != 3 {
		t.Fatal(files)
	}
	for _, entry := range files.Entries {
		if entry.Directory {
			if entry.Openable || entry.Kind != "folder" {
				t.Fatal(entry)
			}
		} else if !entry.Openable || entry.Kind != "sc-texture" {
			t.Fatal(entry)
		}
	}
	mc, err := ListSCTextures(filepath.Join(root, "MC.zip"))
	if err != nil || mc.Selected != "" {
		t.Fatal(mc, err)
	}
}
