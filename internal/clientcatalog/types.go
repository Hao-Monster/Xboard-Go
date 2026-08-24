package clientcatalog

import (
	"errors"
	"net/http"
	"time"

	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

var (
	ErrInvalid     = errors.New("invalid client catalog input")
	ErrNotFound    = errors.New("client catalog item not found")
	ErrUnavailable = errors.New("client download unavailable")
)

var platforms = [...]string{"android", "ios", "windows", "macos", "linux"}
var actions = [...]string{"direct", "qr", "cloud", "tutorial"}

type DownloadDefinition struct {
	Platform    string
	Source      string
	URL         string
	Repository  string
	Patterns    []string
	FallbackURL string
}

type Definition struct {
	ID          string
	Name        string
	Core        string
	Description string
	Featured    bool
	Downloads   []DownloadDefinition
}

type ActionLinks struct {
	Direct   string `json:"direct"`
	QR       string `json:"qr"`
	Cloud    string `json:"cloud"`
	Tutorial string `json:"tutorial"`
}

type AdminPlatform struct {
	Platform string      `json:"platform"`
	Links    ActionLinks `json:"links"`
}

type AdminClient struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Core      string          `json:"core"`
	Platforms []AdminPlatform `json:"platforms"`
}

type AdminCatalog struct {
	Revision int64         `json:"revision"`
	Clients  []AdminClient `json:"clients"`
}

type UserDownload struct {
	Platform    string  `json:"platform"`
	Source      string  `json:"source"`
	DownloadURL string  `json:"download_url"`
	CloudURL    *string `json:"cloud_url"`
	TutorialURL *string `json:"tutorial_url"`
}

type UserClient struct {
	ID          string         `json:"id"`
	Name        string         `json:"name"`
	Core        string         `json:"core"`
	Featured    bool           `json:"featured"`
	HWID        bool           `json:"hwid"`
	Description string         `json:"description"`
	Downloads   []UserDownload `json:"downloads"`
}

type OverrideInput map[string]map[string]map[string]string

type HTTPDoer interface {
	Do(*http.Request) (*http.Response, error)
}

type Options struct {
	Store      *store.Store
	PanelURL   string
	HTTPClient HTTPDoer
	Now        func() time.Time
	CacheTTL   time.Duration
}
