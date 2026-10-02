package store

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// PurchaseChannels contains public storefront and widget identifiers, never API credentials.
type PurchaseChannels struct {
	Revision             int64  `json:"revision"`
	CardStoreURL         string `json:"card_store_url"`
	ChatwootBaseURL      string `json:"chatwoot_base_url"`
	ChatwootWebsiteToken string `json:"chatwoot_website_token"`
}

const schemaV67PurchaseChannels = `
CREATE TABLE IF NOT EXISTS purchase_channels (
 id INTEGER PRIMARY KEY CHECK(id = 1),
 revision INTEGER NOT NULL DEFAULT 1 CHECK(revision > 0),
 card_store_url TEXT NOT NULL DEFAULT '',
 chatwoot_base_url TEXT NOT NULL DEFAULT '',
 chatwoot_website_token TEXT NOT NULL DEFAULT '',
 updated_by INTEGER REFERENCES users(id),
 updated_at INTEGER NOT NULL DEFAULT 0
);
`

var chatwootWebsiteTokenPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

func (s *Store) GetPurchaseChannels(ctx context.Context) (PurchaseChannels, error) {
	return readPurchaseChannels(ctx, s.db)
}

func readPurchaseChannels(ctx context.Context, query interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}) (PurchaseChannels, error) {
	var result PurchaseChannels
	err := query.QueryRowContext(ctx, `SELECT revision, card_store_url, chatwoot_base_url, chatwoot_website_token FROM purchase_channels WHERE id = 1`).Scan(&result.Revision, &result.CardStoreURL, &result.ChatwootBaseURL, &result.ChatwootWebsiteToken)
	if err != nil {
		return PurchaseChannels{}, fmt.Errorf("read purchase channels: %w", err)
	}
	return result, nil
}

func normalizePurchaseChannels(input PurchaseChannels) (PurchaseChannels, error) {
	input.CardStoreURL = strings.TrimSpace(input.CardStoreURL)
	input.ChatwootBaseURL = strings.TrimSpace(input.ChatwootBaseURL)
	input.ChatwootWebsiteToken = strings.TrimSpace(input.ChatwootWebsiteToken)
	if strings.HasPrefix(input.ChatwootBaseURL, "https://") && strings.HasSuffix(input.ChatwootBaseURL, "/") {
		input.ChatwootBaseURL = strings.TrimSuffix(input.ChatwootBaseURL, "/")
	}
	if input.Revision < 1 || !validPurchaseChannelURL(input.CardStoreURL, false) || !validPurchaseChannelURL(input.ChatwootBaseURL, true) {
		return PurchaseChannels{}, ErrInvalidInput
	}
	if (input.ChatwootBaseURL == "") != (input.ChatwootWebsiteToken == "") || (input.ChatwootWebsiteToken != "" && !chatwootWebsiteTokenPattern.MatchString(input.ChatwootWebsiteToken)) {
		return PurchaseChannels{}, ErrInvalidInput
	}
	return input, nil
}

func validPurchaseChannelURL(value string, originOnly bool) bool {
	if value == "" {
		return true
	}
	if len(value) > 2048 || !utf8.ValidString(value) || strings.ContainsAny(value, "\\#") || strings.IndexFunc(value, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) >= 0 {
		return false
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" || parsed.User != nil || parsed.Opaque != "" || parsed.Fragment != "" {
		return false
	}
	if parsed.Port() != "" {
		port, err := strconv.Atoi(parsed.Port())
		if err != nil || port < 1 || port > 65535 {
			return false
		}
	}
	if originOnly && (parsed.Path != "" || parsed.RawQuery != "" || parsed.ForceQuery) {
		return false
	}
	return true
}

func (s *Store) UpdatePurchaseChannels(ctx context.Context, administratorID int64, input PurchaseChannels, now time.Time) (PurchaseChannels, error) {
	normalized, err := normalizePurchaseChannels(input)
	if err != nil {
		return PurchaseChannels{}, err
	}
	if administratorID < 1 || now.Unix() < 0 {
		return PurchaseChannels{}, ErrInvalidInput
	}
	defer s.lockWrite()()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return PurchaseChannels{}, fmt.Errorf("begin purchase channels update: %w", err)
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `UPDATE purchase_channels SET card_store_url = ?, chatwoot_base_url = ?, chatwoot_website_token = ?, revision = revision + 1, updated_by = ?, updated_at = ? WHERE id = 1 AND revision = ?`, normalized.CardStoreURL, normalized.ChatwootBaseURL, normalized.ChatwootWebsiteToken, administratorID, now.Unix(), normalized.Revision)
	if err != nil {
		return PurchaseChannels{}, fmt.Errorf("update purchase channels: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return PurchaseChannels{}, err
	}
	if changed != 1 {
		return PurchaseChannels{}, ErrConflict
	}
	updated, err := readPurchaseChannels(ctx, tx)
	if err != nil {
		return PurchaseChannels{}, err
	}
	if err := tx.Commit(); err != nil {
		return PurchaseChannels{}, fmt.Errorf("commit purchase channels: %w", err)
	}
	return updated, nil
}
