// Package server 管理存档会话与 HTTP；核心资源、来源和特殊对象处理器由应用组装。
package server

import (
	"container/list"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"neomega-converter-core/structure"
	"neomega-converter-core/worldinput"
	r2 "neomega-r2"
	"neomega-r2link"
	"neomega-r2link/blockactor"
	"neomega-r2link/effector"
	"neomega-r2link/object"
	"neomega-view-utils/r2assets"
	"neomega-viewer-r2/internal/filebrowse"
	"neomega-viewer-r2/internal/mobius"
	"neomega-viewer-r2/internal/snapshot"
	"neomega-viewer-r2/internal/worldsource"
	"net/http"
	"strconv"
	"strings"
	"sync"

	"github.com/coder/websocket"
)

// Info 描述当前可加载的来源；Loaded 同时适用于存档与内置场景。
type Info struct {
	worldinput.Info
	Scene              *mobius.Scene `json:"scene,omitempty"`
	ID                 uint64        `json:"id"`
	Loaded             bool          `json:"loaded"`
	MinY               int           `json:"minY"`
	Height             int           `json:"height"`
	BedrockFix         bool          `json:"bedrockFix"`
	StructureMemoryMiB int           `json:"structureMemoryMiB"`
	StructureWorkers   int           `json:"structureWorkers"`
	StructureRoot      string        `json:"structureRoot,omitempty"`
	TextureName        string        `json:"textureName,omitempty"`
	SCTexturePath      string        `json:"scTexturePath,omitempty"`
	SCTextureUpdate    bool          `json:"scTextureUpdate,omitempty"`
	SCModPaths         []string      `json:"survivalcraftMods,omitempty"`
	SCModNames         []string      `json:"scModNames,omitempty"`
	SCModBlocks        int           `json:"scModBlocks,omitempty"`
}

// OpenOptions 让网页只覆盖用户填写的选项，其余使用服务启动时的默认值。
type OpenOptions struct {
	Path               string                   `json:"path"`
	Dimension          string                   `json:"dimension"`
	BedrockFix         *bool                    `json:"bedrockFix"`
	StructureMemoryMiB int                      `json:"structureMemoryMiB"`
	StructureWorkers   *int                     `json:"structureWorkers"`
	StructureRoot      string                   `json:"structureRoot"`
	SurvivalcraftMods  []string                 `json:"survivalcraftMods"`
	Progress           func(structure.Progress) `json:"-"`
}

// worldSource 只要求会话真正读取的内容，使内置场景与存档共享缓存和 R2Link 路径。
type worldSource interface {
	Region(context.Context, int, int) (r2link.R2Region, error)
	Chunks(context.Context) (r2link.ChunkIndex, error)
	Objects(context.Context, int, int, [3]int, int, effector.Registry, blockactor.Registry) ([]object.Descriptor, error)
	Close() error
}

// cacheEntry 将区域与 LRU 位置关联，限制每个会话的缓存容量。
type cacheEntry struct {
	key    [2]int
	region r2link.R2Region
	bytes  int
}

// session 隔离来源与缓存；切换世界时取消 ctx，连接到旧世界的浏览器随之断开。
type session struct {
	info   Info
	source worldSource
	ctx    context.Context
	cancel context.CancelFunc
	mu     sync.Mutex
	cache  map[[2]int]*list.Element
	order  *list.List
	bytes  int
	// 占用表单独加锁，扫描大存档时不阻塞区域缓存。
	chunksMu sync.Mutex
	chunks   r2link.ChunkIndex
	scanned  bool
}

// newSession 建立带独立取消的会话。
func newSession(info Info, source worldSource) *session {
	ctx, cancel := context.WithCancel(context.Background())
	return &session{info: info, source: source, ctx: ctx, cancel: cancel, cache: map[[2]int]*list.Element{}, order: list.New()}
}

// Server 串行切换来源，并限制区域读取的并发量。
type Server struct {
	// DesktopHost 允许内置 Electron 页面连接本机世界。
	DesktopHost bool
	Options     worldsource.Options
	mu          sync.RWMutex
	openMu      sync.Mutex
	current     *session
	builtin     *mobius.World
	revision    uint64
	workers     chan struct{}
	Effects     effector.Registry
	Actors      blockactor.Registry
}

// New 接收应用启用的特效与方块实体处理器。
func New(effects effector.Registry, actors blockactor.Registry) *Server {
	s := &Server{
		Options: worldsource.Options{Options: worldinput.Options{Structure: structure.Options{MemoryMiB: 512}}},
		builtin: mobius.New(),
		workers: make(chan struct{}, 4),
		Effects: effects,
		Actors:  actors,
	}
	s.Close()
	return s
}

