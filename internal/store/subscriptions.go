package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	yaml "go.yaml.in/yaml/v3"
)

const maxSubscriptionTemplateBytes = 1 << 20

var subscriptionPathPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

func (s *Store) GetSubscriptionSettings(ctx context.Context) (SubscriptionSettings, error) {
	settings, err := readSubscriptionSettings(ctx, s.db)
	if err != nil {
		return SubscriptionSettings{}, fmt.Errorf("get subscription settings: %w", err)
	}
	return settings, nil
}

// GetSubscriptionRenderConfig projects the immutable inputs needed by a
// single subscription response. Keeping this to one row avoids loading all
// site settings and all six potentially large templates on the hot path.
func (s *Store) GetSubscriptionRenderConfig(ctx context.Context, templateName string) (SubscriptionRenderConfig, error) {
	if templateName != "" {
		valid := false
		for _, name := range SubscriptionTemplateNames {
			if templateName == name {
				valid = true
				break
			}
		}
		if !valid {
			return SubscriptionRenderConfig{}, ErrInvalidInput
		}
	}
	var config SubscriptionRenderConfig
	var content string
	if err := s.db.QueryRowContext(ctx, `
		SELECT subscription_settings.path, subscription_settings.show_info, subscription_settings.show_protocol,
		       app_settings.app_name, app_settings.app_url, app_settings.force_https, app_settings.subscribe_url,
		       COALESCE(subscription_templates.content, '')
		FROM subscription_settings
		CROSS JOIN app_settings
		LEFT JOIN subscription_templates ON subscription_templates.name = ?
		WHERE subscription_settings.id = 1 AND app_settings.id = 1
	`, templateName).Scan(&config.Path, &config.ShowInfo, &config.ShowProtocol, &config.AppName, &config.AppURL, &config.ForceHTTPS, &config.SubscribeURL, &content); err != nil {
		return SubscriptionRenderConfig{}, fmt.Errorf("get subscription render config: %w", err)
	}
	config.Templates = make(map[string]string, 1)
	if templateName != "" {
		config.Templates[templateName] = content
	}
	return config, nil
}

func (s *Store) UpdateSubscriptionSettings(ctx context.Context, administratorID, revision int64, input SaveSubscriptionSettingsInput, now time.Time) (SubscriptionSettings, error) {
	if administratorID < 1 || revision < 1 || now.Unix() < 0 {
		return SubscriptionSettings{}, ErrInvalidInput
	}
	normalized, err := normalizeSubscriptionSettings(input)
	if err != nil {
		return SubscriptionSettings{}, err
	}
	defer s.lockWrite()()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return SubscriptionSettings{}, fmt.Errorf("begin subscription settings update: %w", err)
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `
		UPDATE subscription_settings
		SET path = ?, show_info = ?, show_protocol = ?, updated_by = ?, updated_at = ?, revision = revision + 1
		WHERE id = 1 AND revision = ?
	`, normalized.Path, normalized.ShowInfo, normalized.ShowProtocol, administratorID, now.Unix(), revision)
	if err != nil {
		return SubscriptionSettings{}, fmt.Errorf("update subscription settings: %w", err)
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return SubscriptionSettings{}, fmt.Errorf("count subscription settings update: %w", err)
	}
	if updated != 1 {
		return SubscriptionSettings{}, ErrRevisionConflict
	}
	statement, err := tx.PrepareContext(ctx, `UPDATE subscription_templates SET content = ?, updated_at = ? WHERE name = ?`)
	if err != nil {
		return SubscriptionSettings{}, fmt.Errorf("prepare subscription template update: %w", err)
	}
	defer statement.Close()
	for _, name := range SubscriptionTemplateNames {
		result, err := statement.ExecContext(ctx, normalized.Templates[name], now.Unix(), name)
		if err != nil {
			return SubscriptionSettings{}, fmt.Errorf("update subscription template %s: %w", name, err)
		}
		count, err := result.RowsAffected()
		if err != nil || count != 1 {
			return SubscriptionSettings{}, fmt.Errorf("update subscription template %s: unexpected row count", name)
		}
	}
	settings, err := readSubscriptionSettings(ctx, tx)
	if err != nil {
		return SubscriptionSettings{}, fmt.Errorf("read updated subscription settings: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return SubscriptionSettings{}, fmt.Errorf("commit subscription settings update: %w", err)
	}
	return settings, nil
}

