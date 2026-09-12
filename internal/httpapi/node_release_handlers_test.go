package httpapi

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestNodeReleaseManifestAndArtifactAreServedFromConfiguredRoot(t *testing.T) {
	root := t.TempDir()
	versionDir := filepath.Join(root, "v1.14.3-test")
	if err := os.Mkdir(versionDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(versionDir, "manifest.json"), []byte(`{"version":"v1.14.3-test"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(versionDir, "install.sh"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
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
		{name: "manifest alias", path: "/api/v2/node/releases/v1.14.3-test/manifest", want: `{"version":"v1.14.3-test"}`},
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
}

func TestNodeReleaseArtifactRejectsTraversalSymlinksAndUnconfiguredRoot(t *testing.T) {
	root := t.TempDir()
	versionDir := filepath.Join(root, "v1.14.3-test")
	if err := os.Mkdir(versionDir, 0o755); err != nil {
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
