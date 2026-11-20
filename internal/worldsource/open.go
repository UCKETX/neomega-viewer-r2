// Package worldsource 把原生存档与转换修复接到 R2 的公共区域适配器。
package worldsource

import (
	"context"
	"strings"
	"sync"

	"celler"
	"celler/java"
	converter "neomega-converter-core"
	"neomega-converter-core/be2je/adapt"
	"neomega-converter-core/worldinput"
	fixedadapt "neomega-converter/adapt"
	"neomega-view-utils/fixer/java-postprocess"
	"neomega-view-utils/r2assets"
	"neomega-viewer-r2/internal/survivalcraft"
	"neomega-viewer-r2/internal/worlddata"
)

var sharedFixer = sync.OnceValues(fixer.New)

// Options 把原生打开选项和显示用的邻接修复分开，Java 状态不经过 Bedrock 修复。
type Options struct {
	worldinput.Options
	SkipBedrockFix       bool
	SurvivalcraftTexture string
	SurvivalcraftMods    []string
}

// World 拥有文件来源，渲染适配器只借用它提供的只读 Java 区块。
type World struct {
	*worlddata.World
	Info          worldinput.Info
	input         *worldinput.Source
	survivalcraft *survivalcraftTerrain
	Textures      *r2assets.TextureSet
	TextureName   string
	SCTexturePath string
	SCModPaths    []string
	SCModNames    []string
	SCModBlocks   int
}

// Open 在发布会话前完成原生打开与修复器准备，失败时保留调用方的旧世界。
func Open(ctx context.Context, options Options) (*World, error) {
	path := strings.Trim(strings.TrimSpace(options.Path), "\"")
	if survivalcraft.IsPath(path) {
		return openSurvivalcraft(ctx, path, options)
	}
	input, err := worldinput.Open(ctx, options.Options)
	if err != nil {
		return nil, err
	}
	output := &worlddata.World{}
	if input.Java != nil {
		output.ScanChunks = input.Java.Chunks
		output.ReadChunk = func(ctx context.Context, pos celler.ChunkPos) (*converter.JavaChunk, error) {
			records, err := input.Java.Read(ctx, pos)
			if err != nil || records[0] == nil {
				return nil, err
			}
			raw, err := records[0].Decode()
			if err != nil {
				return nil, err
			}
			chunk, err := java.DecodeChunk(raw)
			if err != nil {
				return nil, err
			}
			ys, err := chunk.SectionYs()
			if err != nil {
				return nil, err
			}
			result := &converter.JavaChunk{Pos: pos, Sections: map[int32]*converter.JavaSection{}}
			for _, y := range ys {
				section, err := chunk.Section(y)
				if err != nil {
					return nil, err
				}
				result.Sections[y] = &converter.JavaSection{Blocks: section.Blocks}
			}
			result.BlockEntities, _, err = chunk.BlockEntities()
			return result, err
		}
	} else {
		reads := input.Bedrock
		converted := adapt.NewConverted(reads.Read, adapt.Options{})
		var chunks adapt.ChunkSource = converted
		if !options.SkipBedrockFix {
			f, err := sharedFixer()
			if err != nil {
				input.Close()
				return nil, err
			}
			chunks = fixedadapt.NewFixed(converted, f, adapt.Options{})
		}
		output.ReadChunk = chunks.Chunk
		output.ScanChunks = reads.Chunks
	}
	return &World{World: output, Info: input.Info, input: input}, nil
}

// Close 将渲染请求归还的来源交回原生读取层，由它等待底层 I/O 并关闭句柄。
func (w *World) Close() error {
	if w.input != nil {
		return w.input.Close()
	}
	return nil
}
