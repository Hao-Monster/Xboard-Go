package legacymigration

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"
	_ "modernc.org/sqlite"
)

func TestReadHumanUsersSnapshotPreservesSupportedLegacyIdentityAndAccessState(t *testing.T) {
	path := createLegacyHumanUsersSnapshot(t)
	snapshot, err := ReadHumanUsersSnapshot(context.Background(), path)
	if err != nil {
		t.Fatalf("ReadHumanUsersSnapshot() error = %v", err)
	}
	if snapshot.Path != path || snapshot.Size < 512 || len(snapshot.SHA256) != 64 || len(snapshot.Checksum) != 64 || len(snapshot.Users) != 2 {
		t.Fatalf("snapshot identity/count = path:%q size:%d users:%d", snapshot.Path, snapshot.Size, len(snapshot.Users))
	}
	admin, invited := snapshot.Users[0], snapshot.Users[1]
	if admin.ID != 1 || admin.Email != "admin@example.test" || !admin.IsAdmin || admin.Banned || admin.GroupID == nil || *admin.GroupID != 7 ||
		admin.LastLoginAt == nil || *admin.LastLoginAt != 1_700_000_100 || admin.SpeedLimit != 0 || admin.DeviceLimit != 0 {
		t.Fatalf("admin = %#v", admin)
	}
	if invited.ID != 2 || invited.InviteUserID == nil || *invited.InviteUserID != 1 || invited.TransferEnable != 1_000 ||
		invited.Balance != 1_234 || invited.Discount == nil || *invited.Discount != 15 || invited.CommissionType != 2 ||
		invited.CommissionRate == nil || *invited.CommissionRate != 20 || invited.CommissionBalance != 567 ||
		invited.TrafficUpload != 10 || invited.TrafficDownload != 20 || invited.ExpiredAt == nil || *invited.ExpiredAt != 1_800_000_000 ||
		invited.LastOnlineAt == nil || *invited.LastOnlineAt != 1_700_000_200 || invited.SpeedLimit != 50 || invited.DeviceLimit != 3 ||
		invited.PlanID == nil || *invited.PlanID != 9 || invited.NextResetAt == nil || *invited.NextResetAt != 1_700_000_300 ||
		invited.LastResetAt == nil || *invited.LastResetAt != 1_700_000_250 || invited.ResetCount != 2 {
		t.Fatalf("invited user = %#v", invited)
	}
	if !strings.HasPrefix(admin.PasswordHash, "$2y$10$") || strings.Contains(snapshot.Checksum, admin.PasswordHash) {
		t.Fatal("snapshot did not preserve bcrypt safely")
	}
}

