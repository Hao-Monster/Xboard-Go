package clientcatalog

import (
	"context"
	"encoding/base64"
	"io"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

func TestDefaultCatalogMatchesPinnedXboardOrderAndPlatforms(t *testing.T) {
	definitions := DefaultDefinitions()
	want := []string{"karing", "happ", "clash-mi", "koalaclash", "flclashx", "rabbit-hole", "prizrakbox", "flowvy", "throne", "v2raytun", "shadowrocket", "incy", "renoarx", "deskbox", "inhive"}
	if len(definitions) != len(want) {
		t.Fatalf("definitions = %d, want %d", len(definitions), len(want))
	}
	for index, id := range want {
		if definitions[index].ID != id {
			t.Fatalf("definition[%d].ID = %q, want %q", index, definitions[index].ID, id)
		}
	}
	if definitions[3].Downloads[0].Repository != "coolcoala/koala-clash" {
		t.Fatalf("Koala Clash repository = %q", definitions[3].Downloads[0].Repository)
	}
	seenClients := make(map[string]struct{}, len(definitions))
	validPlatforms := make(map[string]struct{}, len(platforms))
	for _, platform := range platforms {
		validPlatforms[platform] = struct{}{}
	}
	for _, definition := range definitions {
		if _, exists := seenClients[definition.ID]; exists {
			t.Fatalf("duplicate client ID %q", definition.ID)
		}
		seenClients[definition.ID] = struct{}{}
		if definition.ID == "" || definition.Name == "" || definition.Core == "" || definition.Description == "" || len(definition.Downloads) == 0 {
			t.Fatalf("incomplete definition %#v", definition)
		}
		seenPlatforms := make(map[string]struct{}, len(definition.Downloads))
		for _, download := range definition.Downloads {
			if _, exists := validPlatforms[download.Platform]; !exists {
				t.Fatalf("%s has unknown platform %q", definition.ID, download.Platform)
			}
			if _, exists := seenPlatforms[download.Platform]; exists {
				t.Fatalf("%s has duplicate platform %q", definition.ID, download.Platform)
			}
			seenPlatforms[download.Platform] = struct{}{}
			if download.URL != "" {
				if download.Repository != "" || len(download.Patterns) != 0 {
					t.Fatalf("%s/%s mixes fixed and GitHub sources", definition.ID, download.Platform)
				}
				if _, err := validateExternalURL(download.URL); err != nil {
					t.Fatalf("%s/%s fixed URL: %v", definition.ID, download.Platform, err)
				}
			} else if download.Repository == "" || len(download.Patterns) == 0 {
				t.Fatalf("%s/%s has no resolvable source", definition.ID, download.Platform)
			}
			for _, pattern := range download.Patterns {
				if _, err := regexp.Compile(pattern); err != nil {
					t.Fatalf("%s/%s pattern %q: %v", definition.ID, download.Platform, pattern, err)
				}
			}
			if download.FallbackURL != "" {
				if _, err := validateExternalURL(download.FallbackURL); err != nil {
					t.Fatalf("%s/%s fallback URL: %v", definition.ID, download.Platform, err)
				}
			}
		}
	}
}

func TestServiceValidatesOverridesAndBuildsStableUserRoutes(t *testing.T) {
	database := testStore(t)
	service := New(Options{Store: database, PanelURL: "https://panel.example.test/base", Now: time.Now})
	admin, err := service.AdminCatalog(context.Background())
	if err != nil || admin.Revision != 1 || len(admin.Clients) != 15 {
		t.Fatalf("AdminCatalog() = (%#v, %v)", admin, err)
	}
	links := OverrideInput{"karing": {"android": {
		"direct":   "https://downloads.example.test/karing.apk",
		"qr":       "https://qr.example.test/karing",
		"cloud":    "https://cloud.example.test/karing",
		"tutorial": "/guide/12/karing",
	}}}
	admin, err = service.SaveOverrides(context.Background(), admin.Revision, links)
	if err != nil || admin.Revision != 2 {
		t.Fatalf("SaveOverrides() = (%#v, %v)", admin, err)
	}
	clients, err := service.UserCatalog(context.Background())
	if err != nil || len(clients) != 15 {
		t.Fatalf("UserCatalog() clients=%d err=%v", len(clients), err)
	}
	android := clients[0].Downloads[0]
	if android.DownloadURL != "https://panel.example.test/base/client-download/karing/android" ||
		android.CloudURL == nil || *android.CloudURL != "https://panel.example.test/base/client-link/karing/android/cloud" ||
		android.TutorialURL == nil || *android.TutorialURL != "https://panel.example.test/base/client-link/karing/android/tutorial" {
		t.Fatalf("stable routes = %#v", android)
	}
	for _, unsafe := range []string{"http://example.test/app", "javascript:alert(1)", "//example.test/app", "https://user:pass@example.test/app"} {
		if _, err := service.SaveOverrides(context.Background(), admin.Revision, OverrideInput{"karing": {"android": {"cloud": unsafe}}}); err == nil {
			t.Fatalf("unsafe override %q was accepted", unsafe)
		}
	}
	admin, err = service.SaveOverrides(context.Background(), admin.Revision, OverrideInput{"karing": {"android": {
		"direct": "https://downloads.example.test:8443/karing.apk",
	}}})
	if err != nil {
		t.Fatalf("SaveOverrides(HTTPS port) error = %v", err)
	}
	resolved, err := service.Resolve(context.Background(), "karing", "android", "qr")
	if err != nil || resolved != "https://downloads.example.test:8443/karing.apk" {
		t.Fatalf("QR direct fallback = (%q, %v)", resolved, err)
	}
}

func TestConfigCacheRefreshesChangesSavedByAnotherService(t *testing.T) {
	database := testStore(t)
	now := time.Date(2026, 8, 24, 0, 0, 0, 0, time.UTC)
	reader := New(Options{Store: database, PanelURL: "https://panel.example.test", Now: func() time.Time { return now }})
	writer := New(Options{Store: database, PanelURL: "https://panel.example.test", Now: func() time.Time { return now }})

	initial, err := reader.AdminCatalog(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writer.SaveOverrides(context.Background(), initial.Revision, OverrideInput{"karing": {"android": {"cloud": "https://cloud.example.test/app"}}}); err != nil {
		t.Fatal(err)
	}
	stale, err := reader.AdminCatalog(context.Background())
	if err != nil || stale.Revision != initial.Revision {
		t.Fatalf("cached catalog = (%d, %v), want revision %d", stale.Revision, err, initial.Revision)
	}
	now = now.Add(configCacheTTL + time.Millisecond)
	refreshed, err := reader.AdminCatalog(context.Background())
	if err != nil || refreshed.Revision != initial.Revision+1 {
		t.Fatalf("refreshed catalog = (%d, %v), want revision %d", refreshed.Revision, err, initial.Revision+1)
	}
}

func TestGitHubResolutionIsValidatedCachedAndSingleFlight(t *testing.T) {
	database := testStore(t)
	var calls atomic.Int64
	releaseGate := make(chan struct{})
	client := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		calls.Add(1)
		<-releaseGate
		body := `{"assets":[{"name":"karing_1_android_arm64-v8a.apk","browser_download_url":"https://github.com/KaringX/karing/releases/download/v1/karing_1_android_arm64-v8a.apk"}]}`
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
	})
	service := New(Options{Store: database, PanelURL: "https://panel.example.test", HTTPClient: client, Now: time.Now})
	const workers = 12
	results := make(chan string, workers)
	errors := make(chan error, workers)
	var group sync.WaitGroup
	for range workers {
		group.Add(1)
		go func() {
			defer group.Done()
			resolved, err := service.Resolve(context.Background(), "karing", "android", "direct")
			results <- resolved
			errors <- err
		}()
	}
	for calls.Load() == 0 {
		time.Sleep(time.Millisecond)
	}
	close(releaseGate)
	group.Wait()
	close(results)
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
	for result := range results {
		if result != "https://github.com/KaringX/karing/releases/download/v1/karing_1_android_arm64-v8a.apk" {
			t.Fatalf("resolved = %q", result)
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("GitHub calls = %d, want 1", calls.Load())
	}
	if _, err := service.Resolve(context.Background(), "karing", "android", "direct"); err != nil || calls.Load() != 1 {
		t.Fatalf("cached resolve err=%v calls=%d", err, calls.Load())
	}
}

func TestGitHubAssetValidationPinsHostRepositoryAndDefaultHTTPSPort(t *testing.T) {
	valid := "https://github.com/KaringX/karing/releases/download/v1/karing.apk"
	if resolved, err := validateGitHubAssetURL(valid, "KaringX/karing"); err != nil || resolved != valid {
		t.Fatalf("valid asset = (%q, %v)", resolved, err)
	}
	for _, address := range []string{
		"https://github.com/other/repository/releases/download/v1/karing.apk",
		"https://github.com:8443/KaringX/karing/releases/download/v1/karing.apk",
		"https://downloads.example.test/KaringX/karing/releases/download/v1/karing.apk",
	} {
		if _, err := validateGitHubAssetURL(address, "KaringX/karing"); err == nil {
			t.Fatalf("asset URL %q was accepted", address)
		}
	}
}

func TestQRDataUsesStableResolutionRoute(t *testing.T) {
	service := New(Options{Store: testStore(t), PanelURL: "https://panel.example.test", Now: time.Now})
	downloadURL, dataURL, err := service.QRData("karing", "android")
	if err != nil || downloadURL != "https://panel.example.test/client-link/karing/android/qr" || !strings.HasPrefix(dataURL, "data:image/svg+xml;base64,") {
		t.Fatalf("QRData() = (%q, %q, %v)", downloadURL, dataURL, err)
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(dataURL, "data:image/svg+xml;base64,"))
	if err != nil || !strings.HasPrefix(string(decoded), `<svg xmlns="http://www.w3.org/2000/svg"`) || !strings.HasSuffix(string(decoded), `"/></svg>`) {
		prefixLength := min(len(decoded), 40)
		t.Fatalf("QR SVG is invalid: err=%v prefix=%q", err, string(decoded[:prefixLength]))
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) Do(request *http.Request) (*http.Response, error) {
	return function(request)
}

func testStore(t testing.TB) *store.Store {
	t.Helper()
	database, err := store.OpenSQLite("file:" + t.Name() + "?mode=memory&cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if err := database.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	return database
}
