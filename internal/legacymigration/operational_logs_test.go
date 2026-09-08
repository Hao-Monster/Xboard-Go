package legacymigration

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

func TestReadOperationalLogsSnapshotRetainsOnlyWindowAndSafeMetadata(t *testing.T) {
	path, asOf := operationalSourceFixture(t)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := ReadOperationalLogsSnapshot(t.Context(), path, asOf)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.AsOf != asOf.Unix() || snapshot.CutoffAt != asOf.Add(-90*24*time.Hour).Unix() || len(snapshot.Logs) != 5 || snapshot.ExcludedRows != 1 || snapshot.ExcludedFailedJobs != 1 {
		t.Fatalf("snapshot = %#v", snapshot)
	}
	if snapshot.Logs[0].Category != "admin_audit" || snapshot.Logs[0].Method != "POST" || snapshot.Logs[1].Category != "request" || snapshot.Logs[1].Method != "GET" || snapshot.Logs[2].Method != "" {
		t.Fatalf("metadata projection = %#v", snapshot.Logs)
	}
	if snapshot.Checksum != store.LegacyOperationalLogsChecksum(snapshot.Logs) {
		t.Fatal("checksum mismatch")
	}
	localSnapshot, err := ReadOperationalLogsSnapshot(t.Context(), path, asOf.In(time.FixedZone("Asia/Shanghai", 8*60*60)))
	if err != nil || localSnapshot.CutoffAt != snapshot.CutoffAt || localSnapshot.AsOf-localSnapshot.CutoffAt != 90*86400 || localSnapshot.Checksum != snapshot.Checksum {
		t.Fatalf("retention must remain an elapsed 90-day window regardless of reporting timezone: %#v %v", localSnapshot, err)
	}
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "SENSITIVE_FIXTURE") {
		t.Fatal("metadata output contains a raw legacy field")
	}
	after, err := os.ReadFile(path)
	if err != nil || string(before) != string(after) {
		t.Fatalf("read-only source changed: %v", err)
	}
}

func TestReadOperationalLogsSnapshotRejectsQueuedFutureAndAmbiguousRows(t *testing.T) {
	for _, mutation := range []string{
		`INSERT INTO jobs VALUES ('SENSITIVE_FIXTURE_PHP')`,
		`UPDATE v2_log SET created_at=9999999999 WHERE id=2`,
		`UPDATE v2_log SET created_at=NULL WHERE id=2`,
		`DROP TABLE v2_mail_log; CREATE VIEW v2_mail_log AS SELECT 1 AS id,1 AS created_at`,
	} {
		t.Run(mutation, func(t *testing.T) {
			path, asOf := operationalSourceFixture(t)
			db, err := sql.Open("sqlite", "file:"+path)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = db.Exec(mutation); err != nil {
				db.Close()
				t.Fatal(err)
			}
			if err = db.Close(); err != nil {
				t.Fatal(err)
			}
			if _, err := ReadOperationalLogsSnapshot(t.Context(), path, asOf); err == nil || strings.Contains(err.Error(), "SENSITIVE_FIXTURE") {
				t.Fatalf("unsafe source result = %v", err)
			}
		})
	}
}

func operationalSourceFixture(t *testing.T) (string, time.Time) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "legacy-operations.db")
	asOf := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)
	cutoff := asOf.Add(-90 * 24 * time.Hour).Unix()
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	_, err = db.Exec(`
	 CREATE TABLE jobs(payload TEXT);
	 CREATE TABLE v2_user(id INTEGER PRIMARY KEY,created_at INTEGER,invite_user_id INTEGER,email TEXT);
	 CREATE TABLE failed_jobs(payload TEXT, exception TEXT);
	 INSERT INTO failed_jobs VALUES ('SENSITIVE_FIXTURE_PHP','SENSITIVE_FIXTURE_EXCEPTION');
	 CREATE VIEW v2_stat AS SELECT json_extract('invalid aggregate','$') AS order_total;
	 CREATE VIEW stats_daily AS SELECT json_extract('invalid aggregate','$') AS paid_total;
	 CREATE TABLE v2_admin_audit_log(id INTEGER,created_at INTEGER,method TEXT,admin_id INTEGER,path TEXT);
	 CREATE TABLE v2_log(id INTEGER,created_at INTEGER,method TEXT,data TEXT,ip TEXT);
	 CREATE TABLE v2_mail_log(id INTEGER,created_at INTEGER,email TEXT,subject TEXT);
	 CREATE TABLE v2_server_log(id INTEGER,created_at INTEGER,message TEXT);
	 INSERT INTO v2_admin_audit_log VALUES (1,?,'post',17,'SENSITIVE_FIXTURE_PATH');
	 INSERT INTO v2_log VALUES (1,?,'GET','SENSITIVE_FIXTURE_EXCLUDED','SENSITIVE_FIXTURE_IP'),(2,?,'get','SENSITIVE_FIXTURE_DATA','SENSITIVE_FIXTURE_IP'),(3,?,'SENSITIVE_FIXTURE_METHOD','SENSITIVE_FIXTURE_DATA','SENSITIVE_FIXTURE_IP');
	 INSERT INTO v2_mail_log VALUES (1,?,'SENSITIVE_FIXTURE_EMAIL','SENSITIVE_FIXTURE_SUBJECT');
	 INSERT INTO v2_server_log VALUES (1,?,'SENSITIVE_FIXTURE_MESSAGE');
	`, cutoff, cutoff-1, cutoff, asOf.Unix(), asOf.Unix(), cutoff)
	if err != nil {
		t.Fatal(err)
	}
	return path, asOf
}

func TestReadOperationalLogsSnapshotCountsAllUserFactsWithoutIdentities(t *testing.T) {
	path, asOf := operationalSourceFixture(t)
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE v2_distributor_order(subscriber_user_id INTEGER);
	 INSERT INTO v2_distributor_order VALUES (2);
	 INSERT INTO v2_user VALUES (1,?,NULL,'SENSITIVE_FIXTURE_HUMAN'),(2,?,1,'SENSITIVE_FIXTURE_INTERNAL'),(3,?,NULL,'SENSITIVE_FIXTURE_FUTURE')`, asOf.Unix()-1, asOf.Unix(), asOf.Unix()+1); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	snapshot, err := ReadOperationalLogsSnapshot(t.Context(), path, asOf)
	if err != nil {
		t.Fatal(err)
	}
	want := store.LegacyOperationalUserFactsChecksum([]store.LegacyOperationalUserFact{{HourAt: asOf.Unix() - 3600, Registered: 1}, {HourAt: asOf.Unix(), Registered: 1, Invited: 1}})
	if snapshot.UserFactsChecksum != want {
		t.Fatalf("complete source user domain checksum=%s want=%s", snapshot.UserFactsChecksum, want)
	}
	encoded, err := json.Marshal(snapshot)
	if err != nil || strings.Contains(string(encoded), "SENSITIVE_FIXTURE") {
		t.Fatalf("user fact projection leaked identity: %v", err)
	}
}
