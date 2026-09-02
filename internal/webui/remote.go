package webui

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"
)

const remoteIndexMaxBytes = 2 << 20

// NewRemote delegates backend traffic to api and retrieves only index.html from
// a trusted internal frontend origin for authorized browser entry requests. It
// never forwards browser credentials or arbitrary request paths upstream.
func NewRemote(origin string, api http.Handler, accessResolvers ...FrontendAccessResolver) (http.Handler, error) {
	transport, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return nil, errors.New("webui: default HTTP transport is unavailable")
	}
	directTransport := transport.Clone()
	directTransport.Proxy = nil
	client := &http.Client{
		Transport: directTransport,
		Timeout:   5 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	return newRemote(origin, api, client, accessResolvers...)
}

func newRemote(origin string, api http.Handler, client *http.Client, accessResolvers ...FrontendAccessResolver) (http.Handler, error) {
	if api == nil {
		return nil, errors.New("webui: API handler is required")
	}
	if client == nil {
		return nil, errors.New("webui: remote frontend HTTP client is required")
	}
	if len(accessResolvers) > 1 {
		return nil, errors.New("webui: at most one frontend access resolver is supported")
	}
	var accessResolver FrontendAccessResolver
	if len(accessResolvers) == 1 {
		accessResolver = accessResolvers[0]
	}
	indexURL, err := remoteIndexURL(origin)
	if err != nil {
		return nil, err
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

		switch r.URL.Path {
		case "/", "/index.html":
			if !frontendAccessAllowed(w, r, accessResolver) {
				return
			}
		default:
			requestPath := strings.TrimPrefix(r.URL.Path, "/")
			if requestPath == "" || path.Ext(requestPath) != "" {
				http.NotFound(w, r)
				return
			}
			if !secureFrontendAccessAllowed(w, r, accessResolver) {
				return
			}
		}

		body, fetchErr := fetchRemoteIndex(r, client, indexURL)
		if fetchErr != nil {
			http.Error(w, http.StatusText(http.StatusBadGateway), http.StatusBadGateway)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Content-Length", strconv.Itoa(len(body)))
		if r.Method == http.MethodHead {
			return
		}
		_, _ = w.Write(body)
	}), nil
}

func remoteIndexURL(origin string) (*url.URL, error) {
	value := strings.TrimRight(strings.TrimSpace(origin), "/")
	parsed, err := url.Parse(value)
	if err != nil || len(value) == 0 || len(value) > 2048 || parsed.Host == "" ||
		(parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.User != nil || parsed.Path != "" ||
		parsed.RawPath != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, errors.New("webui: remote frontend origin must be an absolute http or https origin without credentials, path, query, or fragment")
	}
	parsed.Path = "/index.html"
	return parsed, nil
}

func fetchRemoteIndex(request *http.Request, client *http.Client, indexURL *url.URL) ([]byte, error) {
	upstreamRequest, err := http.NewRequestWithContext(request.Context(), http.MethodGet, indexURL.String(), nil)
	if err != nil {
		return nil, err
	}
	upstreamRequest.Header.Set("Accept", "text/html")
	response, err := client.Do(upstreamRequest)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("remote frontend returned status %d", response.StatusCode)
	}
	mediaType := strings.ToLower(strings.TrimSpace(strings.Split(response.Header.Get("Content-Type"), ";")[0]))
	if mediaType != "text/html" {
		return nil, errors.New("remote frontend returned a non-HTML index")
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, remoteIndexMaxBytes+1))
	if err != nil {
		return nil, err
	}
	if len(body) > remoteIndexMaxBytes {
		return nil, errors.New("remote frontend index exceeds size limit")
	}
	return body, nil
}
