package gen

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"strings"
)

const representativeLegacyData = `
INSERT INTO v2_settings (id, name, value, created_at, updated_at) VALUES
    (1, 'app_name', 'Synthetic Xboard', 1700000000, 1700000100),
    (2, 'app_description', 'Anonymous migration fixture', 1700000000, 1700000100),
    (3, 'app_url', 'https://panel.example.test', 1700000000, 1700000100),
    (4, 'tos_url', 'https://panel.example.test/terms', 1700000000, 1700000100),
    (5, 'logo', 'https://cdn.example.test/logo.svg', 1700000000, 1700000100),
    (6, 'client_catalog_links', '{"karing":{"android":{"tutorial":"/guide/10/karing"}}}', 1700000000, 1700000100),
    (7, 'reset_traffic_method', '4', 1700000000, 1700000100),
    (8, 'app_enable_coupon_system', '1', 1700000000, 1700000100),
    (9, 'currency', 'USD', 1700000000, 1700000100),
    (10, 'currency_symbol', '$', 1700000000, 1700000100),
    (11, 'force_https', '1', 1700000000, 1700000100),
    (12, 'subscribe_url', 'https://subscribe.example.test', 1700000000, 1700000100),
    (13, 'safe_mode_enable', '1', 1700000000, 1700000100),
    (14, 'secure_path', 'synthetic-admin', 1700000000, 1700000100);

INSERT INTO v2_notice (id, sort, title, content, show, img_url, tags, created_at, updated_at) VALUES
    (10, 1, 'Synthetic maintenance', 'Synthetic fixture notice.', 1, NULL, '["migration"]', 1700000000, 1700000100),
    (11, 2, 'Hidden synthetic notice', 'Hidden fixture content.', 0, NULL, '[]', 1700000200, 1700000300);

INSERT INTO v2_server_group VALUES
    (5, 'Synthetic Standard', 1700000000, 1700000100),
    (8, 'Synthetic Premium', 1700000200, 1700000300);
INSERT INTO v2_server_route VALUES
    (7, 'Synthetic direct route', '["domain:example.test"]', 'direct', NULL, 1700000000, 1700000100),
    (9, 'Synthetic blocked route', '["domain:blocked.example.test"]', 'block', NULL, 1700000200, 1700000300);

INSERT INTO v2_plan VALUES
    (11, 5, 100, 'Synthetic Monthly', 100, 1, 1, 1, 'Synthetic plan content', NULL, 1000,
     1700000000, 1700000100, '{"monthly":9.99,"yearly":99.90}', 1, 3, '["popular"]'),
    (12, 8, 500, 'Synthetic Premium', NULL, 1, 2, 1, 'Synthetic premium content', 4, NULL,
     1700000200, 1700000300, '{"monthly":19.99}', 1, NULL, '["premium"]');

INSERT INTO v2_user
    (id, invite_user_id, telegram_id, email, password, balance, discount, commission_type,
     commission_rate, commission_balance, u, d, transfer_enable, banned, is_admin, last_login_at,
     is_staff, uuid, group_id, plan_id, speed_limit, remind_expire, remind_traffic, token, expired_at,
     remarks, created_at, updated_at, device_limit, last_online_at, next_reset_at, last_reset_at,
     reset_count, is_distributor)
VALUES
    (1, NULL, NULL, 'admin-one@example.test', '$2y$10$92IXUNpkjO0rOQ5byMi.Ye4oKoEa3Ro9llC/.og/at2uheWG/igi.',
     10000, NULL, 0, NULL, 0, 0, 0, 0, 0, 1, 1700001000, 0,
     '11111111-1111-4111-8111-111111111111', 5, 11, NULL, 1, 1,
     '11111111111111111111111111111111', 1800000000, 'Synthetic administrator',
     1700000000, 1700001000, 3, 1700001000, 1701000000, 1699000000, 1, 0),
    (2, 1, 6000000002, 'member-two@example.test', '$2y$10$92IXUNpkjO0rOQ5byMi.Ye4oKoEa3Ro9llC/.og/at2uheWG/igi.',
     2500, 95, 1, 10, 300, 1024, 2048, 107374182400, 0, 0, NULL, 0,
     '22222222-2222-4222-8222-222222222222', 5, 11, 100, 1, 0,
     '22222222222222222222222222222222', 1800000000, 'Synthetic member',
     1700000200, 1700000300, 2, 1700000300, 1701000000, 1699000000, 1, 0);

INSERT INTO personal_access_tokens
    (id, tokenable_type, tokenable_id, name, token, abilities, last_used_at, expires_at, created_at, updated_at)
VALUES
    (21, 'App\Models\User', 1, 'synthetic-admin-device', '__ACCESS_TOKEN_HASH__', '["*"]', 1700001000, NULL, 1700000000, 1700001000),
    (22, 'App\Models\User', 2, 'synthetic-member-device', '__ACCESS_TOKEN_HASH_2__', '["*"]', NULL, 1800000000, 1700000200, 1700000300);

INSERT INTO v2_invite_code VALUES
    (31, 1, 'SynA1234', 0, 2, 1700000000, 1700000100),
    (32, 1, 'SynB5678', 1, 4, 1700000200, 1700000300);

INSERT INTO v2_coupon VALUES
    (41, 'SYN500', 'Synthetic fixed coupon', 1, 500, 1, 10, 1, '[11]', '["monthly"]', 1690000000, 1800000000, 1690000000, 1690000100),
    (42, 'SYN10PCT', 'Synthetic percent coupon', 2, 10, 1, NULL, NULL, NULL, NULL, 1690000000, 1800000000, 1690000200, 1690000300);

INSERT INTO v2_payment VALUES
    (51, 'synpay51', 'CoinPayments', 'Synthetic CoinPayments', 'https://cdn.example.test/payment.svg',
     '{"coinpayments_merchant_id":"synthetic-merchant","coinpayments_ipn_secret":"synthetic-ipn-secret","coinpayments_currency":"USD"}',
     'https://notify.example.test', 25, 1.5, 1, 1, 1700000000, 1700000100);

INSERT INTO v2_order VALUES
    (61, 1, 2, 11, 41, 51, 1, 'monthly', '2026090312000000000000061', 'synthetic-callback-61',
     524, 25, 450, 0, 0, 0, '[]', 3, 0, 0, NULL, 1700001000, 1700000000, 1700001000,
     NULL, 1700000000, 1800000000, NULL, NULL),
    (62, NULL, 2, 12, NULL, NULL, 1, 'monthly', '2026090312000000000000062', NULL,
     1999, NULL, 0, 0, 0, 0, '[]', 0, 0, 0, NULL, NULL, 1700002000, 1700002000,
     NULL, 1800000000, 1900000000, NULL, NULL);

INSERT INTO v2_ticket VALUES
    (71, 2, 'Synthetic closed ticket', 2, 1, 1, 1700000000, 1700000200, 1),
    (72, 2, 'Synthetic open ticket', 0, 0, 0, 1700000300, 1700000300, NULL);
INSERT INTO v2_ticket_message VALUES
    (81, 2, 71, 'Synthetic member question.', 1700000000, 1700000000),
    (82, 1, 71, 'Synthetic administrator answer.', 1700000200, 1700000200),
    (83, 2, 72, 'Synthetic waiting message.', 1700000300, 1700000300);

INSERT INTO v2_server_machine VALUES
    (91, 'synthetic-machine', 'synthetic-machine-token', 'Synthetic edge', 1, 1700000200,
     '{"cpu":12}', 1700000000, 1700000200);
INSERT INTO v2_server_machine_load_history VALUES
    (92, 91, 12.5, 8589934592, 2147483648, 107374182400, 21474836480, 1024, 2048, 1700000200);
INSERT INTO v2_server VALUES
    (101, 'vless', 'synthetic-edge', NULL, '["5"]', '[7]', 'Synthetic VLESS edge', 1.25,
     '["premium"]', 'edge.example.test', '443', 8443,
     '{"tls":1,"flow":"xtls-rprx-vision","encryption":{"enabled":false},"tls_settings":{"server_name":"edge.example.test"}}',
     1, 1, 1700000000, 1700000200, 1, '[{"start":"08:00","end":"09:00","rate":2}]',
     '[]', '[]', '{}', 1000000, 5, 7, 91, 1);
INSERT INTO v2_server_activation_schedule VALUES
    (111, 101, 'daily', 'Asia/Singapore', 28800, 72000, 0, 0, 'synthetic-schedule-revision',
     1700003600, 0, 1700000000, NULL, 1700000000, 1700000200);
`

