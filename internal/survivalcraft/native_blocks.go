package survivalcraft

import "fmt"

// Survivalcraft 2.4 block IDs and default tiles. Verified against the
// SurvivalcraftApi block classes and BlocksData.txt at 98e5f58f. Tile numbers
// refer to the Survivalcraft atlas (including a world's own .scbtex).
// A shape here selects geometry only; no Minecraft textures are used.
type nativeBlockSpec struct {
	tile  uint8
	shape string
}

var nativeBlocks = map[uint32]nativeBlockSpec{
	41: {32, ""}, 45: {25, ""}, 56: {4, ""}, 57: {82, ""}, 58: {80, ""},
	63: {132, "spruce_sapling"}, 71: {179, ""}, 86: {4, "oak_pressure_plate"},
	100: {35, ""}, 101: {36, ""}, 104: {123, "fire"},
	105: {4, ""}, 106: {4, ""}, 107: {4, ""},
	119: {56, "oak_sapling"}, 120: {63, ""}, 121: {63, ""},
	131: {102, ""}, 132: {102, ""},
	133: {79, ""}, 134: {78, ""}, 135: {78, ""}, 137: {78, ""},
	138: {78, "heavy_weighted_pressure_plate"}, 139: {78, ""}, 140: {78, ""}, 141: {78, ""},
	143: {78, ""}, 144: {4, ""}, 145: {78, ""}, 146: {78, ""}, 147: {183, ""},
	148: {49, ""}, 151: {78, ""}, 152: {78, ""},
	153: {4, ""}, 154: {1, ""}, 155: {78, ""}, 156: {78, ""}, 157: {78, ""},
	167: {63, ""}, 173: {74, "wheat"}, 174: {94, "wheat"},
	179: {78, ""}, 180: {78, ""}, 181: {78, ""}, 182: {78, ""},
	183: {78, ""}, 184: {78, ""}, 185: {78, ""}, 186: {78, ""},
	187: {78, ""}, 188: {78, ""}, 189: {1, "cobblestone_wall"},
	197: {10, ""}, 199: {128, ""}, 204: {30, "wheat"}, 209: {62, ""},
	216: {42, ""}, 223: {70, ""}, 224: {78, ""},
	226: {63, "dead_bush"}, 229: {63, "dead_bush"},
	231: {78, ""}, // 原版锗块 / SemiconductorBlock
	232: {104, "kelp"}, 233: {105, "seagrass"},
	234: {4, ""}, 235: {4, ""}, 236: {4, ""},
	237: {155, ""}, 238: {155, ""}, 243: {16, ""}, 244: {160, ""},
	253: {78, ""}, 254: {78, ""}, 258: {63, ""}, 261: {108, ""},
}

var r2Face = [...]int{5, 1, 4, 0, 3, 2} // SC: +Z, +X, -Z, -X, +Y, -Y.
var sixDirections = [...]string{"south", "east", "north", "west", "up", "down"}

func mountedButton(name string, dir uint32, pressed bool) string {
	side := "wall"
	switch dir {
	case 4:
		side = "floor"
	case 5:
		side = "ceiling"
	}
	return props(name, fmt.Sprintf(`"face"=%q,"facing"=%q,"powered"="%t"`, side, face[dir&3], pressed))
}

