package legacymigration

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Hao-Monster/Xboard-Go/internal/security"
	_ "modernc.org/sqlite"
)

func TestReadInvitationCodesSnapshotPreparesEncryptedCompatibleRows(t *testing.T) {
	path := createLegacyInvitationCodesSnapshot(t)
	snapshot, err := ReadInvitationCodesSnapshot(context.Background(), path)
	if err != nil {
		t.Fatalf("ReadInvitationCodesSnapshot() error = %v", err)
	}
	defer snapshot.ClearSecrets()
	if snapshot.Path != path || snapshot.Size < 256 || len(snapshot.SHA256) != 64 || len(snapshot.Codes) != 2 {
		t.Fatalf("invitation code snapshot = %#v", snapshot)
	}
	protector, err := security.NewInvitationProtector([]byte(strings.Repeat("k", 32)))
	if err != nil {
		t.Fatal(err)
	}
	prepared, checksum, err := snapshot.Prepare(protector)
	if err != nil {
		t.Fatalf("Prepare() error = %v", err)
	}
	if len(prepared) != 2 || len(checksum) != 64 {
		t.Fatalf("prepared invitation codes = %#v checksum=%q", prepared, checksum)
	}
	if prepared[0].ID != 7 || prepared[0].UserID != 11 || prepared[0].PV != 3 || prepared[0].ConsumedAt != nil ||
		prepared[0].CreatedAt != 1_700_000_000 || prepared[0].UpdatedAt != 1_700_000_100 {
		t.Fatalf("first prepared invitation code = %#v", prepared[0])
	}
	if prepared[1].ConsumedAt == nil || *prepared[1].ConsumedAt != 1_700_000_300 {
		t.Fatalf("used prepared invitation code = %#v", prepared[1])
	}
	for index, expected := range []string{"AbCd1234", "ZyXw9876"} {
		plaintext, err := protector.DecryptCode(prepared[index].UserID, prepared[index].CodeCipher)
		if err != nil || string(plaintext) != expected {
			t.Fatalf("DecryptCode(%d) = %q, %v", index, plaintext, err)
		}
		for offset := range plaintext {
			plaintext[offset] = 0
		}
		if strings.Contains(string(prepared[index].CodeCipher), expected) {
			t.Fatalf("prepared ciphertext exposed code %q", expected)
		}
	}
}

func TestReadInvitationCodesSnapshotRejectsDuplicatesWithoutLeakingCode(t *testing.T) {
	path := createLegacyInvitationCodesSnapshot(t)
	database, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`UPDATE v2_invite_code SET code='AbCd1234' WHERE id=9`); err != nil {
		_ = database.Close()
		t.Fatal(err)
	}
	_ = database.Close()
	_, err = ReadInvitationCodesSnapshot(context.Background(), path)
	if err == nil || !strings.Contains(err.Error(), "duplicate") || strings.Contains(err.Error(), "AbCd1234") {
		t.Fatalf("ReadInvitationCodesSnapshot(duplicate) error = %v", err)
	}
}

func TestReadInvitationCodesSnapshotRejectsInvalidStateWithoutLeakingCode(t *testing.T) {
	path := createLegacyInvitationCodesSnapshot(t)
	database, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`UPDATE v2_invite_code SET status=2,pv=-1 WHERE id=7`); err != nil {
		_ = database.Close()
		t.Fatal(err)
	}
	_ = database.Close()
	_, err = ReadInvitationCodesSnapshot(context.Background(), path)
	if err == nil || !strings.Contains(err.Error(), "id 7 is invalid") || strings.Contains(err.Error(), "AbCd1234") {
		t.Fatalf("ReadInvitationCodesSnapshot(invalid state) error = %v", err)
	}
}

func createLegacyInvitationCodesSnapshot(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "legacy-invitation-codes.db")
	database, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`
		CREATE TABLE v2_invite_code (
			id INTEGER PRIMARY KEY AUTOINCREMENT,user_id INTEGER NOT NULL,code VARCHAR NOT NULL,
			status INTEGER NOT NULL DEFAULT 0,pv INTEGER NOT NULL DEFAULT 0,
			created_at INTEGER NOT NULL,updated_at INTEGER NOT NULL
		);
		INSERT INTO v2_invite_code VALUES
			(9,12,'ZyXw9876',1,5,1700000200,1700000300),
			(7,11,'AbCd1234',0,3,1700000000,1700000100);
	`); err != nil {
		_ = database.Close()
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}
