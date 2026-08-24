package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const maxClientCatalogLinks = 300

func (s *Store) GetClientCatalogConfig(ctx context.Context) (ClientCatalogConfig, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return ClientCatalogConfig{}, fmt.Errorf("begin client catalog read: %w", err)
	}
	defer tx.Rollback()
	config, err := readClientCatalogConfig(ctx, tx)
	if err != nil {
		return ClientCatalogConfig{}, err
	}
	if err := tx.Commit(); err != nil {
		return ClientCatalogConfig{}, fmt.Errorf("commit client catalog read: %w", err)
	}
	return config, nil
}

func (s *Store) ReplaceClientCatalogOverrides(ctx context.Context, revision int64, links []ClientCatalogOverride, now time.Time) (ClientCatalogConfig, error) {
	if revision <= 0 || len(links) > maxClientCatalogLinks {
		return ClientCatalogConfig{}, ErrInvalidInput
	}
	normalized, err := normalizeClientCatalogOverrides(links)
	if err != nil {
		return ClientCatalogConfig{}, err
	}
	defer s.lockWrite()()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ClientCatalogConfig{}, fmt.Errorf("begin client catalog replacement: %w", err)
	}
	defer tx.Rollback()
	var currentRevision int64
	if err := tx.QueryRowContext(ctx, `SELECT revision FROM client_catalog_config WHERE id = 1`).Scan(&currentRevision); err != nil {
		return ClientCatalogConfig{}, fmt.Errorf("read client catalog revision: %w", err)
	}
	if currentRevision != revision {
		return ClientCatalogConfig{}, ErrConflict
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM client_catalog_links`); err != nil {
		return ClientCatalogConfig{}, fmt.Errorf("clear client catalog links: %w", err)
	}
	for _, link := range normalized {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO client_catalog_links (client_id, platform, action, url, updated_at)
			VALUES (?, ?, ?, ?, ?)
		`, link.ClientID, link.Platform, link.Action, link.URL, now.Unix()); err != nil {
			return ClientCatalogConfig{}, fmt.Errorf("insert client catalog link: %w", err)
		}
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE client_catalog_config SET revision = revision + 1, updated_at = ? WHERE id = 1 AND revision = ?
	`, now.Unix(), revision)
	if err != nil {
		return ClientCatalogConfig{}, fmt.Errorf("update client catalog revision: %w", err)
	}
	rows, _ := result.RowsAffected()
	if rows != 1 {
		return ClientCatalogConfig{}, ErrConflict
	}
	config, err := readClientCatalogConfig(ctx, tx)
	if err != nil {
		return ClientCatalogConfig{}, err
	}
	if err := tx.Commit(); err != nil {
		return ClientCatalogConfig{}, fmt.Errorf("commit client catalog replacement: %w", err)
	}
	return config, nil
}

type queryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func readClientCatalogConfig(ctx context.Context, database queryer) (ClientCatalogConfig, error) {
	var config ClientCatalogConfig
	var updatedAt int64
	if err := database.QueryRowContext(ctx, `SELECT revision, updated_at FROM client_catalog_config WHERE id = 1`).Scan(&config.Revision, &updatedAt); err != nil {
		return ClientCatalogConfig{}, fmt.Errorf("read client catalog config: %w", err)
	}
	rows, err := database.QueryContext(ctx, `
		SELECT client_id, platform, action, url FROM client_catalog_links ORDER BY client_id, platform, action
	`)
	if err != nil {
		return ClientCatalogConfig{}, fmt.Errorf("list client catalog links: %w", err)
	}
	defer rows.Close()
	config.Links = make([]ClientCatalogOverride, 0)
	for rows.Next() {
		var link ClientCatalogOverride
		if err := rows.Scan(&link.ClientID, &link.Platform, &link.Action, &link.URL); err != nil {
			return ClientCatalogConfig{}, fmt.Errorf("scan client catalog link: %w", err)
		}
		config.Links = append(config.Links, link)
	}
	if err := rows.Err(); err != nil {
		return ClientCatalogConfig{}, fmt.Errorf("iterate client catalog links: %w", err)
	}
	config.UpdatedAt = time.Unix(updatedAt, 0).UTC()
	return config, nil
}

func normalizeClientCatalogOverrides(links []ClientCatalogOverride) ([]ClientCatalogOverride, error) {
	normalized := make([]ClientCatalogOverride, 0, len(links))
	seen := make(map[string]struct{}, len(links))
	for _, link := range links {
		link.ClientID = strings.TrimSpace(link.ClientID)
		link.Platform = strings.TrimSpace(link.Platform)
		link.Action = strings.TrimSpace(link.Action)
		link.URL = strings.TrimSpace(link.URL)
		if !validClientCatalogID(link.ClientID) || !validClientCatalogPlatform(link.Platform) || !validClientCatalogAction(link.Action) ||
			link.URL == "" || len(link.URL) > 2048 || !utf8.ValidString(link.URL) || strings.IndexFunc(link.URL, unicode.IsControl) >= 0 {
			return nil, ErrInvalidInput
		}
		key := link.ClientID + "\x00" + link.Platform + "\x00" + link.Action
		if _, exists := seen[key]; exists {
			return nil, ErrInvalidInput
		}
		seen[key] = struct{}{}
		normalized = append(normalized, link)
	}
	return normalized, nil
}

func validClientCatalogID(value string) bool {
	if value == "" || len(value) > 64 {
		return false
	}
	for _, character := range value {
		if (character < 'a' || character > 'z') && (character < '0' || character > '9') && character != '-' {
			return false
		}
	}
	return true
}

func validClientCatalogPlatform(value string) bool {
	return value == "android" || value == "ios" || value == "windows" || value == "macos" || value == "linux"
}

func validClientCatalogAction(value string) bool {
	return value == "direct" || value == "qr" || value == "cloud" || value == "tutorial"
}
