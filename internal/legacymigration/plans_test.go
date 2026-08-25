package legacymigration

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Hao-Monster/Xboard-Go/internal/store"
	_ "modernc.org/sqlite"
)

func TestReadPlansSnapshotPreservesLegacySemantics(t *testing.T) {
	path := createLegacyPlansSnapshot(t)
	database, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`
		INSERT INTO v2_settings (name, value) VALUES ('reset_traffic_method', '4');
		INSERT INTO v2_plan (
			id, group_id, transfer_enable, name, speed_limit, show, sort, renew, content,
			reset_traffic_method, capacity_limit, created_at, updated_at, prices, sell, device_limit, tags
		) VALUES (
			7, 3, 1024, 'Premium', 500, 1, 4, 0, 'Legacy content',
			2, 25, 1700000000, 1700000010,
			'{"monthly":1.23,"quarterly":3.45,"reset_traffic":0}', 1, 5, '["featured","premium"]'
		), (
			9, NULL, 64, 'Basic', NULL, 0, NULL, 1, NULL,
			NULL, NULL, '2023-11-14 22:13:20', '2023-11-14 22:13:30', '{}', 0, NULL, NULL
		);
	`); err != nil {
		_ = database.Close()
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}

	snapshot, err := ReadPlansSnapshot(context.Background(), path)
	if err != nil {
		t.Fatalf("ReadPlansSnapshot() error = %v", err)
	}
	if snapshot.Path != path || snapshot.Size < 512 || len(snapshot.SHA256) != 64 || len(snapshot.Checksum) != 64 {
		t.Fatalf("snapshot identity = %#v", snapshot)
	}
	if snapshot.TrafficResetMethod != 4 || snapshot.SettingsChecksum != store.LegacyPlanSettingsChecksum(4) {
		t.Fatalf("traffic reset setting = %d/%q", snapshot.TrafficResetMethod, snapshot.SettingsChecksum)
	}
	if len(snapshot.Plans) != 2 {
		t.Fatalf("plans = %#v", snapshot.Plans)
	}
	first := snapshot.Plans[0]
	if first.ID != 7 || first.GroupID == nil || *first.GroupID != 3 || first.TransferEnableGiB != 1024 ||
		!first.Show || first.Renew || !first.Sell || first.Prices["monthly"] != 123 ||
		first.Prices["quarterly"] != 345 || first.Prices["reset_traffic"] != 0 || len(first.Tags) != 2 {
		t.Fatalf("first plan = %#v", first)
	}
	second := snapshot.Plans[1]
	if second.ID != 9 || second.GroupID != nil || second.SpeedLimit != nil || second.Content != "" ||
		second.CreatedAt != 1700000000 || second.UpdatedAt != 1700000010 || second.Tags == nil || len(second.Tags) != 0 {
		t.Fatalf("second plan = %#v", second)
	}
}

func TestReadPlansSnapshotRecordsRealEmptyDomain(t *testing.T) {
	snapshot, err := ReadPlansSnapshot(context.Background(), createLegacyPlansSnapshot(t))
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Plans == nil || len(snapshot.Plans) != 0 || len(snapshot.Checksum) != 64 ||
		snapshot.TrafficResetMethod != 1 || snapshot.SettingsChecksum != store.LegacyPlanSettingsChecksum(1) {
		t.Fatalf("empty snapshot = %#v", snapshot)
	}
}

func TestReadPlansSnapshotTreatsNullTrafficResetSettingAsRuntimeDefault(t *testing.T) {
	path := createLegacyPlansSnapshot(t)
	database, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`INSERT INTO v2_settings (name, value) VALUES ('reset_traffic_method', NULL)`); err != nil {
		_ = database.Close()
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}

	snapshot, err := ReadPlansSnapshot(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.TrafficResetMethod != 1 || snapshot.SettingsChecksum != store.LegacyPlanSettingsChecksum(1) {
		t.Fatalf("null traffic reset setting = %d/%q", snapshot.TrafficResetMethod, snapshot.SettingsChecksum)
	}
}