// Open 先准备新来源，成功后等待旧读取完成再交换，打开失败时保留当前世界。
func (s *Server) Open(ctx context.Context, request OpenOptions) (Info, error) {
	s.openMu.Lock()
	defer s.openMu.Unlock()
	options := s.Options
	options.Path = request.Path
	options.Dimension = request.Dimension
	options.StructureRoot = request.StructureRoot
	if request.SurvivalcraftMods != nil {
		options.SurvivalcraftMods = request.SurvivalcraftMods
	}
	options.Structure.Progress = request.Progress
	if request.BedrockFix != nil {
		options.SkipBedrockFix = !*request.BedrockFix
	}
	if request.StructureMemoryMiB != 0 {
		options.Structure.MemoryMiB = request.StructureMemoryMiB
	}
	if request.StructureWorkers != nil {
		options.Structure.Workers = *request.StructureWorkers
	}
	source, err := worldsource.Open(ctx, options)
	if err != nil {
		return Info{}, err
	}
	if err := ctx.Err(); err != nil {
		source.Close()
		return Info{}, err
	}
	// 先断开旧世界的连接，让在途读取尽快结束，交换时不必久等。
	s.current.cancel()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.revision++
	info := Info{
		Info:          source.Info,
		TextureName:   source.TextureName,
		SCTexturePath: source.SCTexturePath,
		SCModPaths:    source.SCModPaths, SCModNames: source.SCModNames, SCModBlocks: source.SCModBlocks,
		ID:                 s.revision,
		Loaded:             true,
		MinY:               -64,
		Height:             512,
		BedrockFix:         !options.SkipBedrockFix,
		StructureMemoryMiB: options.Structure.MemoryMiB,
		StructureWorkers:   options.Structure.Workers,
		StructureRoot:      options.StructureRoot,
	}
	old := s.current
	s.current = newSession(info, source)
	if old != nil {
		old.source.Close()
	}
	return info, nil
}

// Info 让浏览器依据同一会话描述建立流式来源和开场取景。
func (s *Server) Info() Info {
	// 等待进行中的世界切换提交，旧连接断开时才能读到新世界。
	s.openMu.Lock()
	defer s.openMu.Unlock()
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.infoLocked()
}

// infoLocked 在同一锁内取得世界与诊断。
func (s *Server) infoLocked() Info {
	info := s.current.info
	if info.Scene != nil {
		info.BedrockFix = !s.Options.SkipBedrockFix
		info.StructureMemoryMiB = s.Options.Structure.MemoryMiB
		info.StructureWorkers = s.Options.Structure.Workers
	}
	return info
}

// Close 关闭当前存档并建立新的 Möbius 会话，旧会话请求随即失效。
func (s *Server) Close() {
	s.openMu.Lock()
	defer s.openMu.Unlock()
	if s.current != nil {
		s.current.cancel()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.current != nil {
		s.current.source.Close()
	}
	s.revision++
	scene := mobius.Opening()
	s.current = newSession(
		Info{
			ID:                 s.revision,
			Loaded:             true,
			Info:               worldinput.Info{Name: "Möbius", Position: scene.Camera.Position},
			MinY:               -64,
			Height:             512,
			BedrockFix:         !s.Options.SkipBedrockFix,
			StructureMemoryMiB: s.Options.Structure.MemoryMiB,
			StructureWorkers:   s.Options.Structure.Workers,
			Scene:              scene,
		},
		s.builtin,
	)
}
func writeJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(value)
}
func queryInt(r *http.Request, name string) (int, error) {
	return strconv.Atoi(r.URL.Query().Get(name))
}

