package captcha

import (
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

type Provider string

const (
	ProviderRecaptcha   Provider = "recaptcha"
	ProviderRecaptchaV3 Provider = "recaptcha-v3"
	ProviderTurnstile   Provider = "turnstile"

	defaultRecaptchaEndpoint = "https://www.google.com/recaptcha/api/siteverify"
	defaultTurnstileEndpoint = "https://challenges.cloudflare.com/turnstile/v0/siteverify"
	maxTokenBytes            = 4 << 10
	maxSecretBytes           = 4 << 10
	maxResponseBytes         = 64 << 10
)

var (
	ErrInvalid     = errors.New("captcha verification failed")
	ErrUnavailable = errors.New("captcha verification unavailable")
	actionPattern  = regexp.MustCompile(`^[A-Za-z0-9_/-]{1,64}$`)
)

type HTTPDoer interface {
	Do(request *http.Request) (*http.Response, error)
}

type Verifier interface {
	Verify(context.Context, Verification) error
}

type Options struct {
	HTTPClient          HTTPDoer
	RecaptchaEndpoint   string
	RecaptchaV3Endpoint string
	TurnstileEndpoint   string
}

type Verification struct {
	Provider         Provider
	Secret           []byte
	Token            string
	RemoteIP         string
	ExpectedAction   string
	ExpectedHostname string
	ScoreThreshold   float64
}

type Service struct {
	client              HTTPDoer
	recaptchaEndpoint   string
	recaptchaV3Endpoint string
	turnstileEndpoint   string
}

func New(options Options) (*Service, error) {
	client := options.HTTPClient
	if client == nil {
		client = &http.Client{
			Timeout: 5 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return errors.New("captcha verification redirect rejected")
			},
		}
	}
	recaptchaEndpoint := endpointOrDefault(options.RecaptchaEndpoint, defaultRecaptchaEndpoint)
	recaptchaV3Endpoint := endpointOrDefault(options.RecaptchaV3Endpoint, recaptchaEndpoint)
	turnstileEndpoint := endpointOrDefault(options.TurnstileEndpoint, defaultTurnstileEndpoint)
	for _, endpoint := range []string{recaptchaEndpoint, recaptchaV3Endpoint, turnstileEndpoint} {
		parsed, err := url.Parse(endpoint)
		if err != nil || parsed.Host == "" || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.User != nil {
			return nil, errors.New("captcha verification endpoint is invalid")
		}
	}
	return &Service{client: client, recaptchaEndpoint: recaptchaEndpoint, recaptchaV3Endpoint: recaptchaV3Endpoint, turnstileEndpoint: turnstileEndpoint}, nil
}

func (service *Service) Verify(ctx context.Context, input Verification) error {
	if service == nil || service.client == nil || !validInput(input) {
		return ErrInvalid
	}
	endpoint := ""
	switch input.Provider {
	case ProviderRecaptcha:
		endpoint = service.recaptchaEndpoint
	case ProviderRecaptchaV3:
		endpoint = service.recaptchaV3Endpoint
	case ProviderTurnstile:
		endpoint = service.turnstileEndpoint
	default:
		return ErrInvalid
	}
	values := url.Values{"secret": {string(input.Secret)}, "response": {input.Token}}
	if input.RemoteIP != "" {
		values.Set("remoteip", input.RemoteIP)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(values.Encode()))
	if err != nil {
		return fmt.Errorf("%w: construct request", ErrUnavailable)
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Accept", "application/json")
	response, err := service.client.Do(request)
	if err != nil {
		return fmt.Errorf("%w: upstream request", ErrUnavailable)
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("%w: upstream status", ErrUnavailable)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil || len(body) > maxResponseBytes {
		return fmt.Errorf("%w: upstream response", ErrUnavailable)
	}
	var result struct {
		Success  bool     `json:"success"`
		Score    *float64 `json:"score"`
		Action   string   `json:"action"`
		Hostname string   `json:"hostname"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return fmt.Errorf("%w: decode response", ErrUnavailable)
	}
	if !result.Success {
		return ErrInvalid
	}
	if input.ExpectedHostname != "" && !strings.EqualFold(result.Hostname, input.ExpectedHostname) {
		return ErrInvalid
	}
	if input.Provider == ProviderRecaptchaV3 {
		if result.Score == nil || *result.Score < input.ScoreThreshold || result.Action != input.ExpectedAction {
			return ErrInvalid
		}
	}
	if input.Provider == ProviderTurnstile && result.Action != "" && result.Action != input.ExpectedAction {
		return ErrInvalid
	}
	return nil
}

func validInput(input Verification) bool {
	if input.Provider != ProviderRecaptcha && input.Provider != ProviderRecaptchaV3 && input.Provider != ProviderTurnstile {
		return false
	}
	if len(input.Secret) < 1 || len(input.Secret) > maxSecretBytes || len(input.Token) < 1 || len(input.Token) > maxTokenBytes {
		return false
	}
	if input.Provider == ProviderRecaptchaV3 {
		return actionPattern.MatchString(input.ExpectedAction) && input.ScoreThreshold > 0 && input.ScoreThreshold <= 1 && validHostname(input.ExpectedHostname)
	}
	if input.Provider == ProviderTurnstile && input.ExpectedAction != "" {
		return actionPattern.MatchString(input.ExpectedAction) && validHostname(input.ExpectedHostname)
	}
	return validHostname(input.ExpectedHostname)
}

func validHostname(value string) bool {
	if value == "" {
		return true
	}
	return len(value) <= 253 && !strings.ContainsAny(value, "/\\\x00\r\n\t ")
}

func endpointOrDefault(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}
