package survivalcraft

import (
	"archive/zip"
	"bytes"
	"compress/flate"
	"context"
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func deflate(t *testing.T, raw []byte) []byte {
	t.Helper()
	var out bytes.Buffer
	w, err := flate.NewWriter(&out, flate.BestSpeed)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(raw); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func encodeRun(dst []byte, word uint32, count int) []byte {
	for count > 0 {
		run := min(count, 271)
		dst = binary.LittleEndian.AppendUint32(dst, word|uint32(min(run, 16)-1)<<10)
		if run >= 16 {
			dst = append(dst, byte(run-16))
		}
		count -= run
	}
	return dst
}

func region(slot int, payload []byte) []byte {
	data := make([]byte, regionHeader)
	copy(data, "RGN1")
	binary.LittleEndian.PutUint32(data[4+slot*8:], regionHeader)
	binary.LittleEndian.PutUint32(data[8+slot*8:], uint32(len(payload)))
	return append(append(data, "CHK1"...), payload...)
}

func fixture(t *testing.T, data map[string][]byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "故宫.SCWORLD")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	z := zip.NewWriter(f)
	for name, raw := range data {
		w, err := z.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(raw); err != nil {
			t.Fatal(err)
		}
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestReadTerrain(t *testing.T) {
	ctx := context.Background()
	x, y, z := 2, 250, 3
	index := x + z*Size + y*Size*Size
	word := uint32(49 | (6 << 14))
	raw := encodeRun(make([]byte, 256), 0, index)
	raw = encodeRun(raw, word, 1)
	raw = encodeRun(raw, 0, cellCount-index-1)
	path := fixture(t, map[string][]byte{
		"Project.xml": []byte(`<Project Version="2.4"><Subsystems>
		<Values Name="GameInfo"><Value Name="WorldName" Value="故宫"/></Values>
		<Values Name="BlocksManager"><Value Name="300" Value="RedGlassBlock"/></Values>
		<Values Name="FurnitureBlockBehavior"><Values Name="FurnitureDesigns"><Values Name="0">
		<Value Name="Resolution" Value="3"/><Value Name="Values" Value="1*21,2*0,1*21,2*0,1*21,2*0,1*21,2*0,1*21,2*0,1*21,2*0,1*21,2*0,1*21,2*0,1*21,2*0,"/>
		</Values></Values></Values></Subsystems>
		<Entities><Entity Name="MalePlayer"><Values Name="Body"><Value Name="Position" Value="-29.5,10,1139.5"/></Values></Entity></Entities>
		</Project>`),
		"Regions/Region -1,4.dat": region(14+7*16, deflate(t, raw)),
	})
	w, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	pos := Pos{-2, 71}
	if p := w.Positions(); len(p) != 1 || p[0] != pos || w.Name != "故宫" || w.Position != ([3]float64{-29.5, 263, 1139.5}) {
		t.Fatalf("wrong world info: %v %v %v", p, w.Name, w.Position)
	}
	cells, err := w.ReadChunk(ctx, pos)
	if err != nil || len(cells) != cellCount || cells[index] != word || cells[index-1] != 0 || cells[index+1] != 0 {
		t.Fatal("bad terrain decode", len(cells), err)
	}
	missing, err := w.ReadChunk(ctx, Pos{})
	if missing != nil || err != nil {
		t.Fatal("missing chunk must not create terrain")
	}
	if state := w.BlockState(word); state != `minecraft:oak_stairs ["facing"="south","half"="top","shape"="straight","waterlogged"="false"]` {
		t.Fatal(state)
	}
	if state := w.BlockState(300); state != "minecraft:red_stained_glass []" {
		t.Fatal(state)
	}
	if state := w.BlockState(227); !strings.HasPrefix(state, "minecraft:oak_trapdoor [") {
		t.Fatal(state)
	}
	if state := w.BlockState(94 | ((1 | 8) << 14)); state != `minecraft:oak_fence ["east"="true","north"="true","south"="false","waterlogged"="false","west"="false"]` {
		t.Fatal(state)
	}

	if state := w.BlockState(1023); state != Unknown {
		t.Fatal(state)
	}
}

func TestCorruptAndUnsupportedArchive(t *testing.T) {
	info := []byte(`<Project Version="2.4"/>`)
	const entry = "Regions/Region 0,0.dat"
	ctx := context.Background()
	for _, tc := range []struct {
		name     string
		contents map[string][]byte
		want     string
	}{
		{"missing XML", map[string][]byte{entry: region(0, []byte{1})}, "缺少 Project.xml"},
		{"old terrain", map[string][]byte{"Project.xml": info, "Chunks32fs.dat": []byte{1}}, "暂不支持"},
		{"truncated region", map[string][]byte{"Project.xml": info, entry: []byte("RGN1")}, "RGN1"},
		{"bad pointer", map[string][]byte{"Project.xml": info, entry: region(0, []byte{1})[:regionHeader+4]}, "CHK1"},
		{"short decode", map[string][]byte{"Project.xml": info, entry: region(0, deflate(t, make([]byte, 256)))}, "RLE 格数错误"},
		{"overflow", map[string][]byte{"Project.xml": info, entry: region(0, deflate(t, encodeRun(make([]byte, 256), 0, cellCount+1)))}, "RLE 超过"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Open(ctx, fixture(t, tc.contents))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v, want %v", err, tc.want)
			}
		})
	}
}

func TestCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Open(ctx, "not opened.scworld"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
