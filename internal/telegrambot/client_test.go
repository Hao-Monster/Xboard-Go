package telegrambot

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const testBotToken = "123456789:abcdefghijklmnopqrstuvwxyzABCDE"

func TestClientGetMeAndSetWebhookKeepSecretsInExpectedBoundaries(t *testing.T) {
	var methods []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost || request.Header.Get("Content-Type") != "application/json" {
			t.Fatalf("request = %s content-type=%q", request.Method, request.Header.Get("Content-Type"))
		}
		methods = append(methods, request.URL.Path)
		switch {
		case strings.HasSuffix(request.URL.Path, "/getMe"):
			_, _ = w.Write([]byte(`{"ok":true,"result":{"id":987,"username":"xboard_test_bot"}}`))
		case strings.HasSuffix(request.URL.Path, "/setWebhook"):
			var body struct {
				URL            string   `json:"url"`
				SecretToken    string   `json:"secret_token"`
				AllowedUpdates []string `json:"allowed_updates"`
			}
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil || body.URL != "https://panel.example.test/api/v1/guest/telegram/webhook" || body.SecretToken != "safe_secret-123" || len(body.AllowedUpdates) != 2 {
				t.Fatalf("setWebhook body = %#v, err=%v", body, err)
			}
			_, _ = w.Write([]byte(`{"ok":true,"result":true}`))
		}
	}))
	defer server.Close()
	client, err := New(Options{HTTPClient: server.Client(), APIBaseURL: server.URL, AllowInsecure: true})
	if err != nil {
		t.Fatal(err)
	}
	identity, err := client.GetMe(context.Background(), []byte(testBotToken))
	if err != nil || identity.ID != 987 || identity.Username != "xboard_test_bot" {
		t.Fatalf("GetMe() = %#v, %v", identity, err)
	}
	if err := client.SetWebhook(context.Background(), []byte(testBotToken), "https://panel.example.test/api/v1/guest/telegram/webhook", []byte("safe_secret-123")); err != nil {
		t.Fatalf("SetWebhook() error = %v", err)
	}
	if len(methods) != 2 || !strings.Contains(methods[0], testBotToken) {
		t.Fatalf("method paths = %#v", methods)
	}
}

func TestClientRegistersFixedCommandsAndSendsBoundedPlainText(t *testing.T) {
	var methods []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		methods = append(methods, request.URL.Path)
		switch {
		case strings.HasSuffix(request.URL.Path, "/setMyCommands"):
			var body struct {
				Commands []BotCommand `json:"commands"`
				Scope    struct {
					Type string `json:"type"`
				} `json:"scope"`
			}
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil || body.Scope.Type != "default" || len(body.Commands) != 5 ||
				body.Commands[0] != (BotCommand{Command: "start", Description: "开始使用"}) ||
				body.Commands[4] != (BotCommand{Command: "unbind", Description: "解绑账号"}) {
				t.Fatalf("setMyCommands body=%#v err=%v", body, err)
			}
		case strings.HasSuffix(request.URL.Path, "/sendMessage"):
			var body map[string]any
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil || body["chat_id"] != float64(778899) || body["text"] != "plain _user_ text" {
				t.Fatalf("sendMessage body=%#v err=%v", body, err)
			}
			if _, exists := body["parse_mode"]; exists {
				t.Fatalf("sendMessage enabled markup parsing: %#v", body)
			}
		}
		_, _ = w.Write([]byte(`{"ok":true,"result":true}`))
	}))
	defer server.Close()
	client, err := New(Options{HTTPClient: server.Client(), APIBaseURL: server.URL, AllowInsecure: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := client.SetMyCommands(t.Context(), []byte(testBotToken), FixedCommands()); err != nil {
		t.Fatalf("SetMyCommands() error=%v", err)
	}
	if err := client.SendMessage(t.Context(), []byte(testBotToken), 778899, "plain _user_ text"); err != nil {
		t.Fatalf("SendMessage() error=%v", err)
	}
	if len(methods) != 2 {
		t.Fatalf("methods=%#v", methods)
	}
	for _, invalid := range []string{"", "contains\x00nul", strings.Repeat("x", 4097)} {
		if err := client.SendMessage(t.Context(), []byte(testBotToken), 778899, invalid); !errors.Is(err, ErrInvalid) {
			t.Fatalf("SendMessage(%q) error=%v", invalid, err)
		}
	}
}

func TestClientJoinRequestsAndRejectsMalformedInputs(t *testing.T) {
	var called []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		called = append(called, request.URL.Path)
		_, _ = w.Write([]byte(`{"ok":true,"result":true}`))
	}))
	defer server.Close()
	client, _ := New(Options{HTTPClient: server.Client(), APIBaseURL: server.URL, AllowInsecure: true})
	if err := client.ApproveChatJoinRequest(t.Context(), []byte(testBotToken), -100123, 42); err != nil {
		t.Fatal(err)
	}
	if err := client.DeclineChatJoinRequest(t.Context(), []byte(testBotToken), -100123, 43); err != nil {
		t.Fatal(err)
	}
	if len(called) != 2 || !strings.HasSuffix(called[0], "/approveChatJoinRequest") || !strings.HasSuffix(called[1], "/declineChatJoinRequest") {
		t.Fatalf("calls = %#v", called)
	}
	if err := client.SetWebhook(t.Context(), []byte("bad"), "https://panel.example.test/hook", []byte("safe_secret")); !errors.Is(err, ErrInvalid) {
		t.Fatalf("bad token error = %v", err)
	}
	if err := client.SetWebhook(t.Context(), []byte(testBotToken), "http://panel.example.test/hook", []byte("safe_secret")); !errors.Is(err, ErrInvalid) {
		t.Fatalf("insecure webhook error = %v", err)
	}
}

