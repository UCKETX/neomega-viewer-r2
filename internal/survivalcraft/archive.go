// Package survivalcraft reads saved terrain from Survivalcraft 2.4 RGN1 world exports.
package survivalcraft

import (
	"archive/zip"
	"bytes"
	"compress/flate"
	"context"
	"encoding/binary"
	"encoding/xml"
	"fmt"
	"io"
	"math"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

const (
	Kind            = "scworld"
	Size            = 16
	Height          = 256
	cellCount       = Size * Size * Height
	regionHeader    = 4 + 256*8
	maxChunkBytes   = 1 << 20
	maxRegionBytes  = 72 << 20
	maxTerrainBytes = 256 << 20
	maxProjectBytes = 16 << 20
)

var regionName = regexp.MustCompile(`^Regions/Region (-?[0-9]+),(-?[0-9]+)\.dat$`)

type Pos struct{ X, Z int32 }

// World keeps the ZIP's already-compressed chunk payloads in memory. No files
// are extracted and subsequent concurrent reads do not need the archive open.
type World struct {
	Name          string
	Position      [3]float64
	chunks        map[Pos][]byte
	positions     []Pos
	modBlocks     map[uint32]string
	staticMods    map[uint32]scModBlock
	EmbeddedAtlas []byte
	palette       [16][3]uint8
	furniture     map[uint32]furnitureDesign
}

func IsPath(path string) bool { return strings.EqualFold(filepath.Ext(path), "."+Kind) }

type value struct {
	Name string `xml:"Name,attr"`
	Text string `xml:"Value,attr"`
}

type values struct {
	Name   string   `xml:"Name,attr"`
	Fields []value  `xml:"Value"`
	Groups []values `xml:"Values"`
}

func (v values) field(name string) string {
	for _, field := range v.Fields {
		if field.Name == name {
			return field.Text
		}
	}
	return ""
}

type project struct {
	XMLName    xml.Name        `xml:"Project"`
	Version    string          `xml:"Version,attr"`
	Subsystems []values        `xml:"Subsystems>Values"`
	Entities   []projectEntity `xml:"Entities>Entity"`
}

type projectEntity struct {
	Name   string   `xml:"Name,attr"`
	Groups []values `xml:"Values"`
}

func readProject(ctx context.Context, files []*zip.File) (project, error) {
	var p project
	var xmlFile, jsonFile *zip.File
	for _, file := range files {
		if err := ctx.Err(); err != nil {
			return p, err
		}
		switch file.Name {
		case "Project.xml":
			if xmlFile != nil {
				return p, fmt.Errorf("重复的 Project.xml")
			}
			xmlFile = file
		case "Project.json":
			if jsonFile != nil {
				return p, fmt.Errorf("重复的 Project.json")
			}
			jsonFile = file
		}
	}
	// Keep the original XML world authoritative in archives that carry both
	// formats. Project.bak and Project.temp are never used as the primary save.
	file := xmlFile
	if file == nil {
		file = jsonFile
	}
	if file == nil {
		return p, fmt.Errorf("缺少 Project.xml 或 Project.json")
	}
	raw, err := readEntry(ctx, file, maxProjectBytes)
	if err != nil {
		return p, err
	}
	if file == xmlFile {
		err = xml.Unmarshal(raw, &p)
	} else {
		p, err = decodeProjectJSON(raw)
	}
	if err != nil {
		return p, fmt.Errorf("%s: %w", file.Name, err)
	}
	return p, nil
}

func Open(ctx context.Context, path string) (_ *World, err error) {
	defer func() {
		if err != nil {
			err = fmt.Errorf("打开生存战争地图 %q: %w", filepath.Base(path), err)
		}
	}()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	z, err := zip.OpenReader(path)
	if err != nil {
		return nil, err
	}
	defer z.Close()
	world := &World{
		Name:   strings.TrimSuffix(filepath.Base(path), filepath.Ext(path)),
		chunks: map[Pos][]byte{}, modBlocks: map[uint32]string{}, palette: defaultPalette,
		furniture: map[uint32]furnitureDesign{},
	}
	p, err := readProject(ctx, z.File)
	if err != nil {
		return nil, err
	}
	var bytesLeft uint64 = maxTerrainBytes
	for _, entry := range z.File {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		match := regionName.FindStringSubmatch(entry.Name)
		if match == nil {
			continue
		}
		// Keep world-block coordinates within int32, including the last cell.
		x, errX := strconv.ParseInt(match[1], 10, 24)
		z, errZ := strconv.ParseInt(match[2], 10, 24)
		if errX != nil || errZ != nil {
			return nil, fmt.Errorf("区域坐标越界: %s", entry.Name)
		}
		raw, err := readEntry(ctx, entry, min(maxRegionBytes, bytesLeft))
		if err != nil {
			return nil, err
		}
		bytesLeft -= uint64(len(raw))
		if err := world.addRegion(int32(x), int32(z), raw); err != nil {
			return nil, fmt.Errorf("%s: %w", entry.Name, err)
		}
	}
	if len(world.positions) == 0 {
		return nil, fmt.Errorf("未找到已保存的 RGN1 地形区块（版本 %q）；暂不支持 Chunks.dat 或 Chunks32fs.dat", p.Version)
	}
	slices.SortFunc(world.positions, func(a, b Pos) int {
		if a.X < b.X || (a.X == b.X && a.Z < b.Z) {
			return -1
		}
		if a == b {
			return 0
		}
		return 1
	})
	if err := world.loadProject(ctx, p); err != nil {
		return nil, err
	}
	// ExportWorld stores a custom block atlas separately from the terrain.
	name := worldTextureName(p)
	if name != "" {
		entryName := "EmbeddedContent/" + strings.TrimSuffix(filepath.Base(name), filepath.Ext(name)) + ".scbtex"
		for _, entry := range z.File {
			if entry.Name == entryName {
				world.EmbeddedAtlas, err = readEntry(ctx, entry, maxAtlasBytes)
				if err != nil {
					return nil, err
				}
				break
			}
		}
	}
	return world, nil
}

type contextReader struct {
	ctx context.Context
	io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.Reader.Read(p)
}

func readEntry(ctx context.Context, entry *zip.File, limit uint64) ([]byte, error) {
	if entry.UncompressedSize64 > limit {
		return nil, fmt.Errorf("ZIP 条目超过读取预算: %s", entry.Name)
	}
	r, err := entry.Open()
	if err != nil {
		return nil, err
	}
	defer r.Close()
	data, err := io.ReadAll(io.LimitReader(contextReader{ctx, r}, int64(entry.UncompressedSize64)+1))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", entry.Name, err)
	}
	if uint64(len(data)) != entry.UncompressedSize64 {
		return nil, fmt.Errorf("ZIP 条目大小不符: %s", entry.Name)
	}
	return data, nil
}

