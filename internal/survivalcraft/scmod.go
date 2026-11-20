package survivalcraft

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

const maxMods = 8

type scModSpec struct {
	ID      uint32     `json:"id"`
	Class   string     `json:"class"`
	Model   string     `json:"model"`
	Texture string     `json:"texture"`
	Slot    *uint8     `json:"atlasSlot"`
	Scale   float64    `json:"scale"`
	Offset  [3]float64 `json:"offset"`
}
type scModBlock struct {
	mesh []scTriangle
	tex  image.Image
	slot uint8
}

// ImportMods binds read-only ZIP assets to this world only. It never extracts
// an archive and never loads assembly, script, or game behavior entries.
func (w *World) ImportMods(ctx context.Context, paths []string) ([]string, int, error) {
	if len(paths) > maxMods {
		return nil, 0, fmt.Errorf("最多可同时导入 %d 个 .scmod", maxMods)
	}
	if len(paths) == 0 {
		return nil, 0, nil
	}
	words, err := w.BlockValues(ctx)
	if err != nil {
		return nil, 0, err
	}
	used := map[uint32]bool{}
	for _, word := range words {
		used[word&1023] = true
	}
	out := make(map[uint32]scModBlock)
	var names []string
	pixels := 0
	for _, file := range paths {
		file = strings.Trim(strings.TrimSpace(file), "\"")
		if !strings.EqualFold(filepath.Ext(file), ".scmod") {
			return nil, 0, fmt.Errorf("模组文件必须是 .scmod: %s", filepath.Base(file))
		}
		name, err := w.readSCMod(ctx, file, used, out, &pixels)
		if err != nil {
			return nil, 0, fmt.Errorf("导入模组 %q: %w", filepath.Base(file), err)
		}
		names = append(names, name)
	}
	w.staticMods = out
	return names, len(out), nil
}

func (w *World) readSCMod(ctx context.Context, file string, used map[uint32]bool, out map[uint32]scModBlock, pixels *int) (string, error) {
	stat, err := os.Stat(file)
	if err != nil {
		return "", err
	}
	if !stat.Mode().IsRegular() || stat.Size() > 256<<20 {
		return "", fmt.Errorf("模组需要不超过 256 MiB 的普通文件")
	}
	z, err := zip.OpenReader(file)
	if err != nil {
		return "", err
	}
	defer z.Close()
	if len(z.File) > 4096 {
		return "", fmt.Errorf("模组条目过多")
	}
	files := map[string]*zip.File{}
	for _, entry := range z.File {
		if e := ctx.Err(); e != nil {
			return "", e
		}
		if entry.FileInfo().IsDir() {
			continue
		}
		n := entry.Name
		if !safeModPath(n) || files[n] != nil {
			return "", fmt.Errorf("无效或重复的模组资源路径 %q", n)
		}
		files[n] = entry
	}
	meta := files["modinfo.json"]
	if meta == nil {
		return "", fmt.Errorf("缺少 modinfo.json")
	}
	raw, err := readEntry(ctx, meta, 256<<10)
	if err != nil {
		return "", err
	}
	var info struct {
		Name        string
		PackageName string
	}
	if e := json.Unmarshal(trimBOM(raw), &info); e != nil {
		return "", fmt.Errorf("modinfo.json: %w", e)
	}
	name := strings.TrimSpace(info.Name)
	if name == "" {
		name = info.PackageName
	}
	if name == "" {
		name = filepath.Base(file)
	}
	specs, err := modSpecs(ctx, files)
	if err != nil {
		return "", err
	}
	start := len(out)
	for _, s := range specs {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		id := s.ID
		if id == 0 {
			for k, v := range w.modBlocks {
				if v == s.Class && s.Class != "" {
					if id != 0 {
						return "", fmt.Errorf("方块类 %q 在存档内有多个 ID，请显式指定", s.Class)
					}
					id = k
				}
			}
		}
		if id == 0 || !used[id] {
			continue
		}
		class := w.modBlocks[id]
		if class != "" && s.Class != "" && class != s.Class {
			return "", fmt.Errorf("ID %d 登记为 %q，模组声明为 %q", id, class, s.Class)
		}
		if class == "" && w.BlockState(id) != Unknown {
			return "", fmt.Errorf("不能以模组覆盖原版方块 ID %d", id)
		}
		if _, dup := out[id]; dup {
			return "", fmt.Errorf("多个声明绑定相同的方块 ID %d", id)
		}
		block, err := readModBlock(ctx, files, s, pixels)
		if err != nil {
			return "", fmt.Errorf("方块 ID %d: %w", id, err)
		}
		out[id] = block
	}
	if len(out) == start {
		return "", fmt.Errorf("没有可与该存档对应的静态方块；需要存档方块注册表或 r2-blocks.json 中的明确 ID 与资源绑定")
	}
	return name, nil
}

func trimBOM(raw []byte) []byte { return bytes.TrimPrefix(raw, []byte{0xef, 0xbb, 0xbf}) }

