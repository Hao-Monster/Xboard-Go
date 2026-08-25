package legacymigration

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

const maxLegacySubscriptionTemplateDataBytes = int64(len(store.SubscriptionTemplateNames) * (1 << 20))

type SubscriptionConfigSnapshot struct {
	Path     string
	Size     int64
	SHA256   string
	Config   store.LegacySubscriptionConfig
	Checksum string
}

func ReadSubscriptionConfigSnapshot(ctx context.Context, sourcePath string) (SubscriptionConfigSnapshot, error) {
	config := store.LegacySubscriptionConfig{
		Path: "s", Templates: emptyLegacySubscriptionTemplates(),
	}
	identity, err := readLegacySnapshot(ctx, sourcePath, func(database *sql.DB) error {
		if err := requireRealTable(ctx, database, "v2_settings", []string{"name", "value"}); err != nil {
			return err
		}
		if err := validateLegacyQueryBudget(ctx, database, `
			SELECT COUNT(*), COALESCE(SUM(length(CAST(name AS BLOB)) + COALESCE(length(CAST(value AS BLOB)), 0)), 0)
			FROM v2_settings
			WHERE name IN ('subscribe_path', 'show_info_to_server_enable', 'show_protocol_to_server_enable')
		`, 3, 4096, "legacy subscription settings"); err != nil {
			return err
		}
		var readErr error
		config.Path, config.ShowInfo, config.ShowProtocol, readErr = readLegacySubscriptionSettings(ctx, database)
		if readErr != nil {
			return readErr
		}
		config.Templates, readErr = readLegacySubscriptionTemplates(ctx, database)
		if readErr != nil {
			return readErr
		}
		if err := store.ValidateLegacySubscriptionConfigData(config); err != nil {
			return fmt.Errorf("validate legacy subscription config: %w", err)
		}
		return nil
	})
	if err != nil {
		return SubscriptionConfigSnapshot{}, err
	}
	return SubscriptionConfigSnapshot{
		Path: identity.Path, Size: identity.Size, SHA256: identity.SHA256,
		Config: config, Checksum: store.LegacySubscriptionConfigChecksum(config),
	}, nil
}

func readLegacySubscriptionSettings(ctx context.Context, database *sql.DB) (string, bool, bool, error) {
	rows, err := database.QueryContext(ctx, `
		SELECT name,value FROM v2_settings
		WHERE name IN ('subscribe_path', 'show_info_to_server_enable', 'show_protocol_to_server_enable')
		ORDER BY name
	`)
	if err != nil {
		return "", false, false, fmt.Errorf("read legacy subscription settings: %w", err)
	}
	defer rows.Close()
	path := "s"
	showInfo, showProtocol := false, false
	seen := make(map[string]struct{}, 3)
	for rows.Next() {
		var name string
		var value sql.NullString
		if err := rows.Scan(&name, &value); err != nil {
			return "", false, false, fmt.Errorf("scan legacy subscription setting: %w", err)
		}
		if _, exists := seen[name]; exists {
			return "", false, false, fmt.Errorf("legacy subscription settings contain duplicate %q rows", name)
		}
		seen[name] = struct{}{}
		switch name {
		case "subscribe_path":
			if value.Valid {
				path = value.String
			}
		case "show_info_to_server_enable":
			showInfo = legacyPHPBool(value)
		case "show_protocol_to_server_enable":
			showProtocol = legacyPHPBool(value)
		}
	}
	if err := rows.Err(); err != nil {
		return "", false, false, fmt.Errorf("iterate legacy subscription settings: %w", err)
	}
	return path, showInfo, showProtocol, nil
}

func legacyPHPBool(value sql.NullString) bool {
	return value.Valid && value.String != "" && value.String != "0"
}

func readLegacySubscriptionTemplates(ctx context.Context, database *sql.DB) (map[string]string, error) {
	var objectType string
	err := database.QueryRowContext(ctx, `SELECT type FROM sqlite_schema WHERE name = 'v2_subscribe_templates'`).Scan(&objectType)
	if errors.Is(err, sql.ErrNoRows) {
		// Older Xboard snapshots used packaged template files. Empty overrides
		// select Xboard-Go's equivalent built-in renderers.
		return emptyLegacySubscriptionTemplates(), nil
	}
	if err != nil {
		return nil, fmt.Errorf("inspect legacy subscription templates: %w", err)
	}
	if objectType != "table" {
		return nil, errors.New("legacy snapshot object \"v2_subscribe_templates\" must be a real table")
	}
	if err := requireRealTable(ctx, database, "v2_subscribe_templates", []string{"name", "content"}); err != nil {
		return nil, err
	}
	if err := validateLegacyQueryBudget(ctx, database, `
		SELECT COUNT(*), COALESCE(SUM(length(CAST(name AS BLOB)) + COALESCE(length(CAST(content AS BLOB)), 0)), 0)
		FROM v2_subscribe_templates
	`, len(store.SubscriptionTemplateNames), maxLegacySubscriptionTemplateDataBytes, "legacy subscription templates"); err != nil {
		return nil, err
	}
	rows, err := database.QueryContext(ctx, `SELECT name,content FROM v2_subscribe_templates ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("read legacy subscription templates: %w", err)
	}
	defer rows.Close()
	result := make(map[string]string, len(store.SubscriptionTemplateNames))
	allowed := make(map[string]struct{}, len(store.SubscriptionTemplateNames))
	for _, name := range store.SubscriptionTemplateNames {
		allowed[name] = struct{}{}
	}
	for rows.Next() {
		var name string
		var content sql.NullString
		if err := rows.Scan(&name, &content); err != nil {
			return nil, fmt.Errorf("scan legacy subscription template: %w", err)
		}
		if _, exists := allowed[name]; !exists {
			return nil, fmt.Errorf("legacy subscription templates contain unsupported name %q", name)
		}
		if _, exists := result[name]; exists {
			return nil, fmt.Errorf("legacy subscription templates contain duplicate name %q", name)
		}
		result[name] = content.String
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate legacy subscription templates: %w", err)
	}
	if len(result) != len(store.SubscriptionTemplateNames) {
		return nil, errors.New("legacy subscription template set is incomplete")
	}
	return result, nil
}

func emptyLegacySubscriptionTemplates() map[string]string {
	result := make(map[string]string, len(store.SubscriptionTemplateNames))
	for _, name := range store.SubscriptionTemplateNames {
		result[name] = ""
	}
	return result
}
