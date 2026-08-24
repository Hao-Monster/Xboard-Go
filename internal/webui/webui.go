package webui

import (
	"errors"
	"io/fs"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

const contentSecurityPolicy = "default-src 'self'; connect-src 'self' ws: wss:; img-src 'self' data: https: http:; script-src 'self'; style-src 'self'; object-src 'none'; base-uri 'self'; frame-ancestors 'none'"

type staticFile struct {
	fullPath string
	name     string
	modTime  time.Time
}

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
	files, err := buildManifest(absoluteRoot)
	if err != nil {
		return nil, err
	}
	index, ok := files["index.html"]
	if !ok {
		return nil, errors.New("webui: index.html is missing")
	}

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

		requestPath := strings.TrimPrefix(r.URL.Path, "/")
		if requestPath == "" {
			requestPath = "index.html"
		}
		if !fs.ValidPath(requestPath) {
			http.NotFound(w, r)
			return
		}
		if file, exists := files[requestPath]; exists {
			if strings.HasPrefix(requestPath, "assets/") {
				w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			} else if requestPath == "index.html" {
				w.Header().Set("Cache-Control", "no-store")
			} else {
				w.Header().Set("Cache-Control", "public, max-age=3600")
			}
			serveStaticFile(w, r, file)
			return
		}
		if path.Ext(requestPath) != "" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		serveStaticFile(w, r, index)
	}), nil
}

func buildManifest(root string) (map[string]staticFile, error) {
	files := make(map[string]staticFile)
	err := filepath.WalkDir(root, func(fullPath string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return errors.New("webui: build contains a non-regular file")
		}
		relative, err := filepath.Rel(root, fullPath)
		if err != nil {
			return err
		}
		files[filepath.ToSlash(relative)] = staticFile{
			fullPath: fullPath,
			name:     info.Name(),
			modTime:  info.ModTime(),
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return files, nil
}

func serveStaticFile(w http.ResponseWriter, r *http.Request, file staticFile) {
	content, err := os.Open(file.fullPath)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer content.Close()
	http.ServeContent(w, r, file.name, file.modTime, content)
}

func isBackendPath(path string) bool {
	return path == "/healthz" || path == "/ws" || strings.HasPrefix(path, "/api/") ||
		path == "/client-download" || strings.HasPrefix(path, "/client-download/") ||
		path == "/client-link" || strings.HasPrefix(path, "/client-link/")
}

func setSecurityHeaders(w http.ResponseWriter) {
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Referrer-Policy", "same-origin")
	w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
	w.Header().Set("Content-Security-Policy", contentSecurityPolicy)
}
