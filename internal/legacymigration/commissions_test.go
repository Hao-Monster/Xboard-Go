package legacymigration

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

func TestReadCommissionsSnapshotPreservesLegacyLogs(t *testing.T) {
	path := createLegacyCommissionsSnapshot(t)
	snapshot, err := ReadCommissionsSnapshot(context.Background(), path)
	if err != nil {
		t.Fatalf("ReadCommissionsSnapshot() error = %v", err)
	}
	if snapshot.Path != path || snapshot.Size < 256 || len(snapshot.SHA256) != 64 || len(snapshot.Checksum) != 64 || len(snapshot.Logs) != 2 {
		t.Fatalf("commission snapshot = %#v", snapshot)
	}
	first := snapshot.Logs[0]
	if first.ID != 7 || first.InviteUserID != 11 || first.UserID != 21 || first.TradeNo != "2026082600000000000000001" ||
		first.OrderAmount != 10_000 || first.GetAmount != 1_000 || first.CreatedAt != 1_700_000_000 || first.UpdatedAt != 1_700_000_100 {
		t.Fatalf("first commission = %#v", first)
	}
}

func TestReadCommissionsSnapshotRejectsDuplicatePayout(t *testing.T) {
	path := createLegacyCommissionsSnapshot(t)
	database, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`UPDATE v2_commission_log SET invite_user_id = 11, trade_no = '2026082600000000000000001' WHERE id = 9`); err != nil {
		_ = database.Close()
		t.Fatal(err)
	}
	_ = database.Close()
	if _, err := ReadCommissionsSnapshot(context.Background(), path); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("ReadCommissionsSnapshot(duplicate) error = %v", err)
	}
}

func createLegacyCommissionsSnapshot(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "legacy-commissions.db")
	database, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`
		CREATE TABLE v2_commission_log (
			id INTEGER PRIMARY KEY, invite_user_id INTEGER NOT NULL, user_id INTEGER NOT NULL,
			trade_no TEXT NOT NULL, order_amount INTEGER NOT NULL, get_amount INTEGER NOT NULL,
			created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL
		);
		INSERT INTO v2_commission_log VALUES
			(9, 12, 21, '2026082600000000000000001', 10000, 300, 1700000100, 1700000200),
			(7, 11, 21, '2026082600000000000000001', 10000, 1000, 1700000000, 1700000100);
	`); err != nil {
		_ = database.Close()
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}
