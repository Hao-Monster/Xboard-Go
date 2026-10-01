package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
)

const maxNodeReleaseManifestSize = 1 << 20

func (s *server) nodeReleaseMetadata(w http.ResponseWriter, r *http.Request) {
	if s.nodeReleaseRoot == "" {
		http.NotFound(w, r)
		return
	}
	version := r.PathValue("version")
	if !validNodeReleaseSegment(version) {
		http.NotFound(w, r)
		return
	}
	versionPath := filepath.Join(s.nodeReleaseRoot, version)
	versionInfo, err := os.Lstat(versionPath)
	if err != nil || !versionInfo.IsDir() || versionInfo.Mode()&os.ModeSymlink != 0 {
		http.NotFound(w, r)
		return
	}
	releaseRoot, err := os.OpenRoot(s.nodeReleaseRoot)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer releaseRoot.Close()
	release, err := releaseRoot.OpenRoot(version)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer release.Close()
	manifestInfo, err := release.Lstat("manifest.json")
	if err != nil || !manifestInfo.Mode().IsRegular() || manifestInfo.Mode()&os.ModeSymlink != 0 || manifestInfo.Size() > maxNodeReleaseManifestSize {
		http.NotFound(w, r)
		return
	}
	manifest, err := release.Open("manifest.json")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer manifest.Close()
	data, err := io.ReadAll(io.LimitReader(manifest, maxNodeReleaseManifestSize+1))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	source, err := parseNodeReleaseManifest(data, version)
	if err != nil {
		http.Error(w, "invalid release manifest", http.StatusInternalServerError)
		return
	}
	checksumInfo, err := release.Lstat("SHA256SUMS")
	if err != nil || !checksumInfo.Mode().IsRegular() || checksumInfo.Mode()&os.ModeSymlink != 0 {
		http.Error(w, "invalid release checksums", http.StatusInternalServerError)
		return
	}
	type asset struct {
		Name string `json:"name"`
		URL  string `json:"url"`
	}
	assets := make([]asset, 0, len(source.Artifacts)+1)
	for _, item := range source.Artifacts {
		if !validNodeReleaseSegment(item.Name) {
			http.Error(w, "invalid release artifact", http.StatusInternalServerError)
			return
		}
		assets = append(assets, asset{Name: item.Name, URL: s.panelURL + "/api/v2/node/releases/" + version + "/" + item.Name})
	}
	assets = append(assets, asset{Name: "SHA256SUMS", URL: s.panelURL + "/api/v2/node/releases/" + version + "/SHA256SUMS"})
	w.Header().Set("Content-Type", "application/json")
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
	if !validNodeReleaseSegment(version) || !validNodeReleaseSegment(artifact) {
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
	// OpenRoot confines all later reads even if a filesystem entry changes.
	releaseRoot, err := os.OpenRoot(s.nodeReleaseRoot)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer releaseRoot.Close()
	release, err := releaseRoot.OpenRoot(version)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer release.Close()
	manifestInfo, err := release.Lstat("manifest.json")
	if err != nil || !manifestInfo.Mode().IsRegular() || manifestInfo.Size() > maxNodeReleaseManifestSize {
		http.NotFound(w, r)
		return
	}
	manifest, err := release.Open("manifest.json")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	data, err := io.ReadAll(io.LimitReader(manifest, maxNodeReleaseManifestSize+1))
	manifest.Close()
	source, parseErr := parseNodeReleaseManifest(data, version)
	if err != nil || parseErr != nil {
		http.NotFound(w, r)
		return
	}
	allowed := artifact == "manifest.json" || artifact == "SHA256SUMS"
	for _, item := range source.Artifacts {
		allowed = allowed || artifact == item.Name
	}
	if !allowed {
		http.NotFound(w, r)
		return
	}
	info, err := release.Lstat(artifact)
	if err != nil || !info.Mode().IsRegular() {
		http.NotFound(w, r)
		return
	}
	file, err := release.Open(artifact)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer file.Close()
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	// SHA256SUMS is consumed by POSIX sha256sum. Normalize line endings at the
	// HTTP boundary so releases copied from Windows remain valid on Linux.
	if artifact == "SHA256SUMS" {
		data, err := io.ReadAll(io.LimitReader(file, maxNodeReleaseManifestSize+1))
		if err != nil || len(data) > maxNodeReleaseManifestSize {
			http.NotFound(w, r)
			return
		}
		data = bytes.ReplaceAll(data, []byte("\r\n"), []byte("\n"))
		data = bytes.ReplaceAll(data, []byte("\r"), []byte("\n"))
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		http.ServeContent(w, r, artifact, info.ModTime(), bytes.NewReader(data))
		return
	}
	http.ServeContent(w, r, artifact, info.ModTime(), file)
}

func validNodeReleaseSegment(value string) bool {
	if value == "" || value == "." || value == ".." || len(value) > 128 {
		return false
	}
	for i := 0; i < len(value); i++ {
		char := value[i]
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9') || char == '.' || char == '_' || char == '-' || char == '+' {
			continue
		}
		return false
	}
	return true
}

type nodeReleaseManifest struct {
	Version   string `json:"version"`
	Artifacts []struct {
		Name string `json:"name"`
	} `json:"artifacts"`
}

func parseNodeReleaseManifest(data []byte, version string) (nodeReleaseManifest, error) {
	var source nodeReleaseManifest
	if len(data) > maxNodeReleaseManifestSize || json.Unmarshal(data, &source) != nil || source.Version != version || len(source.Artifacts) == 0 {
		return source, errors.New("invalid manifest")
	}
	seen := make(map[string]bool)
	for _, item := range source.Artifacts {
		if !validNodeReleaseSegment(item.Name) || seen[item.Name] {
			return source, errors.New("invalid artifact")
		}
		seen[item.Name] = true
	}
	return source, nil
}
