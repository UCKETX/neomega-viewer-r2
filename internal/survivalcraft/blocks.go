package survivalcraft

import (
	"fmt"
	"strconv"
	"strings"
)

// Unknown blocks stay visible rather than making holes in a building.
const Unknown = "minecraft:magenta_concrete []"

var face = [...]string{"south", "east", "north", "west"}

// The SC palette is a tint over the original texture. Use the closest R2
// stock material; this is a preview, not a Minecraft world conversion.
var paint = [...]string{"white", "light_blue", "pink", "light_blue", "yellow", "lime", "orange", "light_gray", "gray", "cyan", "purple", "blue", "brown", "green", "red", "black"}
var woodPaint = [...]string{"birch", "warped", "cherry", "warped", "bamboo", "warped", "acacia", "pale_oak", "dark_oak", "warped", "crimson", "warped", "dark_oak", "warped", "mangrove", "dark_oak"}

var solid = map[uint32]string{
	0: "air", 1: "bedrock", 2: "dirt", 6: "gravel", 7: "sand",
	15: "glass", 16: "coal_ore", 17: "glowstone", 22: "coal_block",
	27: "crafting_table", 39: "iron_ore", 44: "glass", 46: "iron_block",
	47: "copper_block", 60: "glass", 62: "ice", 64: "furnace",
	65: "furnace", 66: "calcite", 87: "grass_block", 112: "diamond_ore",
	126: "diamond_block", 127: "cactus", 150: "coal_block", 168: "farmland",
	257: "air",
}

var cube = map[uint32]string{
	3: "granite", 4: "sandstone", 5: "cobblestone", 21: "oak_planks",
	26: "stone_bricks", 67: "basalt", 68: "quartz_block", 72: "clay", 73: "bricks",
}

var stairsBase = map[uint32]string{
	48: "cobblestone", 49: "oak", 50: "stone_brick", 51: "sandstone",
	69: "quartz", 76: "brick", 96: "polished_blackstone", 217: "stone",
	260: "mud_brick",
}
var slabsBase = map[uint32]string{
	52: "sandstone", 53: "cobblestone", 54: "stone_brick", 55: "oak",
	70: "quartz", 75: "brick", 95: "polished_blackstone", 136: "stone",
	259: "mud_brick",
}

func state(name string) string { return "minecraft:" + name + " []" }

func props(name, fields string) string { return "minecraft:" + name + " [" + fields + "]" }

func flag(value, bit uint32, yes, no string) string {
	if value&bit != 0 {
		return yes
	}
	return no
}

func shapedMaterial(base string, color uint32, colored bool) string {
	if !colored {
		return base
	}
	if base == "oak" {
		return woodPaint[color]
	}
	switch color {
	case 0:
		return "quartz"
	case 4, 6:
		return "sandstone"
	case 8, 9, 10, 11, 13:
		return "deepslate_tile"
	case 12:
		return "mud_brick"
	case 14:
		return "red_sandstone"
	case 15:
		return "polished_blackstone"
	}
	return base
}

func stairs(material string, data uint32) string {
	facing := face[(data+2)&3] // The high side is opposite the SC rotation.
	shape := "straight"
	switch (data >> 3) & 3 {
	case 1:
		shape = "outer_right"
	case 2:
		shape = "inner_right"
	}
	return props(material+"_stairs", fmt.Sprintf(`"facing"=%q,"half"=%q,"shape"=%q,"waterlogged"="false"`,
		facing, flag(data, 4, "top", "bottom"), shape))
}

func slab(material string, top bool) string {
	half := "bottom"
	if top {
		half = "top"
	}
	return props(material+"_slab", fmt.Sprintf(`"type"=%q,"waterlogged"="false"`, half))
}

func trapdoor(material string, rotation uint32, top, open string) string {
	return props(material+"_trapdoor", fmt.Sprintf(`"facing"=%q,"half"=%q,"open"=%q,"powered"="false","waterlogged"="false"`, face[rotation&3], top, open))
}

