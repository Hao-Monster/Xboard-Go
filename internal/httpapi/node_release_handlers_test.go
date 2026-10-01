package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNodeReleaseManifestAndArtifactAreServedFromConfiguredRoot(t *testing.T) {
	root := t.TempDir()
	versionDir := filepath.Join(root, "v1.14.3-test")
	if err := os.Mkdir(versionDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(versionDir, "manifest.json"), []byte(`{"version":"v1.14.3-test","artifacts":[{"name":"install.sh"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(versionDir, "install.sh"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(versionDir, "SHA256SUMS"), []byte("hash  install.sh\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	api, _ := newTestAPIWithAllOptionsAndModifier(t, nil, true, nil, nil, false, nil, nil, func(dependencies *Dependencies) {
		dependencies.NodeReleaseRoot = root
	})

	for _, test := range []struct {
		name string
		path string
		want string
	}{
		{name: "manifest alias", path: "/api/v2/node/releases/v1.14.3-test/manifest", want: `{"version":"v1.14.3-test","artifacts":[{"name":"install.sh"}]}`},
		{name: "artifact", path: "/api/v2/node/releases/v1.14.3-test/install.sh", want: "#!/bin/sh\nexit 0\n"},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, test.path, nil)
			response := httptest.NewRecorder()
			api.ServeHTTP(response, request)
			if response.Code != http.StatusOK {
				t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusOK, response.Body)
			}
			if response.Body.String() != test.want {
				t.Fatalf("body = %q, want %q", response.Body.String(), test.want)
			}
			if response.Header().Get("Cache-Control") != "public, max-age=31536000, immutable" {
				t.Fatalf("cache-control = %q", response.Header().Get("Cache-Control"))
			}
		})
	}

	if err := os.WriteFile(filepath.Join(versionDir, "SHA256SUMS"), []byte("hash  install.sh\r\nsecond-hash  xbctl-linux-amd64\r\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/api/v2/node/releases/v1.14.3-test/SHA256SUMS", nil)
	response := httptest.NewRecorder()
	api.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("checksums status = %d, want %d; body=%s", response.Code, http.StatusOK, response.Body)
	}
	if response.Body.String() != "hash  install.sh\nsecond-hash  xbctl-linux-amd64\n" {
		t.Fatalf("checksums body = %q, want normalized LF line endings", response.Body.String())
	}
	if response.Header().Get("Content-Type") != "text/plain; charset=utf-8" {
		t.Fatalf("checksums content-type = %q", response.Header().Get("Content-Type"))
	}

	request = httptest.NewRequest(http.MethodGet, "/api/v2/node/releases/v1.14.3-test", nil)
	response = httptest.NewRecorder()
	api.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("metadata status = %d, want %d; body=%s", response.Code, http.StatusOK, response.Body)
	}
	var metadata struct {
		TagName string `json:"tag_name"`
		Assets  []struct {
			Name  string `json:"name"`
			URL   string `json:"url"`
			Size  int64  `json:"size"`
			State string `json:"state"`
		} `json:"assets"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &metadata); err != nil {
		t.Fatal(err)
	}
	if metadata.TagName != "v1.14.3-test" || len(metadata.Assets) != 2 || metadata.Assets[0].URL != "https://panel.example.test/api/v2/node/releases/v1.14.3-test/install.sh" || metadata.Assets[1].Name != "SHA256SUMS" {
		t.Fatalf("unexpected metadata: %+v", metadata)
	}
	for _, asset := range metadata.Assets {
		info, err := os.Stat(filepath.Join(versionDir, asset.Name))
		if err != nil {
			t.Fatal(err)
		}
		if asset.Size != info.Size() || asset.State != "uploaded" {
			t.Fatalf("incomplete asset metadata: %+v", asset)
		}
	}
	if response.Header().Get("Cache-Control") != "public, max-age=60" {
		t.Fatalf("metadata cache-control = %q", response.Header().Get("Cache-Control"))
	}
	if response.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("metadata content-type = %q", response.Header().Get("Content-Type"))
	}
}

