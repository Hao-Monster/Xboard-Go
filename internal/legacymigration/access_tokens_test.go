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

func TestReadAccessTokensSnapshotPreservesCompatibleBearerCredentials(t *testing.T) {
	path := createLegacyAccessTokensSnapshot(t)
	snapshot, err := ReadAccessTokensSnapshot(context.Background(), path)
	if err != nil {
		t.Fatalf("ReadAccessTokensSnapshot() error = %v", err)
	}
	if snapshot.Path != path || snapshot.Size < 256 || len(snapshot.SHA256) != 64 || len(snapshot.Checksum) != 64 || len(snapshot.Tokens) != 2 {
		t.Fatalf("access token snapshot = %#v", snapshot)
	}
	first := snapshot.Tokens[0]
	if first.ID != 7 || first.UserID != 11 || first.Name != "legacy-device-one" ||
		first.TokenHash != security.DigestToken("legacy-bearer-one") || first.CreatedAt != 1_700_000_000 ||
		first.UpdatedAt != 1_700_000_100 || first.LastUsedAt == nil || *first.LastUsedAt != 1_700_000_050 || first.ExpiresAt != nil {
		t.Fatalf("first access token = %#v", first)
	}
	second := snapshot.Tokens[1]
	if second.ExpiresAt == nil || *second.ExpiresAt != 1_800_000_000 || second.LastUsedAt != nil {
		t.Fatalf("second access token = %#v", second)
	}
}

func TestReadAccessTokensSnapshotRejectsUnsupportedAbilities(t *testing.T) {
	path := createLegacyAccessTokensSnapshot(t)
	database, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`UPDATE personal_access_tokens SET abilities = '["read"]' WHERE id = 7`); err != nil {
		_ = database.Close()
		t.Fatal(err)
	}
	_ = database.Close()
	if _, err := ReadAccessTokensSnapshot(context.Background(), path); err == nil || !strings.Contains(err.Error(), "unsupported abilities") {
		t.Fatalf("ReadAccessTokensSnapshot(abilities) error = %v", err)
	}
}

func createLegacyAccessTokensSnapshot(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "legacy-access-tokens.db")
	database, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`
		CREATE TABLE personal_access_tokens (
			id INTEGER PRIMARY KEY, tokenable_type TEXT NOT NULL, tokenable_id INTEGER NOT NULL,
			name TEXT NOT NULL, token TEXT NOT NULL, abilities TEXT, last_used_at DATETIME,
			expires_at DATETIME, created_at DATETIME, updated_at DATETIME
		);
		INSERT INTO personal_access_tokens VALUES
			(9, 'App\Models\User', 12, 'legacy-device-two', ?, '["*"]', NULL, 1800000000, 1700000100, 1700000200),
			(7, 'App\Models\User', 11, 'legacy-device-one', ?, '["*"]', 1700000050, NULL, 1700000000, 1700000100);
	`, security.DigestToken("legacy-bearer-two"), security.DigestToken("legacy-bearer-one")); err != nil {
		_ = database.Close()
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}
