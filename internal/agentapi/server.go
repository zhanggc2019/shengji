// Package agentapi presents the server's transcription service over HTTP and MCP.
package agentapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"strings"

	"github.com/zhanggc2019/shengji/internal/minutes"
)

type service struct {
	app      *minutes.App
	inputDir string
}

type asrStatus struct {
	minutes.ActiveASR
	InputDir string `json:"input_dir"`
	MaxBytes int64  `json:"max_bytes"`
}

// WithRoutes installs agent endpoints on the same networks as the browser workspace.
func WithRoutes(app *minutes.App, inputDir string, browser http.Handler, allowLAN bool) (http.Handler, error) {
	root, err := filepath.Abs(inputDir)
	if err != nil {
		return nil, fmt.Errorf("解析 ASR 输入目录: %w", err)
	}
	if err = os.MkdirAll(root, 0700); err != nil {
		return nil, fmt.Errorf("创建 ASR 输入目录: %w", err)
	}
	s := &service{app: app, inputDir: root}
	router := http.NewServeMux()
	router.Handle("GET /api/asr/status", networkAccess(http.HandlerFunc(s.statusHTTP), allowLAN))
	router.Handle("POST /api/asr/transcriptions", networkAccess(http.HandlerFunc(s.transcribeHTTP), allowLAN))
	router.Handle("/mcp", networkAccess(s.mcpHandler(), allowLAN))
	router.Handle("/", browser)
	return router, nil
}

// status describes the selected model and the MCP audio directory.
func (s *service) status() (asrStatus, error) {
	model, err := s.app.ActiveASR()
	return asrStatus{ActiveASR: model, InputDir: s.inputDir, MaxBytes: minutes.MaxTranscriptionBytes}, err
}

// statusHTTP reports the current model without returning provider credentials.
func (s *service) statusHTTP(w http.ResponseWriter, _ *http.Request) {
	status, err := s.status()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, status)
}

// transcribeHTTP accepts one multipart file and returns its transcript in the same request.
func (s *service) transcribeHTTP(w http.ResponseWriter, r *http.Request) {
	const maxBody = minutes.MaxTranscriptionBytes + 1<<20
	if r.ContentLength > maxBody {
		writeError(w, http.StatusRequestEntityTooLarge, minutes.ErrASRAudioTooLarge.Error())
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBody)
	multipart, err := r.MultipartReader()
	if err != nil {
		writeError(w, http.StatusBadRequest, "请使用 multipart/form-data 上传 file")
		return
	}
	file, err := multipart.NextPart()
	if err != nil || file.FormName() != "file" || file.FileName() == "" {
		writeError(w, http.StatusBadRequest, "请在第一个 multipart 字段上传 file")
		return
	}
	result, err := s.app.TranscribeAudio(r.Context(), file, file.FileName(), r.URL.Query().Get("language"))
	if err != nil {
		var maxErr *http.MaxBytesError
		code := http.StatusBadGateway
		switch {
		case errors.Is(err, minutes.ErrASRLanguage):
			code = http.StatusBadRequest
		case errors.Is(err, minutes.ErrASRAudioFormat):
			code = http.StatusUnsupportedMediaType
		case errors.Is(err, minutes.ErrASREmpty):
			code = http.StatusUnprocessableEntity
		case errors.Is(err, minutes.ErrASRAudioTooLarge), errors.As(err, &maxErr):
			code = http.StatusRequestEntityTooLarge
		}
		writeError(w, code, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// networkAccess follows the listener's scope and checks the peer, Host and browser origin.
func networkAccess(next http.Handler, allowLAN bool) http.Handler {
	protected := http.NewCrossOriginProtection().Handler(next)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		peer, _ := netip.ParseAddrPort(r.RemoteAddr)
		host := r.Host
		if name, _, err := net.SplitHostPort(host); err == nil {
			host = name
		}
		ip, _ := netip.ParseAddr(strings.Trim(host, "[]"))
		if !allowedAddress(peer.Addr(), allowLAN) || (!allowedAddress(ip, allowLAN) && !strings.EqualFold(host, "localhost")) {
			writeError(w, http.StatusForbidden, "请使用服务监听的本机或局域网地址访问 ASR Agent 接口")
			return
		}
		if origin := r.Header.Get("Origin"); origin != "" && origin != "http://"+r.Host && origin != "https://"+r.Host {
			writeError(w, http.StatusForbidden, "拒绝跨站请求")
			return
		}
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Cache-Control", "no-store")
		protected.ServeHTTP(w, r)
	})
}

// allowedAddress accepts loopback and, on a LAN listener, private or link-local addresses.
func allowedAddress(ip netip.Addr, allowLAN bool) bool {
	return ip.Unmap().IsLoopback() || (allowLAN && minutes.IsLANAddress(ip))
}

// writeError uses the same error shape as the browser API.
func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

// writeJSON sends a UTF-8 JSON result.
func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
