package httpui

import (
	"AgenticService/src/desktop/folderpicker"
	"AgenticService/src/desktop/supervisor"
	"AgenticService/src/domain"
	"AgenticService/src/internal/systeminfo"
	"AgenticService/src/web/console"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const controlCookie = "agentic_desktop_control"

// 存檔請求的上限。base64 會把內容放大約三分之一，因此請求上限比內容上限寬。
// 診斷包與安全備份是這裡最大的兩種檔案。
const (
	maxSaveFileBytes        = 64 << 20
	maxSaveFileRequestBytes = 96 << 20
)

type Config struct {
	Supervisor   *supervisor.Supervisor
	BackendToken string
	ControlToken string
	RestoreUI    func()
}

type Server struct {
	supervisor   *supervisor.Supervisor
	backendToken string
	controlToken string
	restoreUI    func()
	static       http.Handler
	proxy        http.Handler
	mux          *http.ServeMux
}

func New(config Config) (*Server, error) {
	if config.Supervisor == nil {
		return nil, fmt.Errorf("desktop supervisor is required")
	}
	if strings.TrimSpace(config.ControlToken) == "" {
		config.ControlToken = domain.NewID("desktop")
	}
	assets, err := console.Assets()
	if err != nil {
		return nil, err
	}
	target, err := url.Parse(config.Supervisor.BackendURL())
	if err != nil {
		return nil, err
	}
	server := &Server{
		supervisor:   config.Supervisor,
		backendToken: strings.TrimSpace(config.BackendToken),
		controlToken: config.ControlToken,
		restoreUI:    config.RestoreUI,
		static:       http.FileServer(http.FS(assets)),
		mux:          http.NewServeMux(),
	}
	server.proxy = server.newProxy(target)
	server.routes()
	return server, nil
}

func (s *Server) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	if strings.HasPrefix(request.URL.Path, "/desktop/api/") && !s.authorized(request) {
		writeJSON(writer, http.StatusForbidden, map[string]any{"error": "desktop control token is invalid"})
		return
	}
	if !strings.HasPrefix(request.URL.Path, "/backend/") {
		http.SetCookie(writer, &http.Cookie{
			Name:     controlCookie,
			Value:    s.controlToken,
			Path:     "/",
			HttpOnly: true,
			SameSite: http.SameSiteStrictMode,
		})
	}
	s.mux.ServeHTTP(writer, request)
}

func (s *Server) routes() {
	s.mux.HandleFunc("GET /.well-known/nr-intern-desktop", s.desktopIdentity)
	s.mux.HandleFunc("POST /desktop/restore", s.restoreDesktop)
	s.mux.HandleFunc("GET /desktop/api/status", s.status)
	s.mux.HandleFunc("GET /desktop/api/system-info", s.systemInfo)
	s.mux.HandleFunc("GET /desktop/api/logs", s.logs)
	s.mux.HandleFunc("POST /desktop/api/start", s.start)
	s.mux.HandleFunc("POST /desktop/api/stop", s.stop)
	s.mux.HandleFunc("POST /desktop/api/restart", s.restart)
	s.mux.HandleFunc("POST /desktop/api/folders/pick", s.pickFolders)
	s.mux.HandleFunc("POST /desktop/api/folders/dropped", s.droppedFolders)
	s.mux.HandleFunc("POST /desktop/api/mcp/files/dropped", s.droppedMCPFiles)
	s.mux.HandleFunc("POST /desktop/api/files/save", s.saveFile)
	s.mux.HandleFunc("POST /desktop/api/screen-capture", s.captureScreen)
	s.mux.HandleFunc("POST /desktop/api/clipboard/image", s.copyImageToClipboard)
	s.mux.HandleFunc("POST /desktop/api/resources/open", s.openResource)
	s.mux.Handle("/backend/", s.proxy)
	s.mux.HandleFunc("/", s.serveStatic)
}

func (s *Server) desktopIdentity(writer http.ResponseWriter, _ *http.Request) {
	writer.Header().Set("X-NR-Intern-Desktop", "1")
	writeData(writer, map[string]any{"service": "nr-intern-desktop", "version": 1})
}

func (s *Server) restoreDesktop(writer http.ResponseWriter, _ *http.Request) {
	if s.restoreUI == nil {
		writeJSON(writer, http.StatusServiceUnavailable, map[string]any{"error": "native UI restore is unavailable"})
		return
	}
	s.restoreUI()
	writeJSON(writer, http.StatusAccepted, map[string]any{"status": "restoring"})
}

func (s *Server) status(writer http.ResponseWriter, request *http.Request) {
	writeData(writer, s.supervisor.Status(request.Context()))
}

