package telegrambot

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

const (
	defaultAPIBaseURL = "https://api.telegram.org"
	maxResponseBytes  = 1 << 20
)

var (
	ErrInvalid     = errors.New("Telegram request is invalid")
	ErrRejected    = errors.New("Telegram rejected the request")
	ErrUnavailable = errors.New("Telegram is unavailable")
	botTokenRE     = regexp.MustCompile(`^[1-9][0-9]{4,19}:[0-9A-Za-z_-]{20,128}$`)
	secretTokenRE  = regexp.MustCompile(`^[0-9A-Za-z_-]{1,256}$`)
	usernameRE     = regexp.MustCompile(`^[0-9A-Za-z_]{5,64}$`)
)

type HTTPDoer interface {
	Do(*http.Request) (*http.Response, error)
}

type Options struct {
	HTTPClient    HTTPDoer
	APIBaseURL    string
	AllowInsecure bool
}

type BotIdentity struct {
	ID       int64  `json:"id"`
	Username string `json:"username"`
}

type Client interface {
	GetMe(context.Context, []byte) (BotIdentity, error)
	SetWebhook(context.Context, []byte, string, []byte) error
	ApproveChatJoinRequest(context.Context, []byte, int64, int64) error
	DeclineChatJoinRequest(context.Context, []byte, int64, int64) error
}

type Service struct {
	client  HTTPDoer
	baseURL string
}

func ValidBotToken(token []byte) bool {
	return botTokenRE.Match(token)
}

func New(options Options) (*Service, error) {
	client := options.HTTPClient
	if client == nil {
		client = &http.Client{
			Timeout: 10 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return errors.New("Telegram API redirect rejected")
			},
		}
	}
	baseURL := strings.TrimRight(strings.TrimSpace(options.APIBaseURL), "/")
	if baseURL == "" {
		baseURL = defaultAPIBaseURL
	}
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Opaque != "" ||
		(parsed.Scheme != "https" && (!options.AllowInsecure || parsed.Scheme != "http")) {
		return nil, errors.New("Telegram API base URL is invalid")
	}
	return &Service{client: client, baseURL: baseURL}, nil
}

func (service *Service) GetMe(ctx context.Context, token []byte) (BotIdentity, error) {
	var identity BotIdentity
	if err := service.call(ctx, token, "getMe", struct{}{}, &identity); err != nil {
		return BotIdentity{}, err
	}
	if identity.ID < 1 || !usernameRE.MatchString(identity.Username) || !strings.HasSuffix(strings.ToLower(identity.Username), "bot") {
		return BotIdentity{}, ErrRejected
	}
	return identity, nil
}

func (service *Service) SetWebhook(ctx context.Context, token []byte, webhookURL string, secretToken []byte) error {
	parsed, err := url.Parse(webhookURL)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" || parsed.Opaque != "" || len(webhookURL) > 2_048 || !secretTokenRE.Match(secretToken) {
		return ErrInvalid
	}
	return service.call(ctx, token, "setWebhook", struct {
		URL                string   `json:"url"`
		SecretToken        string   `json:"secret_token"`
		AllowedUpdates     []string `json:"allowed_updates"`
		DropPendingUpdates bool     `json:"drop_pending_updates"`
	}{
		URL: webhookURL, SecretToken: string(secretToken),
		AllowedUpdates: []string{"message", "chat_join_request"}, DropPendingUpdates: false,
	}, nil)
}

func (service *Service) ApproveChatJoinRequest(ctx context.Context, token []byte, chatID, userID int64) error {
	return service.joinRequest(ctx, token, "approveChatJoinRequest", chatID, userID)
}

func (service *Service) DeclineChatJoinRequest(ctx context.Context, token []byte, chatID, userID int64) error {
	return service.joinRequest(ctx, token, "declineChatJoinRequest", chatID, userID)
}

func (service *Service) joinRequest(ctx context.Context, token []byte, method string, chatID, userID int64) error {
	if chatID == 0 || userID < 1 {
		return ErrInvalid
	}
	return service.call(ctx, token, method, struct {
		ChatID int64 `json:"chat_id"`
		UserID int64 `json:"user_id"`
	}{ChatID: chatID, UserID: userID}, nil)
}

func (service *Service) call(ctx context.Context, token []byte, method string, input, output any) error {
	if service == nil || service.client == nil || !ValidBotToken(token) {
		return ErrInvalid
	}
	body, err := json.Marshal(input)
	if err != nil {
		return ErrInvalid
	}
	endpoint := service.baseURL + "/bot" + string(token) + "/" + method
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("%w: construct request", ErrUnavailable)
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Content-Type", "application/json")
	response, err := service.client.Do(request)
	if err != nil {
		return fmt.Errorf("%w: upstream request", ErrUnavailable)
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("%w: upstream status", ErrUnavailable)
	}
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil || len(responseBody) > maxResponseBytes {
		return fmt.Errorf("%w: upstream response", ErrUnavailable)
	}
	var envelope struct {
		OK     bool            `json:"ok"`
		Result json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(responseBody, &envelope); err != nil {
		return fmt.Errorf("%w: decode response", ErrUnavailable)
	}
	if !envelope.OK {
		return ErrRejected
	}
	if output != nil {
		if len(envelope.Result) == 0 || string(envelope.Result) == "null" || json.Unmarshal(envelope.Result, output) != nil {
			return fmt.Errorf("%w: decode result", ErrUnavailable)
		}
	}
	return nil
}