func fence(material string, mask uint32, wall bool) string {
	if wall {
		return props(material+"_wall", fmt.Sprintf(`"east"=%q,"north"=%q,"south"=%q,"up"="true","waterlogged"="false","west"=%q`,
			flag(mask, 1, "low", "none"), flag(mask, 8, "low", "none"), flag(mask, 4, "low", "none"), flag(mask, 2, "low", "none")))
	}
	suffix := "_fence"
	if material == "iron" {
		suffix = "_bars"
	}
	return props(material+suffix, fmt.Sprintf(`"east"=%q,"north"=%q,"south"=%q,"waterlogged"="false","west"=%q`,
		flag(mask, 1, "true", "false"), flag(mask, 8, "true", "false"), flag(mask, 4, "true", "false"), flag(mask, 2, "true", "false")))
}

// BlockState maps a decoded SC cell to a stock R2 Java state.
func (w *World) BlockState(word uint32) string {
	id, data := word&1023, word>>14
	if name, ok := w.modBlocks[id]; ok {
		// Identify these mod blocks from the archive, never by a dynamic ID.
		if color, ok := strings.CutSuffix(name, "GlassBlock"); ok {
			color := strings.ToLower(color)
			switch color {
			case "lightblue":
				color = "light_blue"
			case "lightgray":
				color = "light_gray"
			}
			switch color {
			case "white", "orange", "magenta", "light_blue", "yellow", "lime", "pink", "gray", "light_gray", "cyan", "purple", "blue", "brown", "green", "red", "black":
				return state(color + "_stained_glass")
			}
		}
		return Unknown
	}
	if spec, ok := nativeBlocks[id]; ok {
		return nativeState(id, data, spec)
	}
	if material, ok := cube[id]; ok {
		if data&1 != 0 {
			color := data >> 1 & 15
			if id == 21 {
				material = woodPaint[color] + "_planks"
			} else if id == 72 || color != 0 {
				material = paint[color] + "_terracotta"
			}
		}
		if material == "basalt" {
			return props(material, `"axis"="y"`)
		}
		return state(material)
	}
	if material, ok := stairsBase[id]; ok {
		return stairs(shapedMaterial(material, data>>6&15, data&32 != 0), data)
	}
	if material, ok := slabsBase[id]; ok {
		return slab(shapedMaterial(material, data>>2&15, data&2 != 0), data&1 != 0)
	}
	if name, ok := solid[id]; ok {
		return state(name)
	}
	switch id {
	case 190:
		// Treasure generator is an invisible world marker in the game.
		return state("air")
	case 8:
		return props("grass_block", `"snowy"="false"`)
	case 9, 10, 11, 255, 262:
		name := map[uint32]string{9: "oak", 10: "birch", 11: "spruce", 255: "acacia", 262: "birch"}[id]
		axis := [...]string{"y", "z", "x", "x"}[data&3]
		return props(name+"_log", fmt.Sprintf(`"axis"=%q`, axis))
	case 12, 13, 14, 225, 256, 263:
		name := map[uint32]string{12: "oak", 13: "birch", 14: "spruce", 225: "spruce", 256: "acacia", 263: "birch"}[id]
		return props(name+"_leaves", `"distance"="7","persistent"="true","waterlogged"="false"`)
	case 18, 92:
		name := "water"
		if id == 92 {
			name = "lava"
		}
		return props(name, fmt.Sprintf(`"level"="%d"`, data&15))
	case 19:
		return state("short_grass")
	case 20:
		return state("poppy")
	case 24:
		return state("allium")
	case 25:
		return state("oxeye_daisy")
	case 28, 99:
		return state("dead_bush")
	case 31:
		return state("torch")
	case 59, 213:
		return props("ladder", fmt.Sprintf(`"facing"=%q,"waterlogged"="false"`, face[data&3]))
	case 61:
		return props("snow", `"layers"="1"`)
	case 83, 84:
		material := "oak"
		if id == 84 {
			material = "iron"
		}
		return trapdoor(material, data, flag(data, 8, "top", "bottom"), flag(data, 4, "true", "false"))
	case 94, 164, 193, 202, 163:
		color := data >> 5 & 15
		switch id {
		case 94:
			return fence(shapedMaterial("oak", color, data&16 != 0), data, false)
		case 193:
			return fence("iron", data, false)
		case 202:
			return fence("cobblestone", data, true)
		case 163:
			return fence("polished_blackstone_brick", data, true)
		default:
			return fence("brick", data, true)
		}
	case 166, 194:
		material := shapedMaterial("oak", data>>5&15, data&16 != 0)
		if id == 194 {
			material = "dark_oak"
		}
		return props(material+"_fence_gate", fmt.Sprintf(`"facing"=%q,"in_wall"="false","open"=%q,"powered"="false"`,
			face[data&3], flag(data, 4, "true", "false")))
	case 97, 210:
		return props("oak_sign", fmt.Sprintf(`"rotation"="%d","waterlogged"="false"`, (data&7)*2))
	case 98, 211:
		return props("oak_wall_sign", fmt.Sprintf(`"facing"=%q,"waterlogged"="false"`, face[data&3]))
	case 142:
		side := "wall"
		if data&7 == 4 {
			side = "floor"
		}
		if data&7 == 5 {
			side = "ceiling"
		}
		return props("stone_button", fmt.Sprintf(`"face"=%q,"facing"=%q,"powered"="false"`, side, face[data&3]))
	case 208:
		return state(paint[data&15] + "_carpet")
	case 227:
		if design, ok := w.furniture[(data>>15&7)<<10|(data>>2&1023)]; ok {
			return w.furnitureState(design, data&3)
		}
	}
	return Unknown
}

