package store

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestLoginFailureLimitPersistsThresholdAndExpiry(t *testing.T) {
	path := filepath.Join(t.TempDir(), "login-limit.sqlite")
	database, err := OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := database.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)
	administrator := createTicketTestUser(t, database, "login-limit-admin@example.test", now)
	settings, err := database.GetSiteSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !settings.PasswordLimitEnabled || settings.PasswordLimitCount != 5 || settings.PasswordLimitMinutes != 60 {
		t.Fatalf("default password limit settings = %#v", settings)
	}
	input := siteSettingsInput(settings)
	input.PasswordLimitCount = 2
	input.PasswordLimitMinutes = 1
	settings, err = database.UpdateSiteSettings(ctx, administrator.ID, settings.Revision, input, now)
	if err != nil {
		t.Fatal(err)
	}
	digest := bytes.Repeat([]byte{0x31}, 32)
	status, err := database.GetLoginFailureStatus(ctx, digest, now)
	if err != nil || status.Limited || status.Failures != 0 || status.Maximum != 2 || status.Window != time.Minute {
		t.Fatalf("initial login failure status = %#v err=%v", status, err)
	}
	for failure := 1; failure <= 2; failure++ {
		status, err = database.RecordLoginFailure(ctx, digest, now)
		if err != nil || status.Failures != failure {
			t.Fatalf("RecordLoginFailure(%d) status=%#v err=%v", failure, status, err)
		}
	}
	status, err = database.GetLoginFailureStatus(ctx, digest, now)
	if err != nil || !status.Limited || status.ResetAt == nil || !status.ResetAt.Equal(now.Add(time.Minute)) {
		t.Fatalf("limited login failure status = %#v err=%v", status, err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}

	database, err = OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	status, err = database.GetLoginFailureStatus(ctx, digest, now.Add(30*time.Second))
	if err != nil || !status.Limited {
		t.Fatalf("restarted login failure status = %#v err=%v", status, err)
	}
	status, err = database.GetLoginFailureStatus(ctx, digest, now.Add(time.Minute))
	if err != nil || status.Limited || status.Failures != 0 {
		t.Fatalf("expired login failure status = %#v err=%v", status, err)
	}
}

func TestLoginFailureLimitIsAtomicDisabledAndPrunable(t *testing.T) {
	database := newTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)
	digest := bytes.Repeat([]byte{0x42}, 32)
	const workers = 8
	var group sync.WaitGroup
	for index := 0; index < workers; index++ {
		group.Add(1)
		go func() {
			defer group.Done()
			if _, err := database.RecordLoginFailure(ctx, digest, now); err != nil {
				t.Errorf("RecordLoginFailure() error = %v", err)
			}
		}()
	}
	group.Wait()
	status, err := database.GetLoginFailureStatus(ctx, digest, now)
	if err != nil || status.Failures != workers || !status.Limited {
		t.Fatalf("concurrent login failure status = %#v err=%v", status, err)
	}

	administrator := createTicketTestUser(t, database, "login-limit-disable@example.test", now)
	settings, err := database.GetSiteSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	input := siteSettingsInput(settings)
	input.PasswordLimitEnabled = false
	if _, err := database.UpdateSiteSettings(ctx, administrator.ID, settings.Revision, input, now); err != nil {
		t.Fatal(err)
	}
	status, err = database.GetLoginFailureStatus(ctx, digest, now)
	if err != nil || status.Enabled || status.Limited {
		t.Fatalf("disabled login failure status = %#v err=%v", status, err)
	}
	if status, err = database.RecordLoginFailure(ctx, bytes.Repeat([]byte{0x43}, 32), now); err != nil || status.Enabled || status.Failures != 0 {
		t.Fatalf("disabled RecordLoginFailure() status=%#v err=%v", status, err)
	}
	removed, err := database.PruneExpiredLoginFailureLimits(ctx, now.Add(61*time.Minute), 100)
	if err != nil || removed != 1 {
		t.Fatalf("PruneExpiredLoginFailureLimits() removed=%d err=%v", removed, err)
	}
}

func TestLoginFailureLimitRejectsInvalidInputs(t *testing.T) {
	database := newTestStore(t)
	now := time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)
	for _, digest := range [][]byte{nil, bytes.Repeat([]byte{1}, 31), bytes.Repeat([]byte{1}, 33)} {
		if _, err := database.GetLoginFailureStatus(context.Background(), digest, now); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("GetLoginFailureStatus(%d bytes) error=%v, want ErrInvalidInput", len(digest), err)
		}
	}
}

func BenchmarkLoginFailureStatus(b *testing.B) {
	database, err := OpenSQLite("file:benchmark-login-failure?mode=memory&cache=shared")
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = database.Close() })
	ctx := context.Background()
	if err := database.Migrate(ctx); err != nil {
		b.Fatal(err)
	}
	now := time.Unix(1_800_000_000, 0)
	digest := bytes.Repeat([]byte{0x55}, 32)
	if _, err := database.RecordLoginFailure(ctx, digest, now); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for index := 0; index < b.N; index++ {
		if _, err := database.GetLoginFailureStatus(ctx, digest, now); err != nil {
			b.Fatal(err)
		}
	}
}
