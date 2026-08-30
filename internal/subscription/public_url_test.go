package subscription

import (
	"strings"
	"testing"
)

func TestBuildPublicURLUsesConfiguredOriginsAndPreservesDistributorFragment(t *testing.T) {
	config := PublicURLConfig{
		Origins:  "https://one.example.test/root,https://two.example.test",
		AppURL:   "https://panel.example.test",
		PanelURL: "http://127.0.0.1:8080",
		Path:     "feeds",
	}
	for index := 0; index < 100; index++ {
		value, err := BuildPublicURL(config, "0123456789abcdef0123456789abcdef", "order / 1")
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(value, "https://one.example.test/root/feeds/") && !strings.HasPrefix(value, "https://two.example.test/feeds/") {
			t.Fatalf("BuildPublicURL() = %q, want a configured origin", value)
		}
		if !strings.HasSuffix(value, "#order%20/%201") {
			t.Fatalf("BuildPublicURL() fragment = %q", value)
		}
	}
}

func TestBuildPublicURLFallsBackAndForcesHTTPS(t *testing.T) {
	for name, config := range map[string]PublicURLConfig{
		"app URL":   {AppURL: "http://panel.example.test/root/?ignored=1#old", PanelURL: "http://127.0.0.1:8080", Path: "s", ForceHTTPS: true},
		"panel URL": {PanelURL: "http://127.0.0.1:8080/base", Path: "s", ForceHTTPS: true},
	} {
		t.Run(name, func(t *testing.T) {
			value, err := BuildPublicURL(config, "0123456789abcdef0123456789abcdef", "")
			if err != nil {
				t.Fatal(err)
			}
			if !strings.HasPrefix(value, "https://") || strings.Contains(value, "ignored") || strings.Contains(value, "#old") {
				t.Fatalf("BuildPublicURL() = %q", value)
			}
		})
	}
}

func TestBuildPublicURLRejectsTamperedConfiguredOrigins(t *testing.T) {
	for _, origin := range []string{
		"http://external.example.test", "javascript://example.test", "https://user@example.test",
		"https://example.test?query=1", "https://example.test/#fragment", "https://example.test:99999",
	} {
		if _, err := BuildPublicURL(PublicURLConfig{Origins: origin, Path: "s"}, "0123456789abcdef0123456789abcdef", ""); err == nil {
			t.Fatalf("BuildPublicURL(%q) accepted an unsafe origin", origin)
		}
	}
}

func BenchmarkBuildPublicURL(b *testing.B) {
	config := PublicURLConfig{Origins: "https://subscriptions.example.test", Path: "s"}
	b.ReportAllocs()
	for index := 0; index < b.N; index++ {
		if _, err := BuildPublicURL(config, "0123456789abcdef0123456789abcdef", ""); err != nil {
			b.Fatal(err)
		}
	}
}