type furnitureDesign struct {
	material   uint32
	resolution int
	low, high  [3]int
}

// Store only material and extent. This is adequate to approximate thin horizontal
// or vertical furniture with slabs/panels; arbitrary furniture meshes are not loaded.
func parseFurniture(group values) (d furnitureDesign, ok bool) {
	n, err := strconv.Atoi(group.field("Resolution"))
	if err != nil || n < 2 || n > 16 {
		return d, false
	}
	d.resolution = n
	d.low = [3]int{n, n, n}
	text := strings.TrimSuffix(group.field("Values"), ",")
	i := 0
	counts := map[uint32]int{}
	for _, run := range strings.Split(text, ",") {
		countText, valueText, ok := strings.Cut(run, "*")
		count, e1 := strconv.Atoi(countText)
		v, e2 := strconv.ParseInt(valueText, 10, 32)
		if !ok || e1 != nil || e2 != nil || count <= 0 || count > n*n*n-i {
			return d, false
		}
		word := uint32(v)
		if id := word & 1023; id != 0 && id != 227 {
			word &^= 15 << 10
			counts[word] += count
			if counts[word] > counts[d.material] {
				d.material = word
			}
			for j := i; j < i+count; j++ {
				for axis, p := range [3]int{j % n, j / n % n, j / (n * n)} {
					d.low[axis] = min(d.low[axis], p)
					d.high[axis] = max(d.high[axis], p)
				}
			}
		}
		i += count
	}
	return d, i == n*n*n && d.material != 0
}

func (w *World) furnitureState(d furnitureDesign, rotation uint32) string {
	id, data := d.material&1023, d.material>>14
	material := "iron"
	if id == 21 {
		material = shapedMaterial("oak", data>>1&15, data&1 != 0)
	}
	n := d.resolution
	if (d.high[1]-d.low[1]+1)*2 <= n {
		base := "stone"
		if id == 21 {
			base = material
		}
		if id == 68 {
			base = shapedMaterial("quartz", data>>1&15, data&1 != 0)
		}
		return slab(base, d.low[1]*2 >= n)
	}
	if (d.high[0]-d.low[0]+1)*3 <= n {
		facing := uint32(1)
		if d.low[0]*2 >= n {
			facing = 3
		}
		return trapdoor(material, facing+rotation, "bottom", "true")
	}
	if (d.high[2]-d.low[2]+1)*3 <= n {
		facing := uint32(0)
		if d.low[2]*2 >= n {
			facing = 2
		}
		return trapdoor(material, facing+rotation, "bottom", "true")
	}
	return w.BlockState(d.material)
}