func normalizeSubscriptionSettings(input SaveSubscriptionSettingsInput) (SaveSubscriptionSettingsInput, error) {
	input.Path = strings.TrimSpace(input.Path)
	if !subscriptionPathPattern.MatchString(input.Path) || len(input.Templates) != len(SubscriptionTemplateNames) {
		return SaveSubscriptionSettingsInput{}, fmt.Errorf("%w: invalid subscription settings", ErrInvalidInput)
	}
	normalized := make(map[string]string, len(SubscriptionTemplateNames))
	for _, name := range SubscriptionTemplateNames {
		content, exists := input.Templates[name]
		if !exists || !utf8.ValidString(content) || strings.IndexByte(content, 0) >= 0 || len(content) > maxSubscriptionTemplateBytes {
			return SaveSubscriptionSettingsInput{}, fmt.Errorf("%w: invalid subscription template %s", ErrInvalidInput, name)
		}
		if err := validateSubscriptionTemplate(name, content); err != nil {
			return SaveSubscriptionSettingsInput{}, fmt.Errorf("%w: invalid subscription template %s: %v", ErrInvalidInput, name, err)
		}
		normalized[name] = content
	}
	input.Templates = normalized
	return input, nil
}

func validateSubscriptionTemplate(name, content string) error {
	if strings.TrimSpace(content) == "" {
		return nil
	}
	switch name {
	case "singbox":
		decoder := json.NewDecoder(strings.NewReader(content))
		decoder.UseNumber()
		var document map[string]any
		if err := decoder.Decode(&document); err != nil || document == nil {
			return errors.New("must be a JSON object")
		}
		if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
			return errors.New("must contain one JSON object")
		}
		if value, exists := document["outbounds"]; exists {
			if _, ok := value.([]any); !ok {
				return errors.New("outbounds must be an array")
			}
		}
	case "clash", "clashmeta", "stash":
		var document map[string]any
		if err := yaml.Unmarshal([]byte(content), &document); err != nil || document == nil {
			return errors.New("must be a YAML object")
		}
		for _, field := range []string{"proxies", "proxy-groups", "rules"} {
			if value, exists := document[field]; exists {
				if _, ok := value.([]any); !ok {
					return fmt.Errorf("%s must be an array", field)
				}
			}
		}
	}
	return nil
}

