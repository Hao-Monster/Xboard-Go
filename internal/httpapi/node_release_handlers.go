package httpapi

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

func (s *server) nodeReleaseMetadata(w http.ResponseWriter, r *http.Request) {
	if s.nodeReleaseRoot == "" {
		http.NotFound(w, r)
		return
	}
	version := r.PathValue("version")
	if version == "" || strings.ContainsAny(version, `/\\`) {
		http.NotFound(w, r)
		return
	}
	path := filepath.Join(s.nodeReleaseRoot, version, "manifest.json")
	data, err := os.ReadFile(path)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	var source struct {
		Version   string `json:"version"`
		Artifacts []struct {
			Name string `json:"name"`
		} `json:"artifacts"`
	}
	if err := json.Unmarshal(data, &source); err != nil || source.Version != version || len(source.Artifacts) == 0 {
		http.Error(w, "invalid release manifest", http.StatusInternalServerError)
		return
	}
	type asset struct {
		Name string `json:"name"`
		URL  string `json:"url"`
	}
	assets := make([]asset, 0, len(source.Artifacts)+1)
	for _, item := range source.Artifacts {
		assets = append(assets, asset{Name: item.Name, URL: s.panelURL + "/api/v2/node/releases/" + version + "/" + item.Name})
	}
	assets = append(assets, asset{Name: "SHA256SUMS", URL: s.panelURL + "/api/v2/node/releases/" + version + "/SHA256SUMS"})
	w.Header().Set("Cache-Control", "public, max-age=60")
	_ = json.NewEncoder(w).Encode(map[string]any{"tag_name": version, "draft": false, "assets": assets})
}

// nodeReleaseArtifact serves only files from the explicitly configured release
// root. It is disabled when no root is configured and rejects traversal.
func (s *server) nodeReleaseArtifact(w http.ResponseWriter, r *http.Request) {
	s.serveNodeReleaseArtifact(w, r, r.PathValue("artifact"))
}

// nodeReleaseManifest serves the stable manifest name without exposing the
// release root itself. Keeping this as a distinct route makes the client
// contract explicit while the generic artifact route remains useful for the
// signed binaries and installer.
func (s *server) nodeReleaseManifest(w http.ResponseWriter, r *http.Request) {
	s.serveNodeReleaseArtifact(w, r, "manifest.json")
}

func (s *server) serveNodeReleaseArtifact(w http.ResponseWriter, r *http.Request, artifact string) {
	if s.nodeReleaseRoot == "" {
		http.NotFound(w, r)
		return
	}
	version := r.PathValue("version")
	if version == "" || artifact == "" || strings.ContainsAny(version, `/\\`) || strings.ContainsAny(artifact, `/\\`) || artifact == "." || artifact == ".." {
		http.NotFound(w, r)
		return
	}
	root, err := filepath.Abs(s.nodeReleaseRoot)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	versionPath := filepath.Join(root, version)
	versionInfo, err := os.Lstat(versionPath)
	if err != nil || !versionInfo.IsDir() || versionInfo.Mode()&os.ModeSymlink != 0 {
		http.NotFound(w, r)
		return
	}
	path, err := filepath.Abs(filepath.Join(versionPath, artifact))
	if err != nil || filepath.Dir(path) != filepath.Join(root, version) {
		http.NotFound(w, r)
		return
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	http.ServeFile(w, r, path)
}
