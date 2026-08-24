package webui

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

const contentSecurityPolicy = "default-src 'self'; connect-src 'self' ws: wss:; img-src 'self' data:; script-src 'self'; style-src 'self'; object-src 'none'; base-uri 'self'; frame-ancestors 'none'"

// New serves an immutable frontend build and delegates all API and realtime
// traffic to api. The build directory is trusted release input, not writable
// user content.
func New(root string, api http.Handler) (http.Handler, error) {
	if api == nil {
		return nil, errors.New("webui: API handler is required")
	}
	absoluteRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(filepath.Join(absoluteRoot, "index.html"))
	if err != nil || !info.Mode().IsRegular() {
		return nil, errors.New("webui: index.html is missing")
	}

	files := http.FileServer(http.Dir(absoluteRoot))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isBackendPath(r.URL.Path) {
			api.ServeHTTP(w, r)
			return
		}
		setSecurityHeaders(w)
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
			return
		}

		requestPath := filepath.FromSlash(strings.TrimPrefix(r.URL.Path, "/"))
		candidate := filepath.Join(absoluteRoot, requestPath)
		relative, err := filepath.Rel(absoluteRoot, candidate)
		if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			http.NotFound(w, r)
			return
		}
		if fileInfo, err := os.Stat(candidate); err == nil && fileInfo.Mode().IsRegular() {
			if strings.HasPrefix(strings.TrimPrefix(r.URL.Path, "/"), "assets/") {
				w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			} else {
				w.Header().Set("Cache-Control", "public, max-age=3600")
			}
			files.ServeHTTP(w, r)
			return
		}
		if filepath.Ext(requestPath) != "" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		http.ServeFile(w, r, filepath.Join(absoluteRoot, "index.html"))
	}), nil
}

func isBackendPath(path string) bool {
	return path == "/healthz" || path == "/ws" || strings.HasPrefix(path, "/api/")
}

func setSecurityHeaders(w http.ResponseWriter) {
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Referrer-Policy", "same-origin")
	w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
	w.Header().Set("Content-Security-Policy", contentSecurityPolicy)
}
