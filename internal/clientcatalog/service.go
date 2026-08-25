package clientcatalog

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

const (
	defaultCacheTTL      = 6 * time.Hour
	failedCacheTTL       = 30 * time.Second
	configCacheTTL       = time.Second
	githubResponseLimit  = 1 << 20
	configuredURLMaxSize = 2_048
)

type Service struct {
	store       *store.Store
	panelURL    string
	httpClient  HTTPDoer
	now         func() time.Time
	cacheTTL    time.Duration
	definitions []Definition
	byID        map[string]Definition

	configMu        sync.RWMutex
	config          *store.ClientCatalogConfig
	configExpiresAt time.Time
	cacheMu         sync.Mutex
	cache           map[string]cacheEntry
	flights         map[string]*flight
}

type cacheEntry struct {
	address   string
	err       error
	expiresAt time.Time
}

type flight struct {
	done    chan struct{}
	address string
	err     error
}

func New(options Options) *Service {
	if options.Store == nil {
		panic("clientcatalog: Store is required")
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.CacheTTL <= 0 {
		options.CacheTTL = defaultCacheTTL
	}
	if options.HTTPClient == nil {
		options.HTTPClient = defaultHTTPClient()
	}
	definitions := DefaultDefinitions()
	byID := make(map[string]Definition, len(definitions))
	for _, definition := range definitions {
		byID[definition.ID] = definition
	}
	return &Service{
		store: options.Store, panelURL: strings.TrimRight(options.PanelURL, "/"), httpClient: options.HTTPClient,
		now: options.Now, cacheTTL: options.CacheTTL, definitions: definitions, byID: byID,
		cache: make(map[string]cacheEntry), flights: make(map[string]*flight),
	}
}

func (s *Service) AdminCatalog(ctx context.Context) (AdminCatalog, error) {
	config, err := s.loadConfig(ctx)
	if err != nil {
		return AdminCatalog{}, err
	}
	return s.adminCatalog(config), nil
}

func (s *Service) SaveOverrides(ctx context.Context, revision int64, input OverrideInput) (AdminCatalog, error) {
	links, err := s.normalizeOverrides(input)
	if err != nil {
		return AdminCatalog{}, err
	}
	config, err := s.store.ReplaceClientCatalogOverrides(ctx, revision, links, s.now())
	if err != nil {
		return AdminCatalog{}, err
	}
	s.setConfig(config)
	return s.adminCatalog(config), nil
}

func (s *Service) UserCatalog(ctx context.Context) ([]UserClient, error) {
	config, err := s.loadConfig(ctx)
	if err != nil {
		return nil, err
	}
	overrides := overrideLookup(config.Links)
	clients := make([]UserClient, 0, len(s.definitions))
	for _, definition := range s.definitions {
		client := UserClient{
			ID: definition.ID, Name: definition.Name, Core: definition.Core, Featured: definition.Featured,
			HWID: true, Description: definition.Description, Downloads: make([]UserDownload, 0, len(definition.Downloads)),
		}
		for _, download := range definition.Downloads {
			links := overrides[definition.ID][download.Platform]
			item := UserDownload{
				Platform: download.Platform, Source: download.Source,
				DownloadURL: s.stableURL("client-download", definition.ID, download.Platform),
			}
			if links["cloud"] != "" {
				address := s.stableURL("client-link", definition.ID, download.Platform, "cloud")
				item.CloudURL = &address
			}
			if links["tutorial"] != "" {
				address := s.stableURL("client-link", definition.ID, download.Platform, "tutorial")
				item.TutorialURL = &address
			}
			client.Downloads = append(client.Downloads, item)
		}
		clients = append(clients, client)
	}
	return clients, nil
}

func (s *Service) Resolve(ctx context.Context, clientID, platform, action string) (string, error) {
	definition, download, err := s.find(clientID, platform)
	if err != nil || !knownAction(action) {
		return "", ErrNotFound
	}
	config, err := s.loadConfig(ctx)
	if err != nil {
		return "", err
	}
	configuredLinks := overrideLookup(config.Links)[clientID][platform]
	configured := configuredLinks[action]
	if configured != "" {
		return configured, nil
	}
	if action == "qr" && configuredLinks["direct"] != "" {
		return configuredLinks["direct"], nil
	}
	if action != "direct" && action != "qr" {
		return "", ErrNotFound
	}
	if download.URL != "" {
		address, err := validateExternalURL(download.URL)
		if err != nil {
			return "", fmt.Errorf("%w: invalid built-in URL for %s", ErrUnavailable, definition.ID)
		}
		return address, nil
	}
	return s.resolveGitHub(ctx, download)
}

func (s *Service) normalizeOverrides(input OverrideInput) ([]store.ClientCatalogOverride, error) {
	return normalizeOverrides(s.byID, input)
}

// NormalizeOverrides applies the same client, platform, action and URL policy
// used by the administrator API without mutating catalog state. Offline legacy
// migration uses it before opening a target write transaction.
func NormalizeOverrides(input OverrideInput) ([]store.ClientCatalogOverride, error) {
	definitions := DefaultDefinitions()
	byID := make(map[string]Definition, len(definitions))
	for _, definition := range definitions {
		byID[definition.ID] = definition
	}
	return normalizeOverrides(byID, input)
}

func normalizeOverrides(byID map[string]Definition, input OverrideInput) ([]store.ClientCatalogOverride, error) {
	links := make([]store.ClientCatalogOverride, 0)
	for clientID, platformValues := range input {
		definition, exists := byID[clientID]
		if !exists || platformValues == nil {
			return nil, fmt.Errorf("%w: unknown client", ErrInvalid)
		}
		for platform, actionValues := range platformValues {
			if _, ok := downloadFor(definition, platform); !ok || actionValues == nil {
				return nil, fmt.Errorf("%w: unsupported platform", ErrInvalid)
			}
			for action, rawAddress := range actionValues {
				if !knownAction(action) {
					return nil, fmt.Errorf("%w: unknown action", ErrInvalid)
				}
				address := strings.TrimSpace(rawAddress)
				if address == "" {
					continue
				}
				validated, err := validateConfiguredURL(address, action)
				if err != nil {
					return nil, fmt.Errorf("%w: %s/%s/%s: %v", ErrInvalid, clientID, platform, action, err)
				}
				links = append(links, store.ClientCatalogOverride{ClientID: clientID, Platform: platform, Action: action, URL: validated})
			}
		}
	}
	sort.Slice(links, func(left, right int) bool {
		if links[left].ClientID != links[right].ClientID {
			return links[left].ClientID < links[right].ClientID
		}
		if links[left].Platform != links[right].Platform {
			return links[left].Platform < links[right].Platform
		}
		return links[left].Action < links[right].Action
	})
	return links, nil
}

func (s *Service) adminCatalog(config store.ClientCatalogConfig) AdminCatalog {
	overrides := overrideLookup(config.Links)
	clients := make([]AdminClient, 0, len(s.definitions))
	for _, definition := range s.definitions {
		client := AdminClient{ID: definition.ID, Name: definition.Name, Core: definition.Core, Platforms: make([]AdminPlatform, 0, len(definition.Downloads))}
		for _, download := range definition.Downloads {
			values := overrides[definition.ID][download.Platform]
			client.Platforms = append(client.Platforms, AdminPlatform{Platform: download.Platform, Links: ActionLinks{
				Direct: values["direct"], QR: values["qr"], Cloud: values["cloud"], Tutorial: values["tutorial"],
			}})
		}
		clients = append(clients, client)
	}
	return AdminCatalog{Revision: config.Revision, Clients: clients}
}

func (s *Service) loadConfig(ctx context.Context) (store.ClientCatalogConfig, error) {
	now := s.now()
	s.configMu.RLock()
	if s.config != nil && now.Before(s.configExpiresAt) {
		config := *s.config
		s.configMu.RUnlock()
		return config, nil
	}
	s.configMu.RUnlock()

	s.configMu.Lock()
	defer s.configMu.Unlock()
	if s.config != nil && now.Before(s.configExpiresAt) {
		return *s.config, nil
	}
	config, err := s.store.GetClientCatalogConfig(ctx)
	if err != nil {
		return store.ClientCatalogConfig{}, err
	}
	stored := config
	s.config = &stored
	s.configExpiresAt = now.Add(configCacheTTL)
	return config, nil
}

func (s *Service) setConfig(config store.ClientCatalogConfig) {
	s.configMu.Lock()
	copy := config
	s.config = &copy
	s.configExpiresAt = s.now().Add(configCacheTTL)
	s.configMu.Unlock()
}

func (s *Service) find(clientID, platform string) (Definition, DownloadDefinition, error) {
	definition, exists := s.byID[clientID]
	if !exists {
		return Definition{}, DownloadDefinition{}, ErrNotFound
	}
	download, exists := downloadFor(definition, platform)
	if !exists {
		return Definition{}, DownloadDefinition{}, ErrNotFound
	}
	return definition, download, nil
}

func downloadFor(definition Definition, platform string) (DownloadDefinition, bool) {
	for _, download := range definition.Downloads {
		if download.Platform == platform {
			return download, true
		}
	}
	return DownloadDefinition{}, false
}

func knownAction(value string) bool {
	for _, action := range actions {
		if value == action {
			return true
		}
	}
	return false
}

func overrideLookup(links []store.ClientCatalogOverride) map[string]map[string]map[string]string {
	lookup := make(map[string]map[string]map[string]string)
	for _, link := range links {
		if lookup[link.ClientID] == nil {
			lookup[link.ClientID] = make(map[string]map[string]string)
		}
		if lookup[link.ClientID][link.Platform] == nil {
			lookup[link.ClientID][link.Platform] = make(map[string]string)
		}
		lookup[link.ClientID][link.Platform][link.Action] = link.URL
	}
	return lookup
}

func (s *Service) stableURL(parts ...string) string {
	var builder strings.Builder
	builder.WriteString(s.panelURL)
	for _, part := range parts {
		builder.WriteByte('/')
		builder.WriteString(url.PathEscape(part))
	}
	return builder.String()
}

func validateConfiguredURL(address, action string) (string, error) {
	if len(address) > configuredURLMaxSize || !utf8.ValidString(address) || strings.IndexFunc(address, unicode.IsControl) >= 0 {
		return "", errors.New("link is invalid")
	}
	if action == "tutorial" && strings.HasPrefix(address, "/") && !strings.HasPrefix(address, "//") && !strings.Contains(address, `\`) {
		return address, nil
	}
	return validateExternalURL(address)
}

func validateExternalURL(address string) (string, error) {
	if address == "" || len(address) > configuredURLMaxSize || !utf8.ValidString(address) || strings.IndexFunc(address, unicode.IsSpace) >= 0 || strings.IndexFunc(address, unicode.IsControl) >= 0 {
		return "", errors.New("link must be a valid HTTPS URL")
	}
	parsed, err := url.ParseRequestURI(address)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil {
		return "", errors.New("link must be a valid HTTPS URL")
	}
	return parsed.String(), nil
}

func (s *Service) resolveGitHub(ctx context.Context, download DownloadDefinition) (string, error) {
	key := download.Repository + "\x00" + download.Platform
	now := s.now()
	s.cacheMu.Lock()
	if cached, exists := s.cache[key]; exists && now.Before(cached.expiresAt) {
		s.cacheMu.Unlock()
		return cached.address, cached.err
	}
	if active, exists := s.flights[key]; exists {
		done := active.done
		s.cacheMu.Unlock()
		select {
		case <-done:
			return active.address, active.err
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	active := &flight{done: make(chan struct{})}
	s.flights[key] = active
	s.cacheMu.Unlock()

	address, err := s.fetchGitHubAsset(ctx, download)
	if err != nil && download.FallbackURL != "" {
		address, err = validateExternalURL(download.FallbackURL)
	}
	if err != nil {
		err = fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	ttl := s.cacheTTL
	if err != nil {
		ttl = failedCacheTTL
	}
	s.cacheMu.Lock()
	active.address, active.err = address, err
	s.cache[key] = cacheEntry{address: address, err: err, expiresAt: s.now().Add(ttl)}
	delete(s.flights, key)
	close(active.done)
	s.cacheMu.Unlock()
	return address, err
}

func (s *Service) fetchGitHubAsset(ctx context.Context, download DownloadDefinition) (string, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.github.com/repos/"+download.Repository+"/releases/latest", nil)
	if err != nil {
		return "", err
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("User-Agent", "Xboard-Go-Client-Catalog/1.0")
	response, err := s.httpClient.Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("GitHub returned status %d", response.StatusCode)
	}
	limited := io.LimitReader(response.Body, githubResponseLimit+1)
	body, err := io.ReadAll(limited)
	if err != nil {
		return "", err
	}
	if len(body) > githubResponseLimit {
		return "", errors.New("GitHub response is too large")
	}
	var payload struct {
		Assets []struct {
			Name string `json:"name"`
			URL  string `json:"browser_download_url"`
		} `json:"assets"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return "", err
	}
	for _, pattern := range download.Patterns {
		expression, err := regexp.Compile(pattern)
		if err != nil {
			return "", fmt.Errorf("invalid built-in asset pattern: %w", err)
		}
		for _, asset := range payload.Assets {
			if expression.MatchString(asset.Name) {
				return validateGitHubAssetURL(asset.URL, download.Repository)
			}
		}
	}
	return "", errors.New("no matching release asset")
}

func validateGitHubAssetURL(address, repository string) (string, error) {
	validated, err := validateExternalURL(address)
	if err != nil {
		return "", err
	}
	parsed, _ := url.Parse(validated)
	expected := "/" + strings.ToLower(repository) + "/releases/download/"
	if !strings.EqualFold(parsed.Hostname(), "github.com") || parsed.Port() != "" || !strings.HasPrefix(strings.ToLower(parsed.Path), expected) {
		return "", errors.New("GitHub asset URL is outside the configured repository")
	}
	return validated, nil
}

func defaultHTTPClient() *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	return &http.Client{
		Transport: transport,
		Timeout:   12 * time.Second,
		CheckRedirect: func(request *http.Request, via []*http.Request) error {
			if len(via) >= 3 || request.URL.Scheme != "https" || !strings.EqualFold(request.URL.Hostname(), "api.github.com") || request.URL.Port() != "" {
				return errors.New("GitHub redirect target is not allowed")
			}
			return nil
		},
	}
}
