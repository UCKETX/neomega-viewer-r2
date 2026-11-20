package survivalcraft

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"image"
	"image/png"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// IsAtlasPath identifies the file picker formats. The decoder still checks
// the actual image and dimensions; a filename alone does not validate it.
func IsAtlasPath(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".png", ".scbtex":
		return true
	}
	return false
}

const (
	defaultAtlasURL = "https://kaalus.wordpress.com/wp-content/uploads/2024/12/blocks-1.png"
	defaultAtlasSHA = "a58c4eff860c5786754f11ba9a7a8a43876dff1e25339798db5aabeaeb84962c"
	maxAtlasBytes   = 8 << 20
)

func readAtlasBytes(in io.Reader) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(in, maxAtlasBytes+1))
	if err == nil && len(data) > maxAtlasBytes {
		err = fmt.Errorf("生存战争材质文件超过 8 MiB")
	}
	return data, err
}

func decodeAtlas(data []byte) (image.Image, error) {
	conf, err := png.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("需要 PNG 格式的生存战争方块图集: %w", err)
	}
	// The 2.4 atlas always has 16 columns and 16 rows of square tiles.
	n := conf.Width
	if n != conf.Height || n < 128 || n > 1024 || n&(n-1) != 0 {
		return nil, fmt.Errorf("生存战争材质图集必须是 128～1024 像素的正方形，边长为 2 的幂")
	}
	return png.Decode(bytes.NewReader(data))
}

func localAtlas(path string) ([]byte, error) {
	stat, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !stat.Mode().IsRegular() {
		return nil, fmt.Errorf("生存战争材质图集必须是普通文件")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return readAtlasBytes(f)
}

// Atlas uses an explicit atlas, the world export's embedded pack, or the stock
// 2.4 art from Kaalus. Never substitute a Minecraft texture on failure.
func (w *World) Atlas(ctx context.Context, path string) (image.Image, string, error) {
	var data []byte
	var name string
	var err error
	switch {
	case path != "":
		data, err = localAtlas(path)
		name = filepath.Base(path)
	case w.EmbeddedAtlas != nil:
		data = w.EmbeddedAtlas
		name = "地图内置生存战争材质"
	default:
		data, err = defaultAtlas(ctx)
		name = "生存战争 2.4 原版"
	}
	if err != nil {
		return nil, "", fmt.Errorf("读取生存战争材质: %w（可用 -survivalcraft-texture 指定本地图集）", err)
	}
	atlas, err := decodeAtlas(data)
	return atlas, name, err
}

func verifiedAtlas(data []byte) bool {
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:]) == defaultAtlasSHA
}

func defaultAtlas(ctx context.Context) ([]byte, error) {
	cache, err := os.UserCacheDir()
	if err == nil {
		cache = filepath.Join(cache, "neomega-viewer-r2-runtime", "survivalcraft-2.4.png")
		if data, err := localAtlas(cache); err == nil && verifiedAtlas(data) {
			return data, nil
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, defaultAtlasURL, nil)
	if err != nil {
		return nil, err
	}
	client := http.Client{Timeout: 20 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("原版图集下载返回 HTTP %d", resp.StatusCode)
	}
	data, err := readAtlasBytes(resp.Body)
	if err != nil {
		return nil, err
	}
	if !verifiedAtlas(data) {
		return nil, fmt.Errorf("原版图集校验失败")
	}
	if cache != "" {
		cacheAtlas(cache, data)
	}
	return data, nil
}

func cacheAtlas(path string, data []byte) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return
	}
	f, err := os.CreateTemp(dir, "sc-atlas-*.png")
	if err != nil {
		return
	}
	defer os.Remove(f.Name())
	_, writeErr := f.Write(data)
	closeErr := f.Close()
	if writeErr == nil && closeErr == nil {
		_ = os.Rename(f.Name(), path)
	}
}