func TestReadHumanUsersSnapshotRejectsLossyOrUnsafeLegacyState(t *testing.T) {
	weakHash, err := bcrypt.GenerateFromPassword([]byte("legacy-password-123"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	for _, scenario := range []struct {
		name      string
		statement string
		contains  string
	}{
		{name: "staff", statement: `UPDATE v2_user SET is_staff = 1 WHERE id = 2`, contains: "unsupported"},
		{name: "distributor", statement: `UPDATE v2_user SET is_distributor = 1 WHERE id = 2`, contains: "unsupported"},
		{name: "negative finance balance", statement: `UPDATE v2_user SET balance = -1 WHERE id = 2`, contains: "finance balances"},
		{name: "fractional discount", statement: `UPDATE v2_user SET discount = 12.5 WHERE id = 2`, contains: "invalid discount"},
		{name: "invalid commission type", statement: `UPDATE v2_user SET commission_type = 3 WHERE id = 2`, contains: "commission type"},
		{name: "invalid plan", statement: `UPDATE v2_user SET plan_id = -9 WHERE id = 2`, contains: "invalid"},
		{name: "invalid reset state", statement: `UPDATE v2_user SET reset_count = -1 WHERE id = 2`, contains: "reset count"},
		{name: "last login ip", statement: `UPDATE v2_user SET last_login_ip = '203.0.113.4' WHERE id = 2`, contains: "unsupported"},
		{name: "email normalization", statement: `UPDATE v2_user SET email = ' Upper@Example.Test ' WHERE id = 2`, contains: "invalid"},
		{name: "missing inviter", statement: `UPDATE v2_user SET invite_user_id = 999 WHERE id = 2`, contains: "missing inviter"},
		{name: "weak bcrypt", statement: `UPDATE v2_user SET password = '` + string(weakHash) + `' WHERE id = 2`, contains: "invalid"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			path := createLegacyHumanUsersSnapshot(t)
			database, err := sql.Open("sqlite", "file:"+path)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := database.Exec(scenario.statement); err != nil {
				_ = database.Close()
				t.Fatal(err)
			}
			if err := database.Close(); err != nil {
				t.Fatal(err)
			}
			if _, err := ReadHumanUsersSnapshot(context.Background(), path); err == nil || !strings.Contains(strings.ToLower(err.Error()), strings.ToLower(scenario.contains)) {
				t.Fatalf("ReadHumanUsersSnapshot() error = %v, want %q", err, scenario.contains)
			}
		})
	}
}

func createLegacyHumanUsersSnapshot(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "legacy-human-users.db")
	database, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`
		CREATE TABLE v2_user (
			id INTEGER PRIMARY KEY, invite_user_id INTEGER, telegram_id INTEGER, email TEXT NOT NULL,
			password TEXT NOT NULL, password_algo TEXT, password_salt TEXT, balance INTEGER NOT NULL DEFAULT 0,
			discount REAL, commission_type INTEGER NOT NULL DEFAULT 0, commission_rate REAL,
			commission_balance INTEGER NOT NULL DEFAULT 0, t INTEGER NOT NULL DEFAULT 0, u INTEGER NOT NULL DEFAULT 0,
			d INTEGER NOT NULL DEFAULT 0, transfer_enable INTEGER NOT NULL DEFAULT 0, banned INTEGER NOT NULL DEFAULT 0,
			is_admin INTEGER NOT NULL DEFAULT 0, last_login_at INTEGER, is_staff INTEGER NOT NULL DEFAULT 0,
			last_login_ip TEXT, uuid TEXT NOT NULL, group_id INTEGER, plan_id INTEGER, speed_limit INTEGER,
			remind_expire INTEGER NOT NULL DEFAULT 1, remind_traffic INTEGER NOT NULL DEFAULT 1, token TEXT NOT NULL,
			expired_at INTEGER, remarks TEXT, created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL,
			device_limit INTEGER, online_count INTEGER, last_online_at INTEGER, next_reset_at INTEGER,
			last_reset_at INTEGER, reset_count INTEGER NOT NULL DEFAULT 0, is_distributor INTEGER NOT NULL DEFAULT 0,
			distributor_name TEXT
		);
	`); err != nil {
		_ = database.Close()
		t.Fatal(err)
	}
	legacyHash, err := bcrypt.GenerateFromPassword([]byte("legacy-password-123"), 10)
	if err != nil {
		_ = database.Close()
		t.Fatal(err)
	}
	phpHash := "$2y$" + string(legacyHash[4:])
	if _, err := database.Exec(`
		INSERT INTO v2_user
		(id, email, password, is_admin, last_login_at, uuid, group_id, token, expired_at, created_at, updated_at)
		VALUES (1, 'admin@example.test', ?, 1, 1700000100, '11111111-1111-4111-8111-111111111111', 7,
		        '11111111111111111111111111111111', 0, 1700000000, 1700000200);
		INSERT INTO v2_user
		(id, invite_user_id, email, password, balance, discount, commission_type, commission_rate, commission_balance,
		 u, d, transfer_enable, uuid, group_id, plan_id, speed_limit, token,
		 expired_at, created_at, updated_at, device_limit, last_online_at, next_reset_at, last_reset_at, reset_count)
		VALUES (2, 1, 'user@example.test', ?, 1234, 15, 2, 20, 567, 10, 20, 1000, '22222222-2222-4222-8222-222222222222', 7, 9, 50,
		        '22222222222222222222222222222222', 1800000000, 1700000001, 1700000201, 3, 1700000200,
		        1700000300, 1700000250, 2);
	`, phpHash, phpHash); err != nil {
		_ = database.Close()
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}
