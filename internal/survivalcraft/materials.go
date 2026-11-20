package survivalcraft

import (
	"strconv"
	"strings"
)

// Face order matches R2: -X, +X, -Y, +Y, -Z, +Z.
type Material struct {
	Faces   [6]uint8
	Tint    [6][3]uint8
	Opacity uint8
	Group   uint32
	Known   bool
}

var white = [3]uint8{255, 255, 255}

// Survivalcraft 2.4 atlas slots from BlocksData and the block classes.
// For paintable blocks the second entry is the material before tinting.
var atlasSlots = map[uint32][2]uint8{
	1: {17, 17}, 2: {2, 2}, 3: {1, 24}, 4: {176, 64}, 5: {16, 69},
	6: {19, 19}, 7: {18, 18}, 8: {3, 3}, 9: {20, 21}, 10: {117, 21}, 11: {116, 21},
	12: {52, 52}, 13: {107, 107}, 14: {132, 132}, 15: {67, 67}, 16: {34, 34},
	17: {212, 212}, 18: {189, 189}, 19: {85, 85}, 20: {12, 12}, 21: {4, 23},
	22: {227, 227}, 24: {13, 13}, 25: {14, 14}, 26: {54, 50}, 27: {4, 4}, 28: {55, 55},
	31: {215, 215}, 39: {33, 33}, 44: {83, 83}, 46: {180, 180}, 47: {181, 181},
	48: {16, 69}, 49: {4, 23}, 50: {54, 50}, 51: {176, 64}, 52: {176, 64}, 53: {16, 69},
	54: {54, 50}, 55: {4, 23}, 59: {47, 47}, 60: {99, 99}, 61: {66, 66}, 62: {65, 65},
	64: {16, 16}, 65: {16, 16}, 66: {5, 5}, 67: {6, 40}, 68: {7, 51}, 69: {7, 51},
	70: {7, 51}, 72: {8, 15}, 73: {70, 39}, 75: {70, 39}, 76: {70, 39},
	83: {115, 115}, 84: {114, 114}, 87: {22, 22}, 92: {141, 141}, 94: {4, 23},
	95: {6, 40}, 96: {6, 40}, 97: {4, 23}, 98: {4, 23}, 99: {71, 71}, 112: {48, 48},
	126: {182, 182}, 127: {100, 100}, 136: {1, 24}, 142: {78, 78}, 150: {62, 62},
	163: {6, 40}, 164: {70, 39}, 166: {4, 23}, 168: {2, 2}, 193: {58, 58}, 194: {58, 58},
	202: {1, 24}, 208: {38, 38}, 210: {63, 63}, 211: {63, 63}, 213: {63, 63},
	217: {1, 24}, 225: {132, 132}, 255: {116, 21}, 256: {132, 132},
	259: {2, 2}, 260: {2, 2}, 262: {109, 21}, 263: {52, 52},
}

var defaultPalette = [16][3]uint8{
	{255, 255, 255}, {181, 255, 255}, {255, 181, 255}, {160, 181, 255},
	{255, 240, 160}, {181, 255, 181}, {255, 181, 160}, {181, 181, 181},
	{112, 112, 112}, {32, 112, 112}, {112, 32, 112}, {26, 52, 128},
	{87, 54, 31}, {24, 116, 24}, {136, 32, 32}, {24, 24, 24},
}

func (w *World) applyPalette(encoded string) {
	for i, field := range strings.Split(encoded, ";") {
		if i >= len(w.palette) {
			break
		}
		parts := strings.Split(field, ",")
		if len(parts) < 3 {
			continue
		}
		var rgb [3]uint8
		ok := true
		for c := range 3 {
			n, err := strconv.ParseUint(strings.TrimSpace(parts[c]), 10, 8)
			if err != nil {
				ok = false
				break
			}
			rgb[c] = uint8(n)
		}
		if ok {
			w.palette[i] = rgb
		}
	}
}

func (w *World) Material(word uint32) Material {
	id, data := word&1023, word>>14
	if id == 227 {
		if d, ok := w.furniture[(data>>15&7)<<10|(data>>2&1023)]; ok {
			return w.Material(d.material &^ (15 << 10))
		}
		return Material{}
	}
	slot, ok := atlasSlots[id]
	tint, alpha, group := white, uint8(255), uint32(0)
	if name, mod := w.modBlocks[id]; mod {
		if !strings.HasSuffix(name, "GlassBlock") || w.BlockState(word) == Unknown {
			return Material{}
		}
		slot, ok, alpha, group = [2]uint8{67, 67}, true, 128, 2
		modColors := map[string]uint8{"Black": 15, "Blue": 11, "Brown": 12, "Cyan": 9, "Gray": 8, "Green": 13,
			"Lightblue": 3, "Lightgray": 7, "Lime": 5, "Magenta": 10, "Orange": 6, "Pink": 2,
			"Purple": 10, "Red": 14, "White": 0, "Yellow": 4}
		tint = w.palette[modColors[strings.TrimSuffix(name, "GlassBlock")]]
	} else if spec, native := nativeBlocks[id]; native {
		return w.nativeMaterial(id, data, spec)
	}
	if !ok {
		return Material{}
	}
	index := slot[0]
	var colored bool
	var paintIndex uint32
	switch {
	case cube[id] != "":
		colored, paintIndex = data&1 != 0, (data>>1)&15
	case stairsBase[id] != "":
		colored, paintIndex = data&32 != 0, (data>>6)&15
	case slabsBase[id] != "":
		colored, paintIndex = data&2 != 0, (data>>2)&15
	case id == 94 || id == 163 || id == 164 || id == 166 || id == 193 || id == 194 || id == 202:
		colored, paintIndex = data&16 != 0, (data>>5)&15
	case id == 97 || id == 210:
		colored, paintIndex = data&16 != 0, (data>>5)&15
	case id == 98 || id == 211:
		colored, paintIndex = data&4 != 0, (data>>3)&15
	case id == 208:
		colored, paintIndex = true, data&15
	}
	if colored {
		index, tint = slot[1], w.palette[paintIndex]
	}
	switch id {
	case 12, 13, 14, 225, 256, 263, 19:
		tint = [3]uint8{106, 171, 85}
	case 18:
		tint, alpha, group = [3]uint8{16, 92, 114}, 180, 1
	case 62:
		tint, alpha, group = [3]uint8{205, 230, 245}, 180, 3
	}
	m := Material{Known: true, Opacity: alpha, Group: group}
	for i := range m.Faces {
		m.Faces[i], m.Tint[i] = index, tint
	}
	switch id {
	case 8:
		m.Faces[2], m.Faces[3], m.Tint[3] = 2, 0, [3]uint8{115, 203, 75}
		if data != 0 {
			m.Faces[0], m.Faces[1], m.Faces[4], m.Faces[5] = 68, 68, 68, 68
		}
	case 9, 10, 11, 255, 262:
		axis := [...]int{1, 2, 0, 0}[data&3]
		m.Faces[axis*2], m.Faces[axis*2+1] = slot[1], slot[1]
	case 17:
		m.Faces[2] = 4
	}
	return m
}
