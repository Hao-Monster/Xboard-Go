package legacymigration

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

func readPlanAudienceSnapshot(ctx context.Context, db *sql.DB, plans []store.LegacyPlan) error {
	var columns int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM pragma_table_info('v2_plan') WHERE name IN ('customer_visibility','distributor_visibility')`).Scan(&columns); err != nil {
		return err
	}
	if columns == 0 {
		return nil
	}
	if columns != 2 {
		return fmt.Errorf("legacy plan visibility columns are incomplete")
	}
	if err := requireRealTable(ctx, db, "v2_plan_visibility_user", []string{"plan_id", "user_id", "audience"}); err != nil {
		return err
	}
	if err := requireRealTable(ctx, db, "v2_user", []string{"id", "is_distributor"}); err != nil {
		return err
	}
	index := map[int64]int{}
	for i := range plans {
		index[plans[i].ID] = i
	}
	rows, err := db.QueryContext(ctx, `SELECT id,customer_visibility,distributor_visibility FROM v2_plan ORDER BY id`)
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
			return fmt.Errorf("unexpected plan visibility id")
		}
		plans[i].CustomerVisibility = c
		plans[i].DistributorVisibility = d
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	rows, err = db.QueryContext(ctx, `SELECT v.plan_id,v.user_id,v.audience,u.is_distributor FROM v2_plan_visibility_user v LEFT JOIN v2_user u ON u.id=v.user_id ORDER BY v.plan_id,v.audience,v.user_id`)
	if err != nil {
		return err
	}
	defer rows.Close()
	members := 0
	for rows.Next() {
		members++
		if members > store.MaxLegacyPlanAudienceMembers {
			return fmt.Errorf("legacy plan recipient total exceeds migration limit")
		}
		var pid, uid int64
		var audience string
		var distributor sql.NullInt64
		if err = rows.Scan(&pid, &uid, &audience, &distributor); err != nil {
			return err
		}
		i, ok := index[pid]
		if !ok || !distributor.Valid || (audience != "customer" && audience != "distributor") || (audience == "customer" && distributor.Int64 != 0) || (audience == "distributor" && distributor.Int64 != 1) {
			return fmt.Errorf("invalid legacy plan audience membership")
		}
		if audience == "customer" {
			plans[i].CustomerUserIDs = append(plans[i].CustomerUserIDs, uid)
		} else {
			plans[i].DistributorUserIDs = append(plans[i].DistributorUserIDs, uid)
		}
		if len(plans[i].CustomerUserIDs) > 5000 || len(plans[i].DistributorUserIDs) > 5000 {
			return fmt.Errorf("legacy plan recipient limit exceeded")
		}
	}
	return rows.Err()
}