func safeModPath(p string) bool {
	return p != "" && p != "." && p != ".." && !strings.HasPrefix(p, "../") && !path.IsAbs(p) && path.Clean(p) == p && !strings.ContainsAny(p, "\\:\x00")
}
func modSpecs(ctx context.Context, files map[string]*zip.File) ([]scModSpec, error) {
	if manifest := files["r2-blocks.json"]; manifest != nil {
		raw, e := readEntry(ctx, manifest, 512<<10)
		if e != nil {
			return nil, e
		}
		var m struct {
			Version int
			Blocks  []scModSpec
		}
		if e = json.Unmarshal(trimBOM(raw), &m); e != nil {
			return nil, fmt.Errorf("r2-blocks.json: %w", e)
		}
		if m.Version != 1 || len(m.Blocks) == 0 || len(m.Blocks) > 1023 {
			return nil, fmt.Errorf("r2-blocks.json 需要 version=1 及非空 blocks")
		}
		for _, s := range m.Blocks {
			if s.ID > 1023 || s.ID == 0 && s.Class == "" {
				return nil, fmt.Errorf("方块必须声明有效的 id (1～1023) 或 class")
			}
		}
		return m.Blocks, nil
	}
	var names []string
	for n := range files {
		if strings.EqualFold(path.Ext(n), ".csv") {
			names = append(names, n)
		}
	}
	slices.Sort(names)
	var out []scModSpec
	var budget uint64 = 8 << 20
	for _, n := range names {
		raw, e := readEntry(ctx, files[n], min(budget, 2<<20))
		if e != nil {
			return nil, e
		}
		budget -= uint64(len(raw))
		reader := csv.NewReader(bytes.NewReader(trimBOM(raw)))
		reader.Comma = ';'
		reader.FieldsPerRecord = -1
		head, e := reader.Read()
		if e != nil {
			continue
		}
		columns := map[string]int{}
		for i, c := range head {
			columns[strings.ToLower(strings.TrimSpace(c))] = i
		}
		if _, ok := columns["class name"]; !ok {
			continue
		}
		rows, e := reader.ReadAll()
		if e != nil {
			return nil, fmt.Errorf("%s: %w", n, e)
		}
		for _, row := range rows {
			get := func(k string) string {
				if i, ok := columns[k]; ok && i < len(row) {
					return strings.TrimSpace(row[i])
				}
				return ""
			}
			class := get("class name")
			if class == "" {
				continue
			}
			s := scModSpec{Class: class, Model: get("model"), Texture: get("texture")}
			if raw := get("blockindex"); raw != "" {
				id, e := strconv.ParseUint(raw, 10, 10)
				if e != nil || id == 0 {
					return nil, fmt.Errorf("%s 方块 %s 的 BlockIndex 无效", n, class)
				}
				s.ID = uint32(id)
			}
			if slot, e := strconv.ParseUint(get("defaulttextureslot"), 10, 8); e == nil {
				v := uint8(slot)
				s.Slot = &v
			}
			for _, stem := range []string{class, strings.TrimSuffix(class, "Block")} {
				if s.Model == "" {
					for _, ext := range []string{".obj", ".dae"} {
						p := "Assets/Models/" + stem + ext
						if files[p] != nil {
							s.Model = p
							break
						}
					}
				}
				if p := "Assets/Textures/" + stem + ".png"; s.Texture == "" && files[p] != nil {
					s.Texture = p
				}
			}
			if s.Texture != "" || s.Slot != nil {
				out = append(out, s)
			}
			if len(out) > 1023 {
				return nil, fmt.Errorf("模组方块声明过多")
			}
		}
	}
	return out, nil
}

func readModBlock(ctx context.Context, files map[string]*zip.File, s scModSpec, pixels *int) (scModBlock, error) {
	block := scModBlock{}
	get := func(n, ext string, limit uint64) ([]byte, error) {
		if !safeModPath(n) || !strings.EqualFold(path.Ext(n), ext) || files[n] == nil {
			return nil, fmt.Errorf("不支持或找不到模组资源 %q", n)
		}
		return readEntry(ctx, files[n], limit)
	}
	switch {
	case s.Texture != "":
		raw, err := get(s.Texture, ".png", 4<<20)
		if err != nil {
			return block, err
		}
		c, err := png.DecodeConfig(bytes.NewReader(raw))
		if err != nil || c.Width < 1 || c.Height < 1 || c.Width > 1024 || c.Height > 1024 {
			return block, fmt.Errorf("预览 PNG 必须为 1～1024 像素")
		}
		*pixels += c.Width * c.Height
		if *pixels > 2<<20 {
			return block, fmt.Errorf("模组贴图总面积超过预览预算")
		}
		block.tex, err = png.Decode(bytes.NewReader(raw))
		if err != nil {
			return block, err
		}
	case s.Slot != nil:
		block.slot = *s.Slot
	default:
		return block, fmt.Errorf("缺少可读取的 PNG 或 atlasSlot")
	}
	if s.Model == "" {
		block.mesh = scUnitCube()
		return block, nil
	}
	ext := strings.ToLower(path.Ext(s.Model))
	if ext != ".obj" && ext != ".dae" {
		return block, fmt.Errorf("静态模型目前只支持 OBJ 或 DAE")
	}
	raw, err := get(s.Model, ext, 4<<20)
	if err != nil {
		return block, err
	}
	if ext == ".obj" {
		block.mesh, err = readSCObj(raw)
	} else {
		block.mesh, err = readSCDae(raw)
	}
	if err != nil {
		return block, err
	}
	if err = fitSCModel(block.mesh, s.Scale, s.Offset); err != nil {
		return block, err
	}
	return block, nil
}
