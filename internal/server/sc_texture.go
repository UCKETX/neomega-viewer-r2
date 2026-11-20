package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"neomega-viewer-r2/internal/worldsource"
)

var errSCTextureStale = errors.New("存档已切换，请在当前地图重新选择材质")

// setSCTexture prepares a new immutable bundle before retiring the current
// session. Old renderer tables must never be paired with new texture geometry.
func (s *Server) setSCTexture(ctx context.Context, id uint64, path string) (Info, error) {
	s.openMu.Lock()
	defer s.openMu.Unlock()
	old := s.current
	if id != old.info.ID {
		return Info{}, errSCTextureStale
	}
	source, ok := old.source.(*worldsource.World)
	if !ok {
		return Info{}, errors.New("当前场景不是生存战争存档")
	}
	next, err := source.WithSurvivalcraftTexture(ctx, path)
	if err != nil {
		return Info{}, err
	}
	if err := ctx.Err(); err != nil {
		return Info{}, err
	}
	old.cancel()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.revision++
	info := old.info
	info.ID, info.TextureName, info.SCTexturePath = s.revision, next.TextureName, next.SCTexturePath
	info.SCTextureUpdate = true
	s.current = newSession(info, next)
	return info, nil
}

func (s *Server) handleSCTexture(w http.ResponseWriter, r *http.Request) {
	var request struct {
		World uint64  `json:"world"`
		Path  *string `json:"path"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<10)).Decode(&request); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if request.World == 0 || request.Path == nil {
		http.Error(w, "需要当前存档编号和材质路径（空路径表示恢复默认）", http.StatusBadRequest)
		return
	}
	info, err := s.setSCTexture(r.Context(), request.World, *request.Path)
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, errSCTextureStale) {
			status = http.StatusConflict
		}
		http.Error(w, err.Error(), status)
		return
	}
	writeJSON(w, info)
}