// RGN1: 256 little-endian (offset,size) pairs in Z-X order. CHK1 is
// outside the recorded payload size. The compressed sections may have padding.
func (w *World) addRegion(x, z int32, data []byte) error {
	if len(data) < regionHeader || string(data[:4]) != "RGN1" {
		return fmt.Errorf("无效的 RGN1 区域头")
	}
	type span struct{ start, end uint64 }
	var spans []span
	for i := range 256 {
		d := data[4+i*8:]
		offset := uint64(binary.LittleEndian.Uint32(d))
		size := uint64(binary.LittleEndian.Uint32(d[4:]))
		if offset == 0 && size == 0 {
			continue
		}
		end := offset + 4 + size
		pos := Pos{x*Size + int32(i%Size), z*Size + int32(i/Size)}
		if offset < regionHeader || size == 0 || size > maxChunkBytes || end > uint64(len(data)) || string(data[offset:offset+4]) != "CHK1" {
			return fmt.Errorf("区块 %v 的 CHK1 记录无效", pos)
		}
		if _, ok := w.chunks[pos]; ok {
			return fmt.Errorf("重复区块 %v", pos)
		}
		spans = append(spans, span{offset, end})
		w.chunks[pos] = data[offset+4 : end]
		w.positions = append(w.positions, pos)
	}
	slices.SortFunc(spans, func(a, b span) int { return int(a.start) - int(b.start) })
	for i := 1; i < len(spans); i++ {
		if spans[i].start < spans[i-1].end {
			return fmt.Errorf("CHK1 记录重叠")
		}
	}
	return nil
}

