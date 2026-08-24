package knowledge

import (
	"encoding/base64"
	"net/url"
	"strings"
	"sync"
	"testing"
)

func TestUserContentMatchesLegacySubscriptionAndPlaceholderRules(t *testing.T) {
	const body = "# {{siteName}}\n\n{{subscribeUrl}}\n\n{{urlEncodeSubscribeUrl}}\n\n{{safeBase64SubscribeUrl}}\n\n<!--access start-->private one<!--access end-->\n\n<!--access start-->private two<!--access end-->"
	const subscriptionURL = "https://panel.example.test/api/v1/client/subscribe?token=0123456789abcdef0123456789abcdef" // gitleaks:allow -- deterministic fake test fixture

	active := UserContent(body, "Xboard-Go", subscriptionURL, true)
	if !strings.Contains(active, "private one") || !strings.Contains(active, subscriptionURL) {
		t.Fatalf("active user content = %q", active)
	}
	if strings.Contains(active, "<!--access") {
		t.Fatalf("active content retained rendering control markers: %q", active)
	}
	if !strings.Contains(active, url.QueryEscape(subscriptionURL)) {
		t.Fatalf("active content is missing URL encoded subscription URL: %q", active)
	}
	wantBase64 := base64.RawURLEncoding.EncodeToString([]byte(subscriptionURL))
	if !strings.Contains(active, wantBase64) {
		t.Fatalf("active content is missing URL-safe base64 subscription URL: %q", active)
	}

	inactive := UserContent(body, "Xboard-Go", subscriptionURL, false)
	if strings.Contains(inactive, "private one") || strings.Contains(inactive, "private two") {
		t.Fatalf("inactive user received subscription-only content: %q", inactive)
	}
	if got := strings.Count(inactive, NoSubscriptionMessage); got != 2 {
		t.Fatalf("inactive replacement count = %d, want 2; body=%q", got, inactive)
	}
}

func TestPublicContentNeverReceivesPrivateSubscriptionVariables(t *testing.T) {
	body := "{{siteName}} {{subscribeUrl}} {{urlEncodeSubscribeUrl}} {{safeBase64SubscribeUrl}}"
	result := PublicContent(body, "Xboard-Go", "https://panel.example.test/")
	for _, placeholder := range []string{"{{siteName}}", "{{subscribeUrl}}", "{{urlEncodeSubscribeUrl}}", "{{safeBase64SubscribeUrl}}"} {
		if strings.Contains(result, placeholder) {
			t.Fatalf("public content retained placeholder %q: %q", placeholder, result)
		}
	}
	if !strings.Contains(result, "Xboard-Go") || !strings.Contains(result, "https://panel.example.test/") {
		t.Fatalf("public content did not use the site/login fallback: %q", result)
	}
}

func TestRenderPublicSanitizesUnsafeHTMLAndBuildsUniqueTOC(t *testing.T) {
	markdown := `# Setup

## Client

## Client

<script>alert(1)</script>

<img src="https://images.example.test/a.png" onerror="alert(2)">

<img src="data:text/html;base64,PHNjcmlwdD4=" style="position:fixed">

<svg><a href="javascript:alert(4)">svg</a></svg>

<form action="https://evil.example.test"><input name="password"></form>

<a href="javascript:alert(3)" target="_blank">unsafe</a>

[safe](https://docs.example.test)`
	document, err := RenderPublic(markdown)
	if err != nil {
		t.Fatalf("RenderPublic() error = %v", err)
	}
	for _, forbidden := range []string{"<script", "onerror", "javascript:", "data:text", "style=", "<svg", "<form", "<input"} {
		if strings.Contains(strings.ToLower(document.HTML), forbidden) {
			t.Fatalf("rendered HTML retained %q: %s", forbidden, document.HTML)
		}
	}
	if !strings.Contains(document.HTML, `loading="lazy"`) || !strings.Contains(document.HTML, `href="https://docs.example.test"`) {
		t.Fatalf("rendered HTML lost allowed image/link behavior: %s", document.HTML)
	}
	if len(document.TOC) != 3 || document.TOC[0].ID != "setup" || document.TOC[1].ID != "client" || document.TOC[2].ID != "client-1" {
		t.Fatalf("TOC = %#v", document.TOC)
	}
}

func TestRenderPublicPolicyIsSafeForConcurrentRequests(t *testing.T) {
	const workers = 32
	var wait sync.WaitGroup
	errors := make(chan error, workers)
	for range workers {
		wait.Add(1)
		go func() {
			defer wait.Done()
			document, err := RenderPublic("# Concurrent\n\n<img src=\"https://images.example.test/a.png\" onerror=\"alert(1)\">")
			if err != nil {
				errors <- err
				return
			}
			if strings.Contains(document.HTML, "onerror") || !strings.Contains(document.HTML, `loading="lazy"`) {
				errors <- &unexpectedConcurrentRender{html: document.HTML}
			}
		}()
	}
	wait.Wait()
	close(errors)
	for err := range errors {
		t.Fatal(err)
	}
}

type unexpectedConcurrentRender struct {
	html string
}

func (e *unexpectedConcurrentRender) Error() string {
	return "unexpected concurrent render output: " + e.html
}

func TestSlugIsStableAndHasSafeFallback(t *testing.T) {
	if got := Slug("  Desktop Setup Guide  "); got != "desktop-setup-guide" {
		t.Fatalf("Slug(ascii) = %q", got)
	}
	if got := Slug("使用指南"); got != "article" {
		t.Fatalf("Slug(non-ascii) = %q", got)
	}
}