func (s *Server) systemInfo(writer http.ResponseWriter, _ *http.Request) {
	totalMemoryBytes, err := systeminfo.TotalMemoryBytes()
	if err != nil {
		writeJSON(writer, http.StatusNotImplemented, map[string]any{"error": err.Error()})
		return
	}
	writeData(writer, map[string]any{"total_memory_bytes": totalMemoryBytes})
}

func (s *Server) logs(writer http.ResponseWriter, _ *http.Request) {
	writeData(writer, map[string]any{"text": s.supervisor.Logs()})
}

func (s *Server) start(writer http.ResponseWriter, request *http.Request) {
	ctx, cancel := contextWithTimeout(request, 25*time.Second)
	defer cancel()
	if err := s.supervisor.Start(ctx); err != nil {
		writeJSON(writer, http.StatusConflict, map[string]any{"error": err.Error()})
		return
	}
	writeData(writer, s.supervisor.Status(request.Context()))
}

func (s *Server) stop(writer http.ResponseWriter, request *http.Request) {
	ctx, cancel := contextWithTimeout(request, 15*time.Second)
	defer cancel()
	if err := s.supervisor.Stop(ctx); err != nil {
		writeJSON(writer, http.StatusConflict, map[string]any{"error": err.Error()})
		return
	}
	writeData(writer, s.supervisor.Status(request.Context()))
}

func (s *Server) restart(writer http.ResponseWriter, request *http.Request) {
	ctx, cancel := contextWithTimeout(request, 35*time.Second)
	defer cancel()
	if err := s.supervisor.Restart(ctx); err != nil {
		writeJSON(writer, http.StatusConflict, map[string]any{"error": err.Error()})
		return
	}
	writeData(writer, s.supervisor.Status(request.Context()))
}

func (s *Server) pickFolders(writer http.ResponseWriter, request *http.Request) {
	ctx, cancel := contextWithTimeout(request, 10*time.Minute)
	defer cancel()
	values, err := folderpicker.Pick(ctx)
	if errors.Is(err, folderpicker.ErrCanceled) {
		writer.WriteHeader(http.StatusNoContent)
		return
	}
	if errors.Is(err, folderpicker.ErrUnavailable) {
		writeJSON(writer, http.StatusNotImplemented, map[string]any{"error": err.Error()})
		return
	}
	if err != nil {
		writeJSON(writer, http.StatusInternalServerError, map[string]any{"error": "select folders: " + err.Error()})
		return
	}
	writeData(writer, values)
}

func (s *Server) droppedFolders(writer http.ResponseWriter, request *http.Request) {
	values, err := folderpicker.Dropped(request.Context())
	if errors.Is(err, folderpicker.ErrUnavailable) {
		writeJSON(writer, http.StatusNotImplemented, map[string]any{"error": err.Error()})
		return
	}
	if err != nil {
		writeJSON(writer, http.StatusInternalServerError, map[string]any{"error": "read dropped folders: " + err.Error()})
		return
	}
	if len(values) == 0 {
		writer.WriteHeader(http.StatusNoContent)
		return
	}
	writeData(writer, values)
}