func TestReadPlansSnapshotRejectsInvalidTrafficResetSetting(t *testing.T) {
	for _, scenario := range []struct {
		name   string
		values string
	}{
		{name: "out of range", values: "('reset_traffic_method', '5')"},
		{name: "not an integer", values: "('reset_traffic_method', 'monthly')"},
		{name: "duplicate", values: "('reset_traffic_method', '1'), ('reset_traffic_method', '2')"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			path := createLegacyPlansSnapshot(t)
			database, err := sql.Open("sqlite", "file:"+path)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := database.Exec("INSERT INTO v2_settings (name, value) VALUES " + scenario.values); err != nil {
				_ = database.Close()
				t.Fatal(err)
			}
			if err := database.Close(); err != nil {
				t.Fatal(err)
			}
			if _, err := ReadPlansSnapshot(context.Background(), path); err == nil || !strings.Contains(err.Error(), "traffic reset method") {
				t.Fatalf("ReadPlansSnapshot() error = %v", err)
			}
		})
	}
}

func TestReadPlansSnapshotRejectsUnsafeOrLossyRows(t *testing.T) {
	for _, scenario := range []struct {
		name     string
		values   string
		contains string
	}{
		{name: "unknown price period", values: `1,NULL,1,'P',NULL,1,0,1,'',NULL,NULL,1,1,'{"weekly":1}',1,NULL,'[]'`, contains: "unknown price period"},
		{name: "fractional cent", values: `1,NULL,1,'P',NULL,1,0,1,'',NULL,NULL,1,1,'{"monthly":1.001}',1,NULL,'[]'`, contains: "two decimal"},
		{name: "invalid boolean", values: `1,NULL,1,'P',NULL,2,0,1,'',NULL,NULL,1,1,'{}',1,NULL,'[]'`, contains: "boolean"},
		{name: "invalid price JSON", values: `1,NULL,1,'P',NULL,1,0,1,'',NULL,NULL,1,1,'[]',1,NULL,'[]'`, contains: "prices"},
		{name: "invalid tags JSON", values: `1,NULL,1,'P',NULL,1,0,1,'',NULL,NULL,1,1,'{}',1,NULL,'{}'`, contains: "tags"},
		{name: "invalid row", values: `1,NULL,0,'P',NULL,1,0,1,'',NULL,NULL,1,1,'{}',1,NULL,'[]'`, contains: "validate"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			path := createLegacyPlansSnapshot(t)
			database, err := sql.Open("sqlite", "file:"+path)
			if err != nil {
				t.Fatal(err)
			}
			statement := `INSERT INTO v2_plan (
				id,group_id,transfer_enable,name,speed_limit,show,sort,renew,content,
				reset_traffic_method,capacity_limit,created_at,updated_at,prices,sell,device_limit,tags
			) VALUES (` + scenario.values + `)`
			if _, err := database.Exec(statement); err != nil {
				_ = database.Close()
				t.Fatal(err)
			}
			if err := database.Close(); err != nil {
				t.Fatal(err)
			}
			if _, err := ReadPlansSnapshot(context.Background(), path); err == nil || !strings.Contains(strings.ToLower(err.Error()), strings.ToLower(scenario.contains)) {
				t.Fatalf("ReadPlansSnapshot() error = %v, want %q", err, scenario.contains)
			}
		})
	}

	t.Run("missing real table", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "legacy-missing-plan.db")
		database, err := sql.Open("sqlite", "file:"+path)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := database.Exec(`CREATE TABLE unrelated (id INTEGER);`); err != nil {
			t.Fatal(err)
		}
		if err := database.Close(); err != nil {
			t.Fatal(err)
		}
		if _, err := ReadPlansSnapshot(context.Background(), path); err == nil || !strings.Contains(err.Error(), "required real table") {
			t.Fatalf("ReadPlansSnapshot() error = %v", err)
		}
	})
}

func createLegacyPlansSnapshot(t testing.TB) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "legacy-plans.db")
	database, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`
		CREATE TABLE v2_settings (
			name TEXT,
			value TEXT
		);
		CREATE TABLE v2_plan (
			id INTEGER PRIMARY KEY,
			group_id INTEGER,
			transfer_enable INTEGER NOT NULL,
			name TEXT NOT NULL,
			speed_limit INTEGER,
			show INTEGER NOT NULL,
			sort INTEGER,
			renew INTEGER NOT NULL,
			content TEXT,
			reset_traffic_method INTEGER,
			capacity_limit INTEGER,
			created_at,
			updated_at,
			prices TEXT,
			sell INTEGER NOT NULL,
			device_limit INTEGER,
			tags TEXT
		);
	`); err != nil {
		_ = database.Close()
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}
