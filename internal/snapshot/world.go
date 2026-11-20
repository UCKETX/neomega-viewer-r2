package snapshot

import (
	"cmp"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"math"
	"os"
	"slices"

	"neomega-converter-core/worldinput"
	"neomega-r2link"
	"neomega-viewer-r2/internal/worldsource"
)

type bounds struct {
	Min [3]int `json:"min"`
	Max [3]int `json:"max"` // 方块坐标，右端不含。
}

func (b *bounds) include(next bounds) {
	for i := range b.Min {
		b.Min[i] = min(b.Min[i], next.Min[i])
		b.Max[i] = max(b.Max[i], next.Max[i])
	}
}

type dimensionInfo struct {
	ID       string `json:"id"`
	Key      int    `json:"key"` // HTML 内标识；不同维度可以有相同的区域坐标。
	Frame    bounds `json:"frame"`
	Regions  int    `json:"regions"`
	Sections int    `json:"sections"`
	Clipped  int    `json:"clipped"`
}

type manifest struct {
	Version    int             `json:"version"`
	Name       string          `json:"name"`
	Dimensions []dimensionInfo `json:"dimensions"`
}

// 写一个独立的 gzip 成员，浏览器可按区域加载，Go 侧只保留当前区域的方块。
func writeBlock(w io.Writer, attributes string, data []byte) error {
	if _, err := fmt.Fprintf(w, "<script type=\"application/octet-stream\" %s>", attributes); err != nil {
		return err
	}
	encoded := base64.NewEncoder(base64.StdEncoding, w)
	zipped := gzip.NewWriter(encoded)
	if _, err := zipped.Write(data); err != nil {
		return err
	}
	if err := zipped.Close(); err != nil {
		return err
	}
	if err := encoded.Close(); err != nil {
		return err
	}
	_, err := io.WriteString(w, "</script>\n")
	return err
}

func writeWorld(ctx context.Context, w io.Writer, path, name, selected string) error {
	return writeWorldPrepared(ctx, w, worldsource.Options{Options: worldinput.Options{Path: path, Dimension: selected}}, name, nil)
}

func writeWorldPrepared(ctx context.Context, w io.Writer, options worldsource.Options, name string, prepare func(*worldsource.World) error) error {
	selected := options.Dimension
	meta := manifest{Version: 1, Name: name}
	dimensions := []string{selected}
	for n := 0; n < len(dimensions); n++ {
		options.Dimension = dimensions[n]
		world, err := worldsource.Open(ctx, options)
		if err != nil {
			return err
		}
		if n == 0 && prepare != nil {
			if err := prepare(world); err != nil {
				world.Close()
				return err
			}
			if world.Textures != nil {
				meta.Name = world.Info.Name
			}
		}
		if n == 0 && selected == "" {
			for _, dim := range world.Info.Dimensions {
				if dim != world.Info.Dimension {
					dimensions = append(dimensions, dim)
				}
			}
		}
		info, err := writeDimension(ctx, w, world, n)
		closeErr := world.Close()
		if err != nil {
			return fmt.Errorf("读取维度 %s：%w", info.ID, err)
		}
		if closeErr != nil {
			return closeErr
		}
		fmt.Fprintf(os.Stderr, "维度 %s：%d 个 r2region，%d 个非空气子区块，%d 个子区块超出 R2 高度范围\n", info.ID, info.Regions, info.Sections, info.Clipped)
		if info.Sections != 0 {
			meta.Dimensions = append(meta.Dimensions, info)
		}
	}
	if len(meta.Dimensions) == 0 {
		return fmt.Errorf("所选存档或维度在 R2 高度范围 Y=-64～447 内没有可预览的方块")
	}
	if _, err := io.WriteString(w, "<script type=\"application/json\" id=\"r2-world\">"); err != nil {
		return err
	}
	if err := json.NewEncoder(w).Encode(meta); err != nil {
		return err
	}
	_, err := io.WriteString(w, "</script>\n")
	return err
}

func writeDimension(ctx context.Context, w io.Writer, world *worldsource.World, key int) (dimensionInfo, error) {
	info := dimensionInfo{ID: world.Info.Dimension, Key: key}
	chunks, err := world.ScanChunks(ctx)
	if err != nil {
		return info, err
	}
	// 算术右移对负区块坐标同样向下取整。只遍历实际有区块的区域。
	regions := make(map[[2]int]struct{})
	for _, pos := range chunks {
		regions[[2]int{int(pos.X >> 3), int(pos.Z >> 3)}] = struct{}{}
	}
	keys := slices.SortedFunc(maps.Keys(regions), func(a, b [2]int) int {
		if a[0] != b[0] {
			return cmp.Compare(a[0], b[0])
		}
		return cmp.Compare(a[1], b[1])
	})
	index := r2link.ChunkIndex{}
	var all, nearest bounds
	best := math.Inf(1)
	spawn := world.Info.Position
	for _, coord := range keys {
		region, err := world.Region(ctx, coord[0], coord[1])
		if err != nil {
			return info, fmt.Errorf("r2region %v：%w", coord, err)
		}
		info.Clipped += region.Clipped
		if len(region.Sections) == 0 {
			continue
		}
		var area bounds
		for i, section := range region.Sections {
			var box bounds
			for axis, value := range section.Position {
				box.Min[axis], box.Max[axis] = value*16, (value+1)*16
			}
			if i == 0 {
				area = box
			} else {
				area.include(box)
			}
			index.Add(section.Position[0], section.Position[2])
			// 大存档从靠近出生点的非空区块上方开始，避免初始相机对着稀疏区域之间的空地。
			distance := math.Hypot(float64(box.Min[0]+8)-spawn[0], float64(box.Min[2]+8)-spawn[2])
			if distance < best || (distance == best && box.Max[1] > nearest.Max[1]) {
				best, nearest = distance, box
			}
		}
		if info.Regions == 0 {
			all = area
		} else {
			all.include(area)
		}
		attr := fmt.Sprintf("data-dimension=\"%d\" data-r2-region=\"%d/%d\"", key, coord[0], coord[1])
		if err := writeBlock(w, attr, r2link.EncodeRegion(region.Sections)); err != nil {
			return info, err
		}
		info.Regions++
		info.Sections += len(region.Sections)
	}
	if info.Regions == 0 {
		return info, nil
	}
	info.Frame = all
	// 相距甚远的建筑之间可能是空白。大存档从出生点附近实际有内容的区域开始。
	if max(all.Max[0]-all.Min[0], all.Max[2]-all.Min[2]) > 256 {
		info.Frame = nearest
	}
	return info, writeBlock(w, fmt.Sprintf("id=\"r2-chunks-%d\"", key), index.Encode())
}
