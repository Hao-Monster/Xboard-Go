package store

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func BenchmarkAuthenticateAccessTokenTenThousand(b *testing.B) {
	database, err := OpenSQLite("file:benchmark-access-token-auth?mode=memory&cache=shared")
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = database.Close() })
	ctx := context.Background()
	if err := database.Migrate(ctx); err != nil {
		b.Fatal(err)
	}
	now := time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)
	if _, err := database.BootstrapAdmin(ctx, "benchmark-access-token@example.test", "hash", now); err != nil {
		b.Fatal(err)
	}
	user, err := database.FindUserByEmail(ctx, "benchmark-access-token@example.test")
	if err != nil {
		b.Fatal(err)
	}

	tx, err := database.db.BeginTx(ctx, nil)
	if err != nil {
		b.Fatal(err)
	}
	statement, err := tx.PrepareContext(ctx, `
		INSERT INTO access_tokens (user_id, token_hash, name, last_used_at, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		b.Fatal(err)
	}
	const tokenCount = 10_000
	for index := 0; index < tokenCount; index++ {
		if _, err := statement.ExecContext(
			ctx,
			user.ID,
			fmt.Sprintf("%064x", index+1),
			fmt.Sprintf("benchmark-device-%05d", index),
			now.Unix(),
			now.Unix(),
			now.Unix(),
		); err != nil {
			b.Fatal(err)
		}
	}
	if err := statement.Close(); err != nil {
		b.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		b.Fatal(err)
	}
	targetHash := fmt.Sprintf("%064x", tokenCount)

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if _, err := database.AuthenticateAccessToken(ctx, targetHash, now); err != nil {
			b.Fatal(err)
		}
	}
}
