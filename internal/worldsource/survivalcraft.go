package worldsource

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"celler"
	"celler/layer_storage"
	"neomega-converter-core/worldinput"
	"neomega-r2link"
	"neomega-r2link/blockactor"
	"neomega-r2link/effector"
	"neomega-r2link/object"
	"neomega-viewer-r2/internal/survivalcraft"
	"neomega-viewer-r2/internal/worlddata"
)

type survivalcraftTerrain struct {
	source *survivalcraft.World
	hashes map[uint32]uint32
}

func openSurvivalcraft(ctx context.Context, path string, options Options) (*World, error) {
	if options.Dimension != "" && options.Dimension != "." {
		return nil, fmt.Errorf("生存战争地图只有一个主世界")
	}
	path, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	source, err := survivalcraft.Open(ctx, path)
	if err != nil {
		return nil, err
	}
	names, modBlocks, err := source.ImportMods(ctx, options.SurvivalcraftMods)
	if err != nil {
		return nil, err
	}
	position := source.Position
	if options.Position != nil {
		position = *options.Position
	}
	positions := source.Positions()
	output := &worlddata.World{
		ScanChunks: func(ctx context.Context) ([]celler.ChunkPos, error) {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			out := make([]celler.ChunkPos, len(positions))
			for i, pos := range positions {
				out[i] = celler.ChunkPos{X: pos.X, Z: pos.Z}
			}
			return out, nil
		},
	}
	w := &World{
		World: output,
		Info: worldinput.Info{
			Name: source.Name, Path: path, WorldPath: path, Kind: survivalcraft.Kind,
			Edition: "survivalcraft", Dimension: ".", Dimensions: []string{"."}, Position: position,
		},
		survivalcraft: &survivalcraftTerrain{source: source},
		SCModPaths:    options.SurvivalcraftMods, SCModNames: names, SCModBlocks: modBlocks,
	}
	return w.withSurvivalcraftTexture(ctx, options.SurvivalcraftTexture)
}

// WithSurvivalcraftTexture rebuilds the renderer bundle against the existing
// snapshot. Terrain and imported mod assets are not reread or mutated.
func (w *World) WithSurvivalcraftTexture(ctx context.Context, path string) (*World, error) {
	path = strings.Trim(strings.TrimSpace(path), "\"")
	if path != "" {
		if !survivalcraft.IsAtlasPath(path) {
			return nil, fmt.Errorf("请选择生存战争 .scbtex 或 PNG 方块图集")
		}
		abs, err := filepath.Abs(path)
		if err != nil {
			return nil, err
		}
		path = abs
	}
	return w.withSurvivalcraftTexture(ctx, path)
}

func (w *World) withSurvivalcraftTexture(ctx context.Context, path string) (*World, error) {
	if w.survivalcraft == nil {
		return nil, fmt.Errorf("当前场景不是生存战争存档")
	}
	source := w.survivalcraft.source
	atlas, name, err := source.Atlas(ctx, path)
	if err != nil {
		return nil, err
	}
	set, hashes, err := source.RenderTextures(ctx, atlas)
	if err != nil {
		return nil, err
	}
	next := *w
	next.survivalcraft = &survivalcraftTerrain{source, hashes}
	next.TextureName, next.SCTexturePath, next.Textures = name, path, set
	return &next, nil
}

// Region shares the R2Link format but sends SC state hashes, interpreted
// against the active world’s isolated model and texture catalog.
func (w *World) Region(ctx context.Context, rx, rz int) (r2link.R2Region, error) {
	if w.survivalcraft == nil {
		return w.World.Region(ctx, rx, rz)
	}
	s := w.survivalcraft
	if rx < -(1<<24) || rx >= 1<<24 || rz < -(1<<24) || rz >= 1<<24 {
		return r2link.R2Region{}, nil
	}
	var region r2link.R2Region
	for x := rx * 8; x < (rx+1)*8; x++ {
		for z := rz * 8; z < (rz+1)*8; z++ {
			cells, err := s.source.ReadChunk(ctx, survivalcraft.Pos{X: int32(x), Z: int32(z)})
			if err != nil {
				return region, err
			}
			if cells == nil {
				continue
			}
			for sy := range survivalcraft.Height / 16 {
				var storage = layer_storage.MakePaletteStorage(r2link.AirHash)
				hasBlocks := false
				for i, word := range cells[sy*4096 : (sy+1)*4096] {
					if word == 0 {
						continue
					}
					if hash, present := s.hashes[word]; present {
						storage.Set(layer_storage.At(i&15, i>>8, i>>4&15), hash)
						hasBlocks = true
					}
				}
				if hasBlocks {
					region.Sections = append(region.Sections, r2link.Subchunk{
						Position: [3]int{x, sy, z}, Bits: storage.Level,
						Palette: storage.GetUsedPalette(), Words: storage.Data,
					})
				}
			}
		}
	}
	return region, nil
}

func (w *World) Objects(ctx context.Context, x, z int, center [3]int, radius int,
	effects effector.Registry, actors blockactor.Registry) ([]object.Descriptor, error) {
	if w.survivalcraft == nil {
		return w.World.Objects(ctx, x, z, center, radius, effects, actors)
	}
	return []object.Descriptor{}, ctx.Err()
}