func TestNodeReleaseMetadataRequiresChecksumsAndRejectsUnsafeArtifactNames(t *testing.T) {
	root := t.TempDir()
	versionDir := filepath.Join(root, "v1.14.3-test")
	if err := os.Mkdir(versionDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeManifest := func(manifest string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(versionDir, "manifest.json"), []byte(manifest), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(versionDir, "SHA256SUMS"), []byte("hash  install.sh\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	api, _ := newTestAPIWithAllOptionsAndModifier(t, nil, true, nil, nil, false, nil, nil, func(dependencies *Dependencies) {
		dependencies.NodeReleaseRoot = root
	})
	writeManifest(`{"version":"v1.14.3-test","artifacts":[{"name":"../outside"}]}`)
	request := httptest.NewRequest(http.MethodGet, "/api/v2/node/releases/v1.14.3-test", nil)
	response := httptest.NewRecorder()
	api.ServeHTTP(response, request)
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("unsafe artifact status = %d, want %d", response.Code, http.StatusInternalServerError)
	}

	if err := os.Remove(filepath.Join(versionDir, "SHA256SUMS")); err != nil {
		t.Fatal(err)
	}
	writeManifest(`{"version":"v1.14.3-test","artifacts":[{"name":"install.sh"}]}`)
	if err := os.Remove(filepath.Join(versionDir, "SHA256SUMS")); err != nil {
		t.Fatal(err)
	}
	response = httptest.NewRecorder()
	api.ServeHTTP(response, request)
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("missing checksums status = %d, want %d", response.Code, http.StatusInternalServerError)
	}
}

