package store

import (
	"context"
	"database/sql"
	"fmt"
)

func legacyPlanVisibilityInput(p LegacyPlan) SavePlanVisibilityInput {
	c, d := p.CustomerVisibility, p.DistributorVisibility
	if c == "" {
		c = "all"
	}
	if d == "" {
		d = "all"
	}
	cu, du := p.CustomerUserIDs, p.DistributorUserIDs
	if cu == nil {
		cu = []int64{}
	}
	if du == nil {
		du = []int64{}
	}
	return SavePlanVisibilityInput{PlanID: p.ID, CustomerVisibility: c, DistributorVisibility: d, CustomerUserIDs: cu, DistributorUserIDs: du}
}
func importLegacyPlanVisibility(ctx context.Context, tx *sql.Tx, p LegacyPlan) error {
	in := legacyPlanVisibilityInput(p)
	if err := validatePlanVisibilityInput(in); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE plans SET customer_visibility=?,distributor_visibility=? WHERE id=?`, in.CustomerVisibility, in.DistributorVisibility, p.ID); err != nil {
		return err
	}
	for a, ids := range map[string][]int64{"customer": in.CustomerUserIDs, "distributor": in.DistributorUserIDs} {
		for _, id := range ids {
			if _, err := tx.ExecContext(ctx, `INSERT INTO plan_visibility_users(plan_id,audience,user_id) VALUES(?,?,?)`, p.ID, a, id); err != nil {
				return err
			}
		}
	}
	return nil
}
func readTargetPlanAudience(ctx context.Context, db queryer, plans []LegacyPlan) error {
	index := map[int64]int{}
	for i := range plans {
		index[plans[i].ID] = i
	}
	rows, err := db.QueryContext(ctx, `SELECT id,customer_visibility,distributor_visibility FROM plans ORDER BY id`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var id int64
		var c, d string
		if err = rows.Scan(&id, &c, &d); err != nil {
			rows.Close()
			return err
		}
		i, ok := index[id]
		if !ok {
			rows.Close()
			return fmt.Errorf("unexpected imported plan")
		}
		plans[i].CustomerVisibility = c
		plans[i].DistributorVisibility = d
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	rows, err = db.QueryContext(ctx, `SELECT plan_id,user_id,audience FROM plan_visibility_users ORDER BY plan_id,audience,user_id`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var pid, uid int64
		var a string
		if err = rows.Scan(&pid, &uid, &a); err != nil {
			return err
		}
		i, ok := index[pid]
		if !ok {
			return fmt.Errorf("unexpected plan recipient")
		}
		if a == "customer" {
			plans[i].CustomerUserIDs = append(plans[i].CustomerUserIDs, uid)
		} else {
			plans[i].DistributorUserIDs = append(plans[i].DistributorUserIDs, uid)
		}
	}
	return rows.Err()
}