func (g *Generator) populateDomains(ctx context.Context, db *sql.DB) error {
	first := sha256.Sum256([]byte(fmt.Sprintf("synthetic-access-token-%d-1", g.cfg.Seed)))
	second := sha256.Sum256([]byte(fmt.Sprintf("synthetic-access-token-%d-2", g.cfg.Seed)))
	data := representativeLegacyData
	data, err := replaceRequiredOnce(data, "__ACCESS_TOKEN_HASH__", hex.EncodeToString(first[:]))
	if err != nil {
		return err
	}
	data, err = replaceRequiredOnce(data, "__ACCESS_TOKEN_HASH_2__", hex.EncodeToString(second[:]))
	if err != nil {
		return err
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, data); err != nil {
		return err
	}
	rows, err := countRepresentativeDomains(ctx, tx)
	if err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	g.rows = rows
	return nil
}

func countRepresentativeDomains(ctx context.Context, tx *sql.Tx) (map[string]int, error) {
	domains := []struct {
		name  string
		table string
	}{
		{"settings", "v2_settings"},
		{"notices", "v2_notice"},
		{"server_groups", "v2_server_group"},
		{"server_routes", "v2_server_route"},
		{"plans", "v2_plan"},
		{"human_users", "v2_user"},
		{"access_tokens", "personal_access_tokens"},
		{"invitation_codes", "v2_invite_code"},
		{"coupons", "v2_coupon"},
		{"payments", "v2_payment"},
		{"orders", "v2_order"},
		{"tickets", "v2_ticket"},
		{"ticket_messages", "v2_ticket_message"},
		{"server_machines", "v2_server_machine"},
		{"server_machine_load_history", "v2_server_machine_load_history"},
		{"servers", "v2_server"},
		{"server_activation_schedules", "v2_server_activation_schedule"},
	}
	rows := make(map[string]int, len(domains))
	for _, domain := range domains {
		var count int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM "`+domain.table+`"`).Scan(&count); err != nil {
			return nil, fmt.Errorf("count representative domain %s: %w", domain.name, err)
		}
		rows[domain.name] = count
	}
	return rows, nil
}

func replaceRequiredOnce(input, marker, replacement string) (string, error) {
	if strings.Count(input, marker) != 1 {
		return "", fmt.Errorf("representative data marker %q must occur exactly once", marker)
	}
	return strings.Replace(input, marker, replacement, 1), nil
}