// saveFile 以原生存檔面板詢問位置後把內容寫進去。
//
// 桌面端的 WebView 沒有下載處理，網頁的 <a download> 會被靜默丟掉——按了按鈕
// 什麼都不會發生。瀏覽器不會走到這裡，那邊的下載本來就正常。
//
// 路徑由使用者在系統面板裡選定，後端不自行決定要寫到哪；面板回傳什麼就寫什麼，
// 這是唯一能讓「存到哪」保持在使用者手上的作法。
func (s *Server) saveFile(writer http.ResponseWriter, request *http.Request) {
	var input struct {
		Name          string `json:"name"`
		ContentBase64 string `json:"content_base64"`
	}
	if err := json.NewDecoder(io.LimitReader(request.Body, maxSaveFileRequestBytes)).Decode(&input); err != nil {
		writeJSON(writer, http.StatusBadRequest, map[string]any{"error": "invalid save request: " + err.Error()})
		return
	}
	// 名稱會當成原生面板的預設檔名，而它來自使用者自訂的 Provider／MCP id。
	// "." 與 ".." 通過 filepath.Base 之後仍然是自己，必須另外擋掉。
	name := strings.TrimSpace(input.Name)
	if name == "" || name == "." || name == ".." || name != filepath.Base(name) || strings.ContainsAny(name, `/\`) {
		writeJSON(writer, http.StatusBadRequest, map[string]any{"error": "invalid file name"})
		return
	}
	content, err := base64.StdEncoding.DecodeString(input.ContentBase64)
	if err != nil {
		writeJSON(writer, http.StatusBadRequest, map[string]any{"error": "content is not valid base64"})
		return
	}
	if len(content) > maxSaveFileBytes {
		writeJSON(writer, http.StatusRequestEntityTooLarge, map[string]any{"error": "檔案內容過大"})
		return
	}
	path, err := folderpicker.Save(request.Context(), name)
	if errors.Is(err, folderpicker.ErrUnavailable) {
		writeJSON(writer, http.StatusNotImplemented, map[string]any{"error": err.Error()})
		return
	}
	if errors.Is(err, folderpicker.ErrCanceled) {
		// 取消不是錯誤。前端據此保持安靜，不要再退回瀏覽器下載——
		// 使用者剛剛才明確表示不想存。
		writeJSON(writer, http.StatusOK, map[string]any{"data": map[string]any{"canceled": true}})
		return
	}
	if err != nil {
		writeJSON(writer, http.StatusInternalServerError, map[string]any{"error": "choose save location: " + err.Error()})
		return
	}
	// 匯出檔可能含明文憑證，權限比照設定檔而不是一般下載檔。
	if err := os.WriteFile(path, content, 0o600); err != nil {
		writeJSON(writer, http.StatusInternalServerError, map[string]any{"error": "write file: " + err.Error()})
		return
	}
	writeData(writer, map[string]any{"path": path, "canceled": false})
}

func (s *Server) droppedMCPFiles(writer http.ResponseWriter, request *http.Request) {
	paths, err := folderpicker.DroppedFiles(request.Context())
	if errors.Is(err, folderpicker.ErrUnavailable) {
		writeJSON(writer, http.StatusNotImplemented, map[string]any{"error": err.Error()})
		return
	}
	if err != nil {
		writeJSON(writer, http.StatusInternalServerError, map[string]any{"error": "read dropped MCP files: " + err.Error()})
		return
	}
	files := make([]map[string]any, 0, len(paths))
	for _, path := range paths {
		extension := strings.ToLower(filepath.Ext(path))
		if extension != ".mcp" && extension != ".provider" && extension != ".json" {
			continue
		}
		info, statErr := os.Stat(path)
		if statErr != nil || !info.Mode().IsRegular() {
			continue
		}
		if info.Size() > 2*1024*1024 {
			writeJSON(writer, http.StatusRequestEntityTooLarge, map[string]any{"error": "設定檔不得超過 2 MB"})
			return
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			writeJSON(writer, http.StatusInternalServerError, map[string]any{"error": "read dropped MCP file: " + readErr.Error()})
			return
		}
		files = append(files, map[string]any{"name": filepath.Base(path), "size": info.Size(), "content": string(data)})
	}
	if len(files) == 0 {
		writer.WriteHeader(http.StatusNoContent)
		return
	}
	writeData(writer, files)
}

func (s *Server) serveStatic(writer http.ResponseWriter, request *http.Request) {
	path := strings.TrimPrefix(request.URL.Path, "/")
	if path == "" {
		path = "index.html"
	}
	if _, err := fs.Stat(mustAssets(), path); err != nil {
		request.URL.Path = "/index.html"
	}
	s.static.ServeHTTP(writer, request)
}

func (s *Server) newProxy(target *url.URL) http.Handler {
	proxy := &httputil.ReverseProxy{
		Rewrite: func(request *httputil.ProxyRequest) {
			request.SetURL(target)
			request.Out.URL.Path = "/" + strings.TrimPrefix(request.In.URL.Path, "/backend/")
			request.Out.Host = target.Host
			request.Out.Header.Del("Cookie")
			if s.backendToken != "" {
				request.Out.Header.Set("Authorization", "Bearer "+s.backendToken)
			}
		},
		FlushInterval: -1,
		ErrorHandler: func(writer http.ResponseWriter, _ *http.Request, err error) {
			writeJSON(writer, http.StatusBadGateway, map[string]any{"error": "backend unavailable: " + err.Error()})
		},
	}
	return proxy
}

func (s *Server) authorized(request *http.Request) bool {
	cookie, err := request.Cookie(controlCookie)
	if err != nil || len(cookie.Value) != len(s.controlToken) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(cookie.Value), []byte(s.controlToken)) == 1
}

func writeData(writer http.ResponseWriter, value any) {
	writeJSON(writer, http.StatusOK, map[string]any{"data": value})
}

func writeJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}

func mustAssets() fs.FS {
	assets, _ := console.Assets()
	return assets
}