func TestNodeReleaseArtifactRejectsTraversalSymlinksAndUnconfiguredRoot(t *testing.T) {
	root := t.TempDir()
	versionDir := filepath.Join(root, "v1.14.3-test")
	if err := os.Mkdir(versionDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(versionDir, "manifest.json"), []byte(`{"version":"v1.14.3-test","artifacts":[{"name":"linked.txt"}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(outside, []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(versionDir, "linked.txt")); err != nil {
		t.Skipf("symlink creation unavailable: %v", err)
	}
	if err := os.Symlink(t.TempDir(), filepath.Join(root, "linked-version")); err != nil {
		t.Skipf("symlink creation unavailable: %v", err)
	}

	api, _ := newTestAPIWithAllOptionsAndModifier(t, nil, true, nil, nil, false, nil, nil, func(dependencies *Dependencies) {
		dependencies.NodeReleaseRoot = root
	})
	for _, path := range []string{
		"/api/v2/node/releases/v1.14.3-test/linked.txt",
		"/api/v2/node/releases/linked-version/manifest",
	} {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		response := httptest.NewRecorder()
		api.ServeHTTP(response, request)
		if response.Code != http.StatusNotFound {
			t.Fatalf("path %q status = %d, want %d; body=%s", path, response.Code, http.StatusNotFound, response.Body)
		}
	}

	unconfigured, _ := newTestAPI(t)
	request := httptest.NewRequest(http.MethodGet, "/api/v2/node/releases/v1.14.3-test/manifest", nil)
	response := httptest.NewRecorder()
	unconfigured.ServeHTTP(response, request)
	if response.Code != http.StatusNotFound {
		t.Fatalf("unconfigured status = %d, want %d", response.Code, http.StatusNotFound)
	}
}

func TestNodeReleaseMetadataRejectsSymlinkManifest(t *testing.T) {
	root := t.TempDir()
	versionDir := filepath.Join(root, "v1.14.3-test")
	if err := os.Mkdir(versionDir, 0o755); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "manifest.json")
	if err := os.WriteFile(outside, []byte(`{"version":"v1.14.3-test","artifacts":[{"name":"install.sh"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(versionDir, "manifest.json")); err != nil {
		t.Skipf("symlink creation unavailable: %v", err)
	}
	api, _ := newTestAPIWithAllOptionsAndModifier(t, nil, true, nil, nil, false, nil, nil, func(dependencies *Dependencies) {
		dependencies.NodeReleaseRoot = root
	})
	request := httptest.NewRequest(http.MethodGet, "/api/v2/node/releases/v1.14.3-test", nil)
	response := httptest.NewRecorder()
	api.ServeHTTP(response, request)
	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusNotFound)
	}
}

func TestNodeReleaseRejectsUnlistedFilesAndInvalidManifest(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "v1")
	if err := os.Mkdir(dir, 0755); err != nil {
		t.Fatal(err)
	}
	for name, contents := range map[string]string{"manifest.json": `{"version":"v1","artifacts":[{"name":"install.sh"}]}`, "install.sh": "installer", "SHA256SUMS": "checksum", "private.key": "secret"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(contents), 0600); err != nil {
			t.Fatal(err)
		}
	}
	api, _ := newTestAPIWithAllOptionsAndModifier(t, nil, true, nil, nil, false, nil, nil, func(d *Dependencies) { d.NodeReleaseRoot = root })
	for _, name := range []string{"private.key", "..%2fprivate.key", "%5cprivate.key"} {
		w := httptest.NewRecorder()
		api.ServeHTTP(w, httptest.NewRequest("GET", "/api/v2/node/releases/v1/"+name, nil))
		if w.Code != 404 {
			t.Fatalf("%s: status %d", name, w.Code)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), []byte(`{"version":"other","artifacts":[{"name":"install.sh"}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	api.ServeHTTP(w, httptest.NewRequest("GET", "/api/v2/node/releases/v1/install.sh", nil))
	if w.Code != 404 {
		t.Fatalf("mismatched manifest served: %d", w.Code)
	}
}
func TestMachineInstallCommandUsesPanelReleaseSource(t *testing.T) {
	s := server{nodeRelease: "v1.14.3", panelURL: "https://panel.example.test:8443"}
	command := s.installCommand(42, "fixture'enrollment")
	for _, forbidden := range []string{"gh release", "gh auth", "XBOARD_NODE_RELEASE_TOKEN", "--preserve-env"} {
		if strings.Contains(command, forbidden) {
			t.Fatalf("unexpected GitHub dependency %s", forbidden)
		}
	}
	for _, required := range []string{"https://panel.example.test:8443/api/v2/node/releases", "--release-api-base", "sha256sum -c -", "--machine-id 42", shellQuote("fixture'enrollment"), "--proto '=https'"} {
		if !strings.Contains(command, required) {
			t.Fatalf("missing %s", required)
		}
	}
}

func TestMachineInstallCommandUsesPublicGitHubRelease(t *testing.T) {
	s := server{nodeRelease: "v1.14.5", nodeReleaseSource: "github", panelURL: "https://panel.example.test:8443"}
	command := s.installCommand(42, "fixture'enrollment")
	for _, required := range []string{"https://github.com/Hao-Monster/Xboard-Go/releases/download/node-v1.14.5", "--http1.1", "--retry 2", "sha256sum -c -", "--panel 'https://panel.example.test:8443'", shellQuote("fixture'enrollment")} {
		if !strings.Contains(command, required) {
			t.Fatalf("missing %s", required)
		}
	}
	for _, forbidden := range []string{"api/v2/node/releases", "api.github.com", "gh auth", "XBOARD_NODE_RELEASE_TOKEN"} {
		if strings.Contains(command, forbidden) {
			t.Fatalf("unexpected dependency %s", forbidden)
		}
	}
}

func TestNodeReleaseMetadataRejectsUnavailableArtifacts(t *testing.T) {
	for _, filename := range []string{"install.sh", "SHA256SUMS"} {
		for _, kind := range []string{"missing", "empty", "directory", "symlink"} {
			t.Run(filename+"/"+kind, func(t *testing.T) {
				root := t.TempDir()
				dir := filepath.Join(root, "v1")
				if err := os.Mkdir(dir, 0755); err != nil {
					t.Fatal(err)
				}
				for name, data := range map[string]string{"manifest.json": `{"version":"v1","artifacts":[{"name":"install.sh"}]}`, "install.sh": "installer", "SHA256SUMS": "checksum"} {
					if name == filename {
						continue
					}
					if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0600); err != nil {
						t.Fatal(err)
					}
				}
				path := filepath.Join(dir, filename)
				switch kind {
				case "empty":
					if err := os.WriteFile(path, nil, 0600); err != nil {
						t.Fatal(err)
					}
				case "directory":
					if err := os.Mkdir(path, 0700); err != nil {
						t.Fatal(err)
					}
				case "symlink":
					target := filepath.Join(root, "outside")
					if err := os.WriteFile(target, []byte("outside"), 0600); err != nil {
						t.Fatal(err)
					}
					if err := os.Symlink(target, path); err != nil {
						t.Skipf("symlinks unavailable: %v", err)
					}
				}
				api, _ := newTestAPIWithAllOptionsAndModifier(t, nil, true, nil, nil, false, nil, nil, func(d *Dependencies) { d.NodeReleaseRoot = root })
				w := httptest.NewRecorder()
				api.ServeHTTP(w, httptest.NewRequest("GET", "/api/v2/node/releases/v1", nil))
				if w.Code != http.StatusInternalServerError {
					t.Fatalf("incomplete release status = %d, body = %s", w.Code, w.Body)
				}
				if strings.Contains(w.Body.String(), `"assets"`) {
					t.Fatal("partially available release advertised")
				}
			})
		}
	}
}