// Handler 让内置场景与文件来源使用相同接口，并提供公共渲染资源。
func (s *Server) Handler(web fs.FS) http.Handler {
	mux := http.NewServeMux()
	core, _ := fs.Sub(r2.Files, "dist")
	mux.Handle("/r2/assets/", http.StripPrefix("/r2/assets/", assetHandler()))
	mux.Handle("/r2/", http.StripPrefix("/r2/", http.FileServerFS(core)))
	mux.HandleFunc("GET /api/export/template", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		io.WriteString(w, snapshot.Template())
	})
	mux.HandleFunc("GET /api/world", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, s.Info()) })
	mux.HandleFunc("POST /api/world/open", func(w http.ResponseWriter, r *http.Request) {
		var request OpenOptions
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&request); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		streaming := strings.Contains(r.Header.Get("Accept"), "application/x-ndjson")
		encoder := json.NewEncoder(w)
		if streaming {
			w.Header().Set("Content-Type", "application/x-ndjson")
			request.Progress = func(progress structure.Progress) {
				encoder.Encode(struct {
					Type     string             `json:"type"`
					Progress structure.Progress `json:"progress"`
				}{Type: "progress", Progress: progress})
				http.NewResponseController(w).Flush()
			}
		}
		info, err := s.Open(r.Context(), request)
		if streaming {
			if err != nil {
				encoder.Encode(map[string]any{"type": "error", "message": err.Error()})
			} else {
				encoder.Encode(map[string]any{"type": "world", "world": info})
			}
			return
		}
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		writeJSON(w, info)
	})
	mux.HandleFunc("POST /api/world/close", func(w http.ResponseWriter, r *http.Request) { s.Close(); writeJSON(w, s.Info()) })
	mux.HandleFunc("GET /api/files", func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query()
		var listing filebrowse.Listing
		var err error
		if query.Get("kind") == "sc-texture" {
			listing, err = filebrowse.ListSCTextures(query.Get("path"))
		} else {
			listing, err = filebrowse.List(query.Get("path"), query.Get("kind") == "pack")
		}
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		writeJSON(w, listing)
	})
	mux.HandleFunc("POST /api/world/sc-texture", s.handleSCTexture)
	mux.HandleFunc("GET /api/world/textures", func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseUint(r.URL.Query().Get("world"), 10, 64)
		if err != nil {
			http.Error(w, "无效的存档编号", http.StatusBadRequest)
			return
		}
		s.mu.RLock()
		current := s.current
		s.mu.RUnlock()
		if current.info.ID != id {
			http.Error(w, "存档已经切换", http.StatusConflict)
			return
		}
		source, ok := current.source.(*worldsource.World)
		if !ok || source.Textures == nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		writeTextureSet(w, r, source.Textures)
	})
	mux.HandleFunc("POST /api/textures", func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Path    string `json:"path"`
			MaxSize int    `json:"maxSize"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		textures, err := r2assets.BuildTextures(request.Path, request.MaxSize)
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		writeTextureSet(w, r, textures)
	})
	mux.HandleFunc("GET /r2link", s.link)
	mux.Handle("/", http.FileServerFS(web))
	return mux
}

// link 把当前世界作为 R2Link 数据源提供给同源页面；world 必须是当前会话编号，切换世界后旧连接被断开。
func (s *Server) link(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseUint(r.URL.Query().Get("world"), 10, 64)
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	s.mu.RLock()
	current := s.current
	s.mu.RUnlock()
	if current.info.ID != id {
		http.Error(w, "存档已经切换", 409)
		return
	}
	options := &websocket.AcceptOptions{}
	if s.DesktopHost {
		options.OriginPatterns = []string{"r2://app"}
	}
	conn, err := r2link.Accept(w, r, options)
	if err != nil {
		return
	}
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	stop := context.AfterFunc(current.ctx, cancel)
	defer stop()
	r2link.Serve(ctx, conn, &linkSource{server: s, session: current}, r2link.Options{
		Scene:   r2link.Scene{Name: current.info.Name},
		Workers: cap(s.workers),
	})
}

// linkSource 从会话读取，所有连接共用读取名额；读取期间持有读锁，交换世界前等待它结束。
type linkSource struct {
	server  *Server
	session *session
}

// read 取得读取名额并确认会话仍是当前世界。
func (l *linkSource) read(ctx context.Context, build func() error) error {
	s := l.server
	select {
	case s.workers <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	defer func() { <-s.workers }()
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.current != l.session {
		return fmt.Errorf("存档已经切换")
	}
	return build()
}

// Region 使用会话内的 LRU 缓存，同一区域重复订阅时不再读盘。
func (l *linkSource) Region(ctx context.Context, x, z int) (region r2link.R2Region, err error) {
	current, key := l.session, [2]int{x, z}
	err = l.read(ctx, func() error {
		current.mu.Lock()
		if element := current.cache[key]; element != nil {
			current.order.MoveToFront(element)
			region = element.Value.(cacheEntry).region
			current.mu.Unlock()
			return nil
		}
		current.mu.Unlock()
		region, err = current.source.Region(ctx, x, z)
		if err != nil {
			return err
		}
		size := 0
		for _, section := range region.Sections {
			size += 24 + 4*(len(section.Palette)+len(section.Words))
		}
		current.mu.Lock()
		defer current.mu.Unlock()
		if _, exists := current.cache[key]; !exists {
			current.cache[key] = current.order.PushFront(cacheEntry{key: key, region: region, bytes: size})
			current.bytes += size
		}
		for current.bytes > 64*1024*1024 && current.order.Len() > 1 {
			tail := current.order.Back()
			entry := tail.Value.(cacheEntry)
			delete(current.cache, entry.key)
			current.order.Remove(tail)
			current.bytes -= entry.bytes
		}
		return nil
	})
	return region, err
}

// Objects 调用应用注册的特效与方块实体处理器。
func (l *linkSource) Objects(ctx context.Context, x, z int, center [3]int, radius int) (objects []object.Descriptor, err error) {
	if radius < 1 || radius > 16 {
		return nil, fmt.Errorf("近景视距应为 1～16 区块")
	}
	err = l.read(ctx, func() error {
		objects, err = l.session.source.Objects(ctx, x, z, center, radius, l.server.Effects, l.server.Actors)
		return err
	})
	return objects, err
}

// Chunks 的会话缓存独立于区域 LRU，地图在同一会话内只需扫描一次占用表。
func (l *linkSource) Chunks(ctx context.Context) (index r2link.ChunkIndex, err error) {
	current := l.session
	err = l.read(ctx, func() error {
		current.chunksMu.Lock()
		defer current.chunksMu.Unlock()
		if !current.scanned {
			if current.chunks, err = current.source.Chunks(ctx); err != nil {
				return err
			}
			current.scanned = true
		}
		index = current.chunks
		return nil
	})
	return index, err
}