func TestClientMapsUpstreamFailuresWithoutLeakingDescriptions(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"ok":false,"description":"secret upstream details"}`))
	}))
	defer server.Close()
	client, _ := New(Options{HTTPClient: server.Client(), APIBaseURL: server.URL, AllowInsecure: true})
	_, err := client.GetMe(t.Context(), []byte(testBotToken))
	if !errors.Is(err, ErrRejected) || strings.Contains(err.Error(), "secret upstream details") {
		t.Fatalf("GetMe() error = %v", err)
	}
}

func TestClientRejectsMalformedAndOversizedUpstreamResponses(t *testing.T) {
	for name, response := range map[string]struct {
		status int
		body   string
	}{
		"non-2xx":        {status: http.StatusBadGateway, body: `upstream-secret-details`},
		"malformed JSON": {status: http.StatusOK, body: `{not-json`},
		"oversized":      {status: http.StatusOK, body: strings.Repeat("x", maxResponseBytes+1)},
		"missing result": {status: http.StatusOK, body: `{"ok":true,"result":null}`},
	} {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(response.status)
				_, _ = io.WriteString(w, response.body)
			}))
			defer server.Close()
			client, err := New(Options{HTTPClient: server.Client(), APIBaseURL: server.URL, AllowInsecure: true})
			if err != nil {
				t.Fatal(err)
			}
			_, err = client.GetMe(t.Context(), []byte(testBotToken))
			if !errors.Is(err, ErrUnavailable) || strings.Contains(err.Error(), testBotToken) || strings.Contains(err.Error(), "upstream-secret-details") {
				t.Fatalf("GetMe() error=%v", err)
			}
		})
	}
}

func TestClientRejectsUnsafeBaseURLsRedirectsAndLeakyTransportErrors(t *testing.T) {
	for _, baseURL := range []string{
		"http://api.telegram.test", "https://user:pass@api.telegram.test", "https://api.telegram.test?token=value", "javascript:alert(1)",
	} {
		if _, err := New(Options{APIBaseURL: baseURL}); err == nil {
			t.Fatalf("New(%q) accepted an unsafe base URL", baseURL)
		}
	}
	redirectTarget := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"ok":true,"result":{"id":1,"username":"redirect_bot"}}`)
	}))
	defer redirectTarget.Close()
	redirectSource := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		http.Redirect(w, request, redirectTarget.URL, http.StatusTemporaryRedirect)
	}))
	defer redirectSource.Close()
	client, err := New(Options{APIBaseURL: redirectSource.URL, AllowInsecure: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.GetMe(t.Context(), []byte(testBotToken)); !errors.Is(err, ErrUnavailable) || strings.Contains(err.Error(), testBotToken) {
		t.Fatalf("redirect GetMe() error=%v", err)
	}

	leaky := telegramHTTPDoerFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("transport leaked " + testBotToken)
	})
	client, err = New(Options{HTTPClient: leaky})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.GetMe(t.Context(), []byte(testBotToken)); !errors.Is(err, ErrUnavailable) || strings.Contains(err.Error(), testBotToken) {
		t.Fatalf("transport GetMe() error=%v", err)
	}
}

type telegramHTTPDoerFunc func(*http.Request) (*http.Response, error)

func (function telegramHTTPDoerFunc) Do(request *http.Request) (*http.Response, error) {
	return function(request)
}
