package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

const schemaV66PlanVisibility = `
CREATE TABLE IF NOT EXISTS plan_visibility_users (
 plan_id INTEGER NOT NULL REFERENCES plans(id) ON DELETE CASCADE,
 audience TEXT NOT NULL CHECK(audience IN ('customer','distributor')),
 user_id INTEGER NOT NULL CHECK(user_id > 0),
 PRIMARY KEY(plan_id,audience,user_id)
);
CREATE INDEX IF NOT EXISTS idx_plan_visibility_users_user ON plan_visibility_users(user_id,audience);
CREATE INDEX IF NOT EXISTS idx_traffic_reset_logs_global_time ON traffic_reset_logs(reset_at DESC,id DESC);
CREATE TABLE IF NOT EXISTS legacy_user_history (
 user_id INTEGER PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
 legacy_time INTEGER NOT NULL CHECK(legacy_time>=0),
 last_login_ip TEXT NOT NULL CHECK(length(last_login_ip)<=45),
 online_count INTEGER NOT NULL CHECK(online_count>=0)
);
`

func migratePlanVisibility(ctx context.Context, tx *sql.Tx) error {
	for _, column := range []struct{ name, ddl string }{
		{"customer_visibility", `ALTER TABLE plans ADD COLUMN customer_visibility TEXT NOT NULL DEFAULT 'all' CHECK(customer_visibility IN ('all','selected'))`},
		{"distributor_visibility", `ALTER TABLE plans ADD COLUMN distributor_visibility TEXT NOT NULL DEFAULT 'all' CHECK(distributor_visibility IN ('all','selected','none'))`},
	} {
		var exists bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pragma_table_info('plans') WHERE name=?)`, column.name).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			if _, err := tx.ExecContext(ctx, column.ddl); err != nil {
				return err
			}
		}
	}
	var hasResetMethod bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pragma_table_info('traffic_reset_logs') WHERE name='reset_method')`).Scan(&hasResetMethod); err != nil {
		return err
	}
	if !hasResetMethod {
		if _, err := tx.ExecContext(ctx, `ALTER TABLE traffic_reset_logs ADD COLUMN reset_method INTEGER CHECK(reset_method BETWEEN 0 AND 4)`); err != nil {
			return err
		}
	}
	_, err := tx.ExecContext(ctx, schemaV66PlanVisibility)
	return err
}

// Memberships intentionally have no user FK: legacy plans are imported before
// human users. Runtime authorization always uses the authenticated user's role.
type SavePlanVisibilityInput struct {
	PlanID                int64   `json:"plan_id"`
	CustomerVisibility    string  `json:"customer_visibility"`
	DistributorVisibility string  `json:"distributor_visibility"`
	CustomerUserIDs       []int64 `json:"customer_user_ids"`
	DistributorUserIDs    []int64 `json:"distributor_user_ids"`
}
type PlanAudienceUser struct {
	ID              int64  `json:"id"`
	Email           string `json:"email"`
	DistributorName string `json:"distributor_name,omitempty"`
	Banned          bool   `json:"banned"`
}
type PlanVisibility struct {
	ID                    int64              `json:"id"`
	Name                  string             `json:"name"`
	CustomerVisibility    string             `json:"customer_visibility"`
	DistributorVisibility string             `json:"distributor_visibility"`
	CustomerUsers         []PlanAudienceUser `json:"customer_users"`
	DistributorUsers      []PlanAudienceUser `json:"distributor_users"`
}