// Positions returns saved chunks only; procedural terrain was not exported.
func (w *World) Positions() []Pos { return slices.Clone(w.positions) }

// ReadChunk returns X-fastest, then Z, then Y; a missing chunk returns nil.
func (w *World) ReadChunk(ctx context.Context, pos Pos) ([]uint32, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	data, ok := w.chunks[pos]
	if !ok {
		return nil, nil
	}
	cells, err := decode(ctx, data)
	if err != nil {
		return nil, fmt.Errorf("生存战争区块 %v: %w", pos, err)
	}
	return cells, nil
}

func decode(ctx context.Context, data []byte) ([]uint32, error) {
	cells := make([]uint32, cellCount)
	err := visitRuns(ctx, data, func(word uint32, start, count int) error {
		if word&1023 != 0 {
			for i := start; i < start+count; i++ {
				cells[i] = word
			}
		}
		return nil
	})
	return cells, err
}

// BlockValues scans the compressed palette without allocating dense chunks.
func (w *World) BlockValues(ctx context.Context) ([]uint32, error) {
	seen := make(map[uint32]bool)
	for _, pos := range w.positions {
		err := visitRuns(ctx, w.chunks[pos], func(word uint32, _, _ int) error {
			if word&1023 != 0 && !seen[word] {
				if len(seen) >= 65536 {
					return fmt.Errorf("生存战争方块状态超过读取预算")
				}
				seen[word] = true
			}
			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("生存战争区块 %v: %w", pos, err)
		}
	}
	out := make([]uint32, 0, len(seen))
	for word := range seen {
		out = append(out, word)
	}
	slices.Sort(out)
	return out, nil
}

func visitRuns(ctx context.Context, data []byte, visit func(word uint32, start, count int) error) error {
	// Deflate contains one climate byte per column followed by RLE for the
	// cells. The saved light nibble encodes run length, not illumination.
	const prefix = Size * Size
	const limit = prefix + 4*cellCount
	r := flate.NewReader(bytes.NewReader(data))
	defer r.Close()
	raw, err := io.ReadAll(io.LimitReader(contextReader{ctx, r}, limit+1))
	if err != nil {
		return err
	}
	if len(raw) < prefix || len(raw) > limit {
		return fmt.Errorf("区块解压大小无效")
	}
	count := 0
	for i := prefix; i < len(raw); {
		if len(raw)-i < 4 {
			return io.ErrUnexpectedEOF
		}
		word := binary.LittleEndian.Uint32(raw[i:])
		i += 4
		run := int(word>>10&15) + 1
		if run == 16 {
			if i == len(raw) {
				return io.ErrUnexpectedEOF
			}
			run += int(raw[i])
			i++
		}
		if run > cellCount-count {
			return fmt.Errorf("RLE 超过区块高度")
		}
		word &^= 15 << 10
		if err := visit(word, count, run); err != nil {
			return err
		}
		count += run
	}
	if count != cellCount {
		return fmt.Errorf("RLE 格数错误: %d/%d", count, cellCount)
	}
	return nil
}

func vector(s string) (p [3]float64, ok bool) {
	parts := strings.Split(s, ",")
	if len(parts) != len(p) {
		return p, false
	}
	for i, part := range parts {
		v, err := strconv.ParseFloat(strings.TrimSpace(part), 64)
		if err != nil || math.IsNaN(v) || math.Abs(v) > math.MaxInt32 {
			return p, false
		}
		p[i] = v
	}
	return p, true
}

func posOf(p [3]float64) Pos { return Pos{int32(math.Floor(p[0])) >> 4, int32(math.Floor(p[2])) >> 4} }

func worldTextureName(p project) string {
	for _, v := range p.Subsystems {
		if v.Name == "GameInfo" {
			return v.field("BlockTextureName")
		}
	}
	return ""
}

func (w *World) loadProject(ctx context.Context, p project) error {
	var spawnPositions [][3]float64
	for _, entity := range p.Entities {
		if !strings.HasSuffix(entity.Name, "Player") && !strings.HasPrefix(entity.Name, "Player") {
			continue
		}
		for _, group := range entity.Groups {
			if group.Name == "Body" {
				if v, ok := vector(group.field("Position")); ok {
					spawnPositions = append(spawnPositions, v)
				}
			}
		}
	}
	for _, v := range p.Subsystems {
		switch v.Name {
		case "GameInfo":
			for _, group := range v.Groups {
				if group.Name == "Palette" {
					w.applyPalette(group.field("Colors"))
				}
			}
			if name := strings.TrimSpace(v.field("WorldName")); name != "" {
				w.Name = name
			}
		case "Players":
			for _, group := range v.Groups {
				if group.Name != "Players" {
					continue
				}
				for _, player := range group.Groups {
					if pos, ok := vector(player.field("SpawnPosition")); ok {
						spawnPositions = append(spawnPositions, pos)
					}
				}
			}
			if pos, ok := vector(v.field("GlobalSpawnPosition")); ok {
				spawnPositions = append(spawnPositions, pos)
			}
		case "BlocksManager":
			for _, field := range v.Fields {
				if id, err := strconv.ParseUint(field.Name, 10, 10); err == nil && id != 0 {
					w.modBlocks[uint32(id)] = field.Text
				}
			}
		case "FurnitureBlockBehavior":
			for _, group := range v.Groups {
				if group.Name != "FurnitureDesigns" {
					continue
				}
				for _, design := range group.Groups {
					if id, err := strconv.ParseUint(design.Name, 10, 13); err == nil {
						if f, ok := parseFurniture(design); ok {
							w.furniture[uint32(id)] = f
						}
					}
				}
			}
		}
	}
	for _, candidate := range spawnPositions {
		if _, ok := w.chunks[posOf(candidate)]; ok && candidate[1] >= 0 && candidate[1] < Height {
			return w.placeCamera(ctx, candidate)
		}
	}
	// Fall back to the saved chunk nearest the centre of all occupied chunks.
	var x, z float64
	for _, pos := range w.positions {
		x += float64(pos.X)
		z += float64(pos.Z)
	}
	x /= float64(len(w.positions))
	z /= float64(len(w.positions))
	var best Pos
	distance := math.Inf(1)
	for _, pos := range w.positions {
		dx, dz := float64(pos.X)-x, float64(pos.Z)-z
		if d := dx*dx + dz*dz; d < distance {
			best, distance = pos, d
		}
	}
	return w.placeCamera(ctx, [3]float64{float64(best.X)*Size + 8.5, 0, float64(best.Z)*Size + 8.5})
}

func (w *World) placeCamera(ctx context.Context, p [3]float64) error {
	cells, err := w.ReadChunk(ctx, posOf(p))
	if err != nil {
		return err
	}
	column := (int(math.Floor(p[0])) & 15) + (int(math.Floor(p[2]))&15)*Size
	for y := Height - 1; y >= 0; y-- {
		if cells[column+y*Size*Size] != 0 {
			p[1] = max(p[1], float64(y+1))
			break
		}
	}
	p[1] += 12
	w.Position = p
	return nil
}
