package survivalcraft

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"
)

// Some exports store GameProject as objects of named dictionaries and
// [type, value] pairs rather than XML Values/Value elements. Only normalize
// metadata used by the viewer; game logic and inventories are not imported.
func decodeProjectJSON(raw []byte) (project, error) {
	var p project
	var src struct {
		Version    json.RawMessage
		Subsystems map[string]json.RawMessage
		Entities   map[string]json.RawMessage
	}
	if err := json.Unmarshal(bytes.TrimPrefix(raw, []byte{0xef, 0xbb, 0xbf}), &src); err != nil {
		return p, err
	}
	p.Version, _ = jsonProjectValue(src.Version)
	if strings.TrimSpace(p.Version) == "" || src.Subsystems == nil {
		return p, fmt.Errorf("缺少有效的 Version 或 Subsystems")
	}
	for _, name := range []string{"GameInfo", "Players", "BlocksManager", "FurnitureBlockBehavior"} {
		if data, ok := src.Subsystems[name]; ok {
			group, err := jsonProjectGroup(name, data, 0)
			if err != nil {
				return p, err
			}
			p.Subsystems = append(p.Subsystems, group)
		}
	}
	for _, key := range slices.Sorted(maps.Keys(src.Entities)) {
		var entity struct {
			Name      json.RawMessage
			Overrides map[string]json.RawMessage
		}
		if err := json.Unmarshal(src.Entities[key], &entity); err != nil {
			return p, fmt.Errorf("Entities.%s: %w", key, err)
		}
		name, _ := jsonProjectValue(entity.Name)
		if !strings.HasSuffix(name, "Player") && !strings.HasPrefix(name, "Player") {
			continue
		}
		if body, ok := entity.Overrides["Body"]; ok {
			group, err := jsonProjectGroup("Body", body, 0)
			if err != nil {
				return p, err
			}
			p.Entities = append(p.Entities, projectEntity{Name: name, Groups: []values{group}})
		}
	}
	return p, nil
}

func jsonProjectGroup(name string, data json.RawMessage, depth int) (values, error) {
	out := values{Name: name}
	if depth > 16 {
		return out, fmt.Errorf("元数据嵌套过深: %s", name)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return out, fmt.Errorf("%s: %w", name, err)
	}
	for _, key := range slices.Sorted(maps.Keys(fields)) {
		raw := bytes.TrimSpace(fields[key])
		if len(raw) != 0 && raw[0] == '{' {
			group, err := jsonProjectGroup(key, raw, depth+1)
			if err != nil {
				return out, err
			}
			out.Groups = append(out.Groups, group)
		} else if text, ok := jsonProjectValue(raw); ok {
			out.Fields = append(out.Fields, value{Name: key, Text: text})
		}
	}
	return out, nil
}

func jsonProjectValue(raw json.RawMessage) (string, bool) {
	if len(raw) == 0 || raw[0] != '[' {
		return "", false
	}
	var pair []json.RawMessage
	if json.Unmarshal(raw, &pair) != nil || len(pair) != 2 {
		return "", false
	}
	var tag string
	if json.Unmarshal(pair[0], &tag) != nil || tag == "" {
		return "", false
	}
	var text string
	switch pair[1][0] {
	case '"':
		if json.Unmarshal(pair[1], &text) != nil {
			return "", false
		}
	case '{', '[', 'n':
		return "", false
	default:
		text = string(pair[1]) // preserve integer and float text without rounding
	}
	return text, true
}