func nativeState(id, data uint32, spec nativeBlockSpec) string {
	switch id {
	case 56, 57, 58:
		name := "iron_door"
		if id == 56 {
			name = "oak_door"
		}
		// SC decides upper/lower from neighbors. Both halves of the
		// stock door have the same geometry and get the same native tile.
		return props(name, fmt.Sprintf(`"facing"=%q,"half"="lower","hinge"=%q,"open"=%q,"powered"="false"`,
			face[data&3], flag(data, 8, "left", "right"), flag(data, 4, "true", "false")))
	case 63, 119:
		return props(spec.shape, `"stage"="0"`)
	case 138:
		return props(spec.shape, `"power"="0"`)
	case 120, 121, 139, 147, 151, 152, 179, 182, 185, 253, 254:
		return mountedButton("stone_button", data&7, false)
	case 134, 135, 137, 140, 143, 145, 146, 156, 157, 180, 181, 183, 184, 186, 187, 188, 224:
		return mountedButton("stone_button", data>>2&7, false)
	case 141:
		return mountedButton("lever", data>>1&7, data&1 != 0)
	case 133:
		// Draw a thin connection preview, not energized circuit logic.
		// The game stores attached faces; an isolated wall wire uses a
		// vertical plane instead of a false solid cube.
		if mask := data & 63; mask == 1 || mask == 2 || mask == 4 || mask == 8 {
			for i := range 4 {
				if mask == 1<<i {
					return vine(uint32(i))
				}
			}
		}
		return props("redstone_wire", `"east"="side","north"="side","power"="0","south"="side","west"="side"`)
	case 144:
		name := flag(data, 1, "stone_pressure_plate", "oak_pressure_plate")
		return props(name, `"powered"="false"`)
	case 173, 174, 204:
		return props("wheat", fmt.Sprintf(`"age"="%d"`, data&7))
	case 189:
		return fence("cobblestone", 0, true)
	case 197:
		return vine(data & 3)
	case 209:
		return props("campfire", `"facing"="north","lit"="false","signal_fire"="false","waterlogged"="false"`)
	case 237, 238:
		dir := sixDirections[min(data>>3&7, 5)]
		if id == 238 {
			return props("piston_head", fmt.Sprintf(`"facing"=%q,"short"="false","type"="normal"`, dir))
		}
		return props("piston", fmt.Sprintf(`"extended"=%q,"facing"=%q`, flag(data, 1, "true", "false"), dir))
	case 258, 261:
		return props("snow", `"layers"="1"`)
	case 86:
		return props(spec.shape, `"powered"="false"`)
	case 104:
		return props(spec.shape, `"age"="0","east"="false","north"="false","south"="false","up"="false","west"="false"`)
	case 232:
		return props(spec.shape, `"age"="0"`)
	}
	if spec.shape != "" {
		return state(spec.shape)
	}
	return state("stone") // full cube geometry; the SC material defines its appearance.
}

func vine(dir uint32) string {
	// A vine state describes the support face, not the outward plane.
	dir = (dir + 2) & 3
	return props("vine", fmt.Sprintf(`"east"="%t","north"="%t","south"="%t","up"="false","west"="%t"`,
		dir == 1, dir == 2, dir == 0, dir == 3))
}

func (w *World) nativeMaterial(id, data uint32, spec nativeBlockSpec) Material {
	tile, tint := spec.tile, white
	switch id {
	case 119:
		tile = [...]uint8{56, 72, 73, 73, 72, 110, 56, 56}[data&7]
	case 133:
		// Wire geometry uses the game atlas's plain texel tinted copper.
		tile, tint = 15, [3]uint8{146, 81, 42}
		if data&64 != 0 {
			tint = w.palette[(data>>7)&15]
		}
	case 139:
		tile, tint = 67, [3]uint8{200, 171, 134}
		if data>>3&15 != 0 {
			tint = [3]uint8{255, 237, 196}
		}
		if data&128 != 0 {
			tint = w.palette[data>>8&15]
		}
	case 144:
		if data&1 != 0 {
			tile = 1
		}
	case 174:
		size := uint8(data & 7)
		tile = 88 + size
		if data&8 != 0 {
			tile = 86
			if size > 2 {
				tile = 87
			}
		}
	case 197, 232, 233:
		tint = [3]uint8{106, 171, 85}
	case 204:
		tile = [...]uint8{11, 29, 30, 30}[data&3]
	case 226:
		tint = [3]uint8{48, 30, 39}
	case 229:
		tint = [3]uint8{183, 94, 60}
	}
	m := Material{Known: true, Opacity: 255}
	for i := range m.Faces {
		m.Faces[i], m.Tint[i] = tile, tint
	}
	switch id {
	case 45: // chest: metal top/bottom, clasp, back, sides
		m.Faces[2], m.Faces[3] = 42, 42
		m.Faces[r2Face[data&3]], m.Faces[r2Face[(data+2)&3]] = 27, 26
	case 153, 154, 155, 223, 243:
		axis := [...]int{2, 0, 1, 1}[data&3]
		var wired uint8
		switch id {
		case 153:
			wired = 168
		case 154:
			wired = 184
		case 155:
			wired = 216
		case 223:
			wired = 152
		case 243:
			wired = 136
		}
		m.Faces[2*axis], m.Faces[2*axis+1] = wired, wired
	case 216: // dispenser
		if dir := data & 7; dir < 6 {
			m.Faces[r2Face[dir]] = 59
		}
	case 237, 238:
		if dir := data >> 3 & 7; dir < 6 {
			f := r2Face[dir]
			m.Faces[f], m.Faces[f^1] = 170+16*uint8(min(data>>1&3, 2)), 171
		}
	}
	return m
}
