package store

import (
	"context"
	"database/sql"
	"net/netip"
)

// Kept separately from live online state: importing a snapshot must never
// resurrect an old connection. Anonymization removes this historical IP too.
type LegacyUserHistory struct {
	LegacyTime  int64  `json:"t,omitempty"`
	LastLoginIP string `json:"last_login_ip,omitempty"`
	OnlineCount int64  `json:"online_count,omitempty"`
}

func (h LegacyUserHistory) Valid() bool {
	if h.LegacyTime < 0 || h.OnlineCount < 0 {
		return false
	}
	if h.LastLoginIP != "" {
		ip, err := netip.ParseAddr(h.LastLoginIP)
		if err != nil || ip.Zone() != "" {
			return false
		}
	}
	return true
}
func (h LegacyUserHistory) Empty() bool {
	return h.LegacyTime == 0 && h.LastLoginIP == "" && h.OnlineCount == 0
}
func importLegacyUserHistory(ctx context.Context, tx *sql.Tx, user LegacyHumanUser) error {
	if user.History.Empty() {
		return nil
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO legacy_user_history(user_id,legacy_time,last_login_ip,online_count) VALUES(?,?,?,?)`, user.ID, user.History.LegacyTime, user.History.LastLoginIP, user.History.OnlineCount)
	return err
}
func readLegacyUserHistory(ctx context.Context, db queryer, users []LegacyHumanUser) error {
	index := map[int64]int{}
	for i := range users {
		index[users[i].ID] = i
	}
	rows, err := db.QueryContext(ctx, `SELECT user_id,legacy_time,last_login_ip,online_count FROM legacy_user_history`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var h LegacyUserHistory
		if err = rows.Scan(&id, &h.LegacyTime, &h.LastLoginIP, &h.OnlineCount); err != nil {
			return err
		}
		if i, ok := index[id]; ok {
			users[i].History = h
		}
	}
	return rows.Err()
}
