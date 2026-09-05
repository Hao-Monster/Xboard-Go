package webui

import (
	"errors"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

const contentSecurityPolicy = "default-src 'self'; connect-src 'self' ws: wss: https://www.google.com/recaptcha/ https://www.recaptcha.net/recaptcha/ https://challenges.cloudflare.com/turnstile/; img-src 'self' data: https: http:; script-src 'self' https://www.google.com/recaptcha/ https://www.gstatic.com/recaptcha/ https://www.recaptcha.net/recaptcha/ https://challenges.cloudflare.com/turnstile/; frame-src https://www.google.com/recaptcha/ https://recaptcha.google.com/recaptcha/ https://www.recaptcha.net/recaptcha/ https://challenges.cloudflare.com/turnstile/; style-src 'self'; object-src 'none'; base-uri 'self'; frame-ancestors 'none'"

type staticFile struct {
	fullPath string
	name     string
	modTime  time.Time
}

type FrontendAccess struct {
	Allowed    bool
	SecurePath string
}

type FrontendAccessResolver func(*http.Request) (FrontendAccess, error)

// New serves an immutable frontend build and delegates all API and realtime
// traffic to api. The build directory is trusted release input, not writable
// user content.
func New(root string, api http.Handler, accessResolvers ...FrontendAccessResolver) (http.Handler, error) {
	if api == nil {
		return nil, errors.New("webui: API handler is required")
	}
	if len(accessResolvers) > 1 {
		return nil, errors.New("webui: at most one frontend access resolver is supported")
	}
	var accessResolver FrontendAccessResolver
	if len(accessResolvers) == 1 {
		accessResolver = accessResolvers[0]
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
		if file, exists := files[requestPath]; fs.ValidPath(requestPath) && exists {
			if requestPath == "index.html" && !frontendAccessAllowed(w, r, accessResolver) {
				return
			}
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
		if !secureFrontendAccessAllowed(w, r, accessResolver) {
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		serveStaticFile(w, r, index)
	}), nil
}

func frontendAccessAllowed(w http.ResponseWriter, r *http.Request, resolve FrontendAccessResolver) bool {
	access, ok := resolveFrontendAccess(w, r, resolve)
	if !ok {
		return false
	}
	if !access.Allowed {
		http.Error(w, http.StatusText(http.StatusForbidden), http.StatusForbidden)
		return false
	}
	return true
}

func secureFrontendAccessAllowed(w http.ResponseWriter, r *http.Request, resolve FrontendAccessResolver) bool {
	access, ok := resolveFrontendAccess(w, r, resolve)
	if !ok {
		return false
	}
	if resolve != nil && (!validSecurePath(access.SecurePath) || (r.URL.Path != "/"+access.SecurePath && r.URL.Path != "/"+access.SecurePath+"/")) {
		http.NotFound(w, r)
		return false
	}
	if !access.Allowed {
		http.Error(w, http.StatusText(http.StatusForbidden), http.StatusForbidden)
		return false
	}
	return true
}

func resolveFrontendAccess(w http.ResponseWriter, r *http.Request, resolve FrontendAccessResolver) (FrontendAccess, bool) {
	if resolve == nil {
		return FrontendAccess{Allowed: true}, true
	}
	access, err := resolve(r)
	if err != nil {
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return FrontendAccess{}, false
	}
	return access, true
}

func validSecurePath(value string) bool {
	if len(value) == 0 || len(value) > 64 {
		return false
	}
	for _, character := range value {
		if (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') || character == '_' || character == '-' {
			continue
		}
		return false
	}
	return true
}

func HostMatchesURL(requestHost, configuredURL string) bool {
	configured, err := url.Parse(configuredURL)
	if err != nil || configured.User != nil || configured.Hostname() == "" || (configured.Scheme != "http" && configured.Scheme != "https") {
		return false
	}
	request, err := url.Parse("//" + requestHost)
	if err != nil || request.User != nil || request.Hostname() == "" || request.RawQuery != "" || request.Fragment != "" {
		return false
	}
	want := strings.TrimSuffix(strings.ToLower(configured.Hostname()), ".")
	got := strings.TrimSuffix(strings.ToLower(request.Hostname()), ".")
	wantIP, gotIP := net.ParseIP(want), net.ParseIP(got)
	if wantIP != nil || gotIP != nil {
		return wantIP != nil && gotIP != nil && wantIP.Equal(gotIP)
	}
	return got == want
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
		path == "/client-link" || strings.HasPrefix(path, "/client-link/") ||
		path == "/guide" || strings.HasPrefix(path, "/guide/") ||
		path == "/knowledge-attachments" || strings.HasPrefix(path, "/knowledge-attachments/") ||
		path == "/guide-attachments" || strings.HasPrefix(path, "/guide-attachments/") ||
		isDynamicSubscriptionPath(path)
}

func isDynamicSubscriptionPath(path string) bool {
	segments := strings.Split(strings.Trim(path, "/"), "/")
	if len(segments) != 2 || segments[0] == "" || len(segments[1]) != 32 {
		return false
	}
	for _, character := range segments[1] {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}

func setSecurityHeaders(w http.ResponseWriter) {
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Referrer-Policy", "same-origin")
	w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
	w.Header().Set("Content-Security-Policy", contentSecurityPolicy)
}