type subscriptionSettingsQuery interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func readSubscriptionSettings(ctx context.Context, query subscriptionSettingsQuery) (SubscriptionSettings, error) {
	var settings SubscriptionSettings
	var updatedAt int64
	if err := query.QueryRowContext(ctx, `
		SELECT revision,path,show_info,show_protocol,updated_at FROM subscription_settings WHERE id = 1
	`).Scan(&settings.Revision, &settings.Path, &settings.ShowInfo, &settings.ShowProtocol, &updatedAt); err != nil {
		return SubscriptionSettings{}, err
	}
	settings.UpdatedAt = time.Unix(updatedAt, 0)
	settings.Templates = make(map[string]string, len(SubscriptionTemplateNames))
	rows, err := query.QueryContext(ctx, `SELECT name,content FROM subscription_templates ORDER BY name`)
	if err != nil {
		return SubscriptionSettings{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var name, content string
		if err := rows.Scan(&name, &content); err != nil {
			return SubscriptionSettings{}, err
		}
		settings.Templates[name] = content
	}
	if err := rows.Err(); err != nil {
		return SubscriptionSettings{}, err
	}
	if len(settings.Templates) != len(SubscriptionTemplateNames) {
		return SubscriptionSettings{}, errors.New("subscription template set is incomplete")
	}
	return settings, nil
}

func (s *Store) FindSubscriptionAccount(ctx context.Context, token string) (SubscriptionAccount, error) {
	if !validSubscriptionToken(token) {
		return SubscriptionAccount{}, ErrNotFound
	}
	return scanSubscriptionAccount(s.db.QueryRowContext(ctx, `
		SELECT id,email,uuid,group_id,plan_id,transfer_enable,traffic_u,traffic_d,expired_at,next_reset_at,speed_limit,device_limit,banned,subscription_token,created_at
		FROM users WHERE subscription_token = ? AND account_kind IN ('human', 'internal_subscription')
	`, token))
}

func (s *Store) GetSubscriptionAccount(ctx context.Context, userID int64) (SubscriptionAccount, error) {
	if userID < 1 {
		return SubscriptionAccount{}, ErrNotFound
	}
	return scanSubscriptionAccount(s.db.QueryRowContext(ctx, `
		SELECT id,email,uuid,group_id,plan_id,transfer_enable,traffic_u,traffic_d,expired_at,next_reset_at,speed_limit,device_limit,banned,subscription_token,created_at
		FROM users WHERE id = ? AND account_kind = 'human'
	`, userID))
}

func scanSubscriptionAccount(row *sql.Row) (SubscriptionAccount, error) {
	var account SubscriptionAccount
	var uuid sql.NullString
	var groupID, planID, expiredAt, nextResetAt sql.NullInt64
	var createdAt int64
	err := row.Scan(&account.ID, &account.Email, &uuid, &groupID, &planID, &account.TransferEnable,
		&account.TrafficUpload, &account.TrafficDownload, &expiredAt, &nextResetAt, &account.SpeedLimit,
		&account.DeviceLimit, &account.Banned, &account.SubscriptionToken, &createdAt)
	if errors.Is(err, sql.ErrNoRows) {
		return SubscriptionAccount{}, ErrNotFound
	}
	if err != nil {
		return SubscriptionAccount{}, fmt.Errorf("find subscription account: %w", err)
	}
	account.UUID = uuid.String
	account.GroupID = nullableInt64Pointer(groupID)
	account.PlanID = nullableInt64Pointer(planID)
	if expiredAt.Valid {
		value := time.Unix(expiredAt.Int64, 0)
		account.ExpiredAt = &value
	}
	if nextResetAt.Valid {
		value := time.Unix(nextResetAt.Int64, 0)
		account.NextResetAt = &value
	}
	account.CreatedAt = time.Unix(createdAt, 0)
	return account, nil
}

func (s *Store) ResetSubscriptionSecurity(ctx context.Context, userID int64, now time.Time) (SubscriptionAccount, SubscriptionSecurityMutation, error) {
	if userID < 1 || now.Unix() < 0 {
		return SubscriptionAccount{}, SubscriptionSecurityMutation{}, ErrInvalidInput
	}
	newToken, err := newSubscriptionToken()
	if err != nil {
		return SubscriptionAccount{}, SubscriptionSecurityMutation{}, err
	}
	newUUID := uuid.NewString()
	defer s.lockWrite()()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return SubscriptionAccount{}, SubscriptionSecurityMutation{}, fmt.Errorf("begin subscription security reset: %w", err)
	}
	defer tx.Rollback()
	before, err := scanSubscriptionAccount(tx.QueryRowContext(ctx, `
		SELECT id,email,uuid,group_id,plan_id,transfer_enable,traffic_u,traffic_d,expired_at,next_reset_at,speed_limit,device_limit,banned,subscription_token,created_at
		FROM users WHERE id = ? AND account_kind = 'human'
	`, userID))
	if err != nil {
		return SubscriptionAccount{}, SubscriptionSecurityMutation{}, err
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE users
		SET uuid = ?, subscription_token = ?, online_count = 0, admin_revision = admin_revision + 1, updated_at = ?
		WHERE id = ? AND account_kind = 'human'
	`, newUUID, newToken, now.Unix(), userID)
	if err != nil {
		return SubscriptionAccount{}, SubscriptionSecurityMutation{}, fmt.Errorf("reset subscription security: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return SubscriptionAccount{}, SubscriptionSecurityMutation{}, fmt.Errorf("count subscription security reset: %w", err)
	}
	if changed != 1 {
		return SubscriptionAccount{}, SubscriptionSecurityMutation{}, ErrNotFound
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM node_device_ips WHERE user_id = ?`, userID); err != nil {
		return SubscriptionAccount{}, SubscriptionSecurityMutation{}, fmt.Errorf("clear subscription devices: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM node_user_online WHERE user_id = ?`, userID); err != nil {
		return SubscriptionAccount{}, SubscriptionSecurityMutation{}, fmt.Errorf("clear subscription online state: %w", err)
	}
	updated, err := scanSubscriptionAccount(tx.QueryRowContext(ctx, `
		SELECT id,email,uuid,group_id,plan_id,transfer_enable,traffic_u,traffic_d,expired_at,next_reset_at,speed_limit,device_limit,banned,subscription_token,created_at
		FROM users WHERE id = ? AND account_kind = 'human'
	`, userID))
	if err != nil {
		return SubscriptionAccount{}, SubscriptionSecurityMutation{}, err
	}
	if err := tx.Commit(); err != nil {
		return SubscriptionAccount{}, SubscriptionSecurityMutation{}, fmt.Errorf("commit subscription security reset: %w", err)
	}
	return updated, SubscriptionSecurityMutation{PreviousUUID: before.UUID, GroupID: cloneInt64(before.GroupID)}, nil
}

func validSubscriptionToken(token string) bool {
	if len(token) != 32 {
		return false
	}
	for _, character := range token {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}

func (s *Store) ListSubscriptionNodes(ctx context.Context, groupID int64) ([]SubscriptionNode, error) {
	if groupID < 1 {
		return nil, ErrInvalidInput
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT n.id,n.type,COALESCE(d.external_code,''),d.parent_id,n.name,d.tags_json,n.host,n.port,d.server_port,
		       d.protocol_settings_json,n.show,n.enabled,n.sort,d.transfer_enable,n.traffic_u,n.traffic_d,
		       d.configured_rate_micros,n.created_at,parent.created_at,d.rate_time_enabled,d.rate_time_ranges_json,
		       d.custom_outbounds_json,d.custom_routes_json,d.cert_config_json
		FROM node_group_memberships membership
		JOIN nodes n ON n.id = membership.node_id
		JOIN node_protocol_definitions d ON d.node_id = n.id
		LEFT JOIN nodes parent ON parent.id = d.parent_id
		WHERE membership.group_id = ? AND n.show = 1
		  AND (d.transfer_enable = 0 OR (n.traffic_u < d.transfer_enable AND n.traffic_d < d.transfer_enable - n.traffic_u))
		ORDER BY n.sort,n.id
	`, groupID)
	if err != nil {
		return nil, fmt.Errorf("list subscription nodes: %w", err)
	}
	defer rows.Close()
	nodes := make([]SubscriptionNode, 0)
	for rows.Next() {
		var node SubscriptionNode
		var tagsJSON string
		var protocolSettingsJSON, rateTimeRangesJSON, customOutboundsJSON, customRoutesJSON, certificateConfigJSON string
		var parentID, parentCreatedAt sql.NullInt64
		var createdAt int64
		var rateMicros int64
		if err := rows.Scan(&node.ID, &node.Type, &node.ExternalCode, &parentID, &node.Name, &tagsJSON, &node.Host,
			&node.Port, &node.ServerPort, &protocolSettingsJSON, &node.Show, &node.Enabled, &node.Sort,
			&node.TransferEnable, &node.TrafficUpload, &node.TrafficDownload, &rateMicros, &createdAt,
			&parentCreatedAt, &node.RateTimeEnabled, &rateTimeRangesJSON, &customOutboundsJSON,
			&customRoutesJSON, &certificateConfigJSON); err != nil {
			return nil, fmt.Errorf("scan subscription node: %w", err)
		}
		if err := json.Unmarshal([]byte(tagsJSON), &node.Tags); err != nil {
			return nil, fmt.Errorf("decode subscription node tags: %w", err)
		}
		node.ProtocolSettings = json.RawMessage(protocolSettingsJSON)
		node.RateTimeRanges = json.RawMessage(rateTimeRangesJSON)
		node.CustomOutbounds = json.RawMessage(customOutboundsJSON)
		node.CustomRoutes = json.RawMessage(customRoutesJSON)
		node.CertificateConfig = json.RawMessage(certificateConfigJSON)
		node.ParentID = nullableInt64Pointer(parentID)
		node.CreatedAt = time.Unix(createdAt, 0)
		if parentCreatedAt.Valid {
			value := time.Unix(parentCreatedAt.Int64, 0)
			node.ParentCreatedAt = &value
		}
		node.ConfiguredRate = float64(rateMicros) / 1_000_000
		nodes = append(nodes, node)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate subscription nodes: %w", err)
	}
	return nodes, nil
}