func validatePlanVisibilityInput(in SavePlanVisibilityInput) error {
	if in.PlanID < 1 || (in.CustomerVisibility != "all" && in.CustomerVisibility != "selected") || (in.DistributorVisibility != "all" && in.DistributorVisibility != "selected" && in.DistributorVisibility != "none") || in.CustomerUserIDs == nil || in.DistributorUserIDs == nil {
		return ErrInvalidInput
	}
	for _, ids := range [][]int64{in.CustomerUserIDs, in.DistributorUserIDs} {
		if len(ids) > 5000 {
			return ErrInvalidInput
		}
		seen := map[int64]bool{}
		for _, id := range ids {
			if id < 1 || seen[id] {
				return ErrInvalidInput
			}
			seen[id] = true
		}
	}
	return nil
}
func (s *Store) SavePlanVisibility(ctx context.Context, in SavePlanVisibilityInput, now time.Time) error {
	if err := validatePlanVisibilityInput(in); err != nil {
		return err
	}
	defer s.lockWrite()()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for audience, ids := range map[string][]int64{"customer": in.CustomerUserIDs, "distributor": in.DistributorUserIDs} {
		for _, id := range ids {
			var valid bool
			if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE id=? AND account_kind='human' AND is_distributor=? AND NOT EXISTS(SELECT 1 FROM user_lifecycles lc WHERE lc.user_id=users.id AND lc.deactivated_at IS NOT NULL))`, id, audience == "distributor").Scan(&valid); err != nil {
				return err
			}
			if !valid {
				return fmt.Errorf("%w: recipient does not belong to audience", ErrInvalidInput)
			}
		}
	}
	result, err := tx.ExecContext(ctx, `UPDATE plans SET customer_visibility=?,distributor_visibility=?,revision=revision+1,updated_at=? WHERE id=?`, in.CustomerVisibility, in.DistributorVisibility, now.Unix(), in.PlanID)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM plan_visibility_users WHERE plan_id=?`, in.PlanID); err != nil {
		return err
	}
	for audience, ids := range map[string][]int64{"customer": in.CustomerUserIDs, "distributor": in.DistributorUserIDs} {
		for _, id := range ids {
			if _, err = tx.ExecContext(ctx, `INSERT INTO plan_visibility_users(plan_id,audience,user_id) VALUES(?,?,?)`, in.PlanID, audience, id); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}
func (s *Store) ListPlanVisibility(ctx context.Context) ([]PlanVisibility, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,name,customer_visibility,distributor_visibility FROM plans ORDER BY sort_position,id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []PlanVisibility{}
	for rows.Next() {
		var p PlanVisibility
		if err = rows.Scan(&p.ID, &p.Name, &p.CustomerVisibility, &p.DistributorVisibility); err != nil {
			return nil, err
		}
		result = append(result, p)
	}
	return result, rows.Err()
}
func (s *Store) GetPlanVisibility(ctx context.Context, id int64) (PlanVisibility, error) {
	var p PlanVisibility
	err := s.db.QueryRowContext(ctx, `SELECT id,name,customer_visibility,distributor_visibility FROM plans WHERE id=?`, id).Scan(&p.ID, &p.Name, &p.CustomerVisibility, &p.DistributorVisibility)
	if err == sql.ErrNoRows {
		return p, ErrNotFound
	}
	if err != nil {
		return p, err
	}
	p.CustomerUsers = []PlanAudienceUser{}
	p.DistributorUsers = []PlanAudienceUser{}
	rows, err := s.db.QueryContext(ctx, `SELECT v.audience,u.id,u.email,COALESCE(u.distributor_name,''),u.banned FROM plan_visibility_users v JOIN users u ON u.id=v.user_id WHERE v.plan_id=? AND u.account_kind='human' ORDER BY v.audience,u.id`, id)
	if err != nil {
		return p, err
	}
	defer rows.Close()
	for rows.Next() {
		var a string
		var u PlanAudienceUser
		if err = rows.Scan(&a, &u.ID, &u.Email, &u.DistributorName, &u.Banned); err != nil {
			return p, err
		}
		if a == "customer" {
			p.CustomerUsers = append(p.CustomerUsers, u)
		} else {
			p.DistributorUsers = append(p.DistributorUsers, u)
		}
	}
	return p, rows.Err()
}
func (s *Store) SearchPlanAudienceUsers(ctx context.Context, audience, q string) ([]PlanAudienceUser, error) {
	if (audience != "customer" && audience != "distributor") || !utf8.ValidString(q) || utf8.RuneCountInString(q) < 2 || utf8.RuneCountInString(q) > 255 {
		return nil, ErrInvalidInput
	}
	pattern := "%" + strings.NewReplacer(`\`, `\\`, "%", `\%`, "_", `\_`).Replace(q) + "%"
	rows, err := s.db.QueryContext(ctx, `SELECT id,email,COALESCE(distributor_name,''),banned FROM users WHERE account_kind='human' AND NOT EXISTS(SELECT 1 FROM user_lifecycles lc WHERE lc.user_id=users.id AND lc.deactivated_at IS NOT NULL) AND is_distributor=? AND (email LIKE ? ESCAPE '\' OR distributor_name LIKE ? ESCAPE '\') ORDER BY email,id LIMIT 20`, audience == "distributor", pattern, pattern)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []PlanAudienceUser{}
	for rows.Next() {
		var u PlanAudienceUser
		if err = rows.Scan(&u.ID, &u.Email, &u.DistributorName, &u.Banned); err != nil {
			return nil, err
		}
		result = append(result, u)
	}
	return result, rows.Err()
}
func requirePlanAudience(ctx context.Context, q planQueryer, planID, userID int64) error {
	var allowed bool
	err := q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM plans p JOIN users u ON u.id=? WHERE p.id=? AND u.account_kind='human' AND ((CASE WHEN u.is_distributor=1 THEN p.distributor_visibility ELSE p.customer_visibility END)='all' OR ((CASE WHEN u.is_distributor=1 THEN p.distributor_visibility ELSE p.customer_visibility END)='selected' AND EXISTS(SELECT 1 FROM plan_visibility_users v WHERE v.plan_id=p.id AND v.user_id=u.id AND v.audience=CASE WHEN u.is_distributor=1 THEN 'distributor' ELSE 'customer' END))))`, userID, planID).Scan(&allowed)
	if err != nil {
		return err
	}
	if !allowed {
		return ErrPlanUnavailable
	}
	return nil
}
