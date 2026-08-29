package store

import (
	"fmt"
	"testing"
)

func BenchmarkGetClientAppSettings(b *testing.B) {
	database := openBenchmarkStore(b, "client-app-settings.db")
	ctx := b.Context()
	b.ReportAllocs()
	for b.Loop() {
		if _, err := database.GetClientAppSettings(ctx); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkLegacyClientAppVersionLookup100KUsers(b *testing.B) {
	database := openBenchmarkStore(b, "client-app-version-100k.db")
	ctx := b.Context()
	if _, err := database.db.ExecContext(ctx, `
		WITH RECURSIVE sequence(value) AS (
			SELECT 1 UNION ALL SELECT value + 1 FROM sequence WHERE value < 100000
		)
		INSERT INTO users (email,password_hash,subscription_token,created_at,updated_at)
		SELECT printf('client-version-%06d@example.test',value),'hash',printf('%032x',value),0,0
		FROM sequence
	`); err != nil {
		b.Fatal(err)
	}
	token := fmt.Sprintf("%032x", 100_000)
	b.ReportMetric(100_000, "users")
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if exists, err := database.ClientAppVersionTokenExists(ctx, token); err != nil || !exists {
			b.Fatal(err)
		}
		if _, err := database.GetClientAppSettings(ctx); err != nil {
			b.Fatal(err)
		}
	}
}
