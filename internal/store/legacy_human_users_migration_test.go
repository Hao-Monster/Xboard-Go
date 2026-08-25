package store

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
)

func TestImportLegacyHumanUsersComposesWithPriorSlicesAndIsIdempotent(t *testing.T) {
	database := newLegacyHumanUserTarget(t)
	ctx := context.Background()
	if _, err := database.ImportLegacyContent(ctx, validLegacyContentImport(t), time.Unix(100, 0)); err != nil {
		t.Fatal(err)
	}
	if _, err := database.ImportLegacyGroupsRoutes(ctx, validLegacyGroupsRoutesImport(), time.Unix(200, 0)); err != nil {
		t.Fatal(err)
	}
	if _, err := database.ImportLegacyKnowledge(ctx, validLegacyKnowledgeImport(), time.Unix(300, 0)); err != nil {
		t.Fatal(err)
	}
	input := validLegacyHumanUsersImport(t)
	report, err := database.ImportLegacyHumanUsers(ctx, input, time.Unix(400, 0))
	if err != nil {
		t.Fatalf("ImportLegacyHumanUsers() error = %v", err)
	}
	if report.AlreadyApplied || report.Slice != LegacyHumanUsersSlice || report.Users.SourceRows != 2 ||
		report.Users.TargetRows != 2 || report.Users.SourceChecksum != report.Users.TargetChecksum {
		t.Fatalf("report = %#v", report)
	}
	page, err := database.ListAdminUsers(ctx, AdminUserFilter{Limit: 10})
	if err != nil || len(page.Items) != 2 || page.Items[0].ID != 20 || page.Items[1].ID != 10 {
		t.Fatalf("admin user page = (%#v, %v)", page, err)
	}
	admin, err := database.GetAdminUser(ctx, 10)
	if err != nil || !admin.IsAdmin || admin.GroupID == nil || *admin.GroupID != 9 || admin.LastLoginAt == nil ||
		!admin.LastLoginAt.Equal(time.Unix(1_700_000_100, 0).UTC()) {
		t.Fatalf("imported admin = (%#v, %v)", admin, err)
	}
	var inviterID int64
	var passwordHash, token string
	if err := database.db.QueryRowContext(ctx, `SELECT invite_user_id, password_hash, subscription_token FROM users WHERE id = 20`).Scan(&inviterID, &passwordHash, &token); err != nil {
		t.Fatal(err)
	}
	if inviterID != 10 || passwordHash != input.Users[1].PasswordHash || token != input.Users[1].SubscriptionToken {
		t.Fatal("imported credential or invitation state differs from the source")
	}
	var encodedReport string
	if err := database.db.QueryRowContext(ctx, `SELECT report_json FROM legacy_migration_runs WHERE slice = ?`, LegacyHumanUsersSlice).Scan(&encodedReport); err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{input.Users[0].Email, input.Users[0].PasswordHash, input.Users[0].SubscriptionToken} {
		if strings.Contains(encodedReport, secret) {
			t.Fatal("migration ledger report contains user credential or identity data")
		}
	}
	repeated, err := database.ImportLegacyHumanUsers(ctx, input, time.Unix(500, 0))
	if err != nil || !repeated.AlreadyApplied || !repeated.AppliedAt.Equal(time.Unix(400, 0).UTC()) {
		t.Fatalf("repeated import = (%#v, %v)", repeated, err)
	}
	var runs int
	if err := database.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM legacy_migration_runs`).Scan(&runs); err != nil || runs != 4 {
		t.Fatalf("migration runs = %d, err=%v", runs, err)
	}
}

func TestImportLegacyHumanUsersPreservesPlanAndTrafficResetState(t *testing.T) {
	database := newLegacyHumanUserTarget(t)
	ctx := context.Background()
	if _, err := database.ImportLegacyGroupsRoutes(ctx, validLegacyGroupsRoutesImport(), time.Unix(100, 0)); err != nil {
		t.Fatal(err)
	}
	plan := LegacyPlan{
		ID: 77, GroupID: int64Pointer(9), TransferEnableGiB: 100, Name: "Legacy plan", Show: true,
		Renew: true, Sell: true, Prices: PlanPrices{"monthly": 999}, Tags: []string{"popular"}, CreatedAt: 100, UpdatedAt: 110,
	}
	planInput := LegacyPlansImport{
		Slice: LegacyPlansSlice, SourceSHA256: strings.Repeat("a", 64), SourceSize: 8192,
		Plans: []LegacyPlan{plan}, RollbackBackupPath: "/var/lib/xboard-backups/pre-plans.xbbackup",
		RollbackBackupSHA256: strings.Repeat("b", 64), TrafficResetMethod: 1,
		SettingsChecksum: LegacyPlanSettingsChecksum(1),
	}
	planInput.Checksum = LegacyPlansChecksum(planInput.Plans)
	if _, err := database.ImportLegacyPlans(ctx, planInput, time.Unix(200, 0)); err != nil {
		t.Fatal(err)
	}

	input := validLegacyHumanUsersImport(t)
	nextResetAt := int64(1_700_000_300)
	lastResetAt := int64(1_700_000_250)
	input.Users[1].PlanID = int64Pointer(77)
	input.Users[1].NextResetAt = &nextResetAt
	input.Users[1].LastResetAt = &lastResetAt
	input.Users[1].ResetCount = 3
	input.Checksum = LegacyHumanUsersChecksum(input.Users)
	report, err := database.ImportLegacyHumanUsers(ctx, input, time.Unix(300, 0))
	if err != nil || report.Users.SourceChecksum != report.Users.TargetChecksum {
		t.Fatalf("ImportLegacyHumanUsers() = (%#v, %v)", report, err)
	}
	var planID, next, last, resetCount int64
	if err := database.db.QueryRowContext(ctx, `
		SELECT plan_id, next_reset_at, last_reset_at, reset_count FROM users WHERE id = 20
	`).Scan(&planID, &next, &last, &resetCount); err != nil {
		t.Fatal(err)
	}
	if planID != 77 || next != nextResetAt || last != lastResetAt || resetCount != 3 {
		t.Fatalf("plan/reset state = %d/%d/%d/%d", planID, next, last, resetCount)
	}
}

func TestImportLegacyHumanUsersRejectsMissingPlanBeforeReplacingBootstrap(t *testing.T) {
	database := newLegacyHumanUserTarget(t)
	ctx := context.Background()
	if _, err := database.ImportLegacyGroupsRoutes(ctx, validLegacyGroupsRoutesImport(), time.Unix(100, 0)); err != nil {
		t.Fatal(err)
	}
	input := validLegacyHumanUsersImport(t)
	input.Users[1].PlanID = int64Pointer(404)
	input.Checksum = LegacyHumanUsersChecksum(input.Users)
	if _, err := database.ImportLegacyHumanUsers(ctx, input, time.Unix(200, 0)); !errors.Is(err, ErrConflict) || !strings.Contains(err.Error(), "missing plan 404") {
		t.Fatalf("ImportLegacyHumanUsers(missing plan) error = %v", err)
	}
	var bootstrapCount, runs int
	if err := database.db.QueryRow(`SELECT COUNT(*) FROM users WHERE email = 'bootstrap@example.test'`).Scan(&bootstrapCount); err != nil {
		t.Fatal(err)
	}
	if err := database.db.QueryRow(`SELECT COUNT(*) FROM legacy_migration_runs WHERE slice = ?`, LegacyHumanUsersSlice).Scan(&runs); err != nil {
		t.Fatal(err)
	}
	if bootstrapCount != 1 || runs != 0 {
		t.Fatalf("rejected import changed target: bootstrap=%d runs=%d", bootstrapCount, runs)
	}
}

func TestImportLegacyHumanUsersRejectsDependenciesAndRollsBackExactly(t *testing.T) {
	database := newLegacyHumanUserTarget(t)
	ctx := context.Background()
	bootstrap, err := database.FindUserByEmail(ctx, "bootstrap@example.test")
	if err != nil {
		t.Fatal(err)
	}
	if err := database.CreateSession(ctx, bootstrap.ID, "bootstrap-session", "bootstrap-csrf", time.Unix(500, 0), time.Unix(100, 0)); err != nil {
		t.Fatal(err)
	}
	input := validLegacyHumanUsersImport(t)
	input.Users[0].GroupID = nil
	input.Users[1].GroupID = nil
	input.Checksum = LegacyHumanUsersChecksum(input.Users)
	_, err = database.ImportLegacyHumanUsers(ctx, input, time.Unix(200, 0))
	if !errors.Is(err, ErrConflict) || !strings.Contains(err.Error(), "admin_sessions.user_id") {
		t.Fatalf("ImportLegacyHumanUsers(dependency) error = %v", err)
	}
	preserved, err := database.FindUserByID(ctx, bootstrap.ID)
	if err != nil || preserved.Email != bootstrap.Email || preserved.PasswordHash != bootstrap.PasswordHash {
		t.Fatalf("bootstrap after rejected import = (%#v, %v)", preserved, err)
	}
	var users, sessions, runs int
	for query, destination := range map[string]*int{
		`SELECT COUNT(*) FROM users`:                 &users,
		`SELECT COUNT(*) FROM admin_sessions`:        &sessions,
		`SELECT COUNT(*) FROM legacy_migration_runs`: &runs,
	} {
		if err := database.db.QueryRowContext(ctx, query).Scan(destination); err != nil {
			t.Fatal(err)
		}
	}
	if users != 1 || sessions != 1 || runs != 0 {
		t.Fatalf("rejected import changed target: users=%d sessions=%d runs=%d", users, sessions, runs)
	}
}

func TestImportLegacyHumanUsersRequiresGroupsAndExplicitReplacement(t *testing.T) {
	for _, scenario := range []struct {
		name   string
		mutate func(*LegacyHumanUsersImport)
	}{
		{name: "missing explicit replacement", mutate: func(input *LegacyHumanUsersImport) { input.ReplaceBootstrapAdmin = false }},
		{name: "missing target group", mutate: func(input *LegacyHumanUsersImport) {}},
		{name: "different checksum", mutate: func(input *LegacyHumanUsersImport) { input.Checksum = strings.Repeat("0", 64) }},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			database := newLegacyHumanUserTarget(t)
			input := validLegacyHumanUsersImport(t)
			scenario.mutate(&input)
			if _, err := database.ImportLegacyHumanUsers(context.Background(), input, time.Unix(200, 0)); err == nil {
				t.Fatal("ImportLegacyHumanUsers() unexpectedly succeeded")
			}
			var users, runs int
			_ = database.db.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&users)
			_ = database.db.QueryRow(`SELECT COUNT(*) FROM legacy_migration_runs`).Scan(&runs)
			if users != 1 || runs != 0 {
				t.Fatalf("rejected import changed target: users=%d runs=%d", users, runs)
			}
		})
	}
}

func TestConcurrentLegacyHumanUserImportsAreSerializedAndIdempotent(t *testing.T) {
	database := newLegacyHumanUserTarget(t)
	ctx := context.Background()
	if _, err := database.ImportLegacyGroupsRoutes(ctx, validLegacyGroupsRoutesImport(), time.Unix(100, 0)); err != nil {
		t.Fatal(err)
	}
	input := validLegacyHumanUsersImport(t)
	start := make(chan struct{})
	results := make(chan LegacyHumanUsersImportReport, 2)
	errorsFound := make(chan error, 2)
	var workers sync.WaitGroup
	for range 2 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			report, err := database.ImportLegacyHumanUsers(ctx, input, time.Unix(200, 0))
			if err != nil {
				errorsFound <- err
				return
			}
			results <- report
		}()
	}
	close(start)
	workers.Wait()
	close(results)
	close(errorsFound)
	for err := range errorsFound {
		t.Fatalf("concurrent import error = %v", err)
	}
	var applied, repeated int
	for report := range results {
		if report.AlreadyApplied {
			repeated++
		} else {
			applied++
		}
	}
	if applied != 1 || repeated != 1 {
		t.Fatalf("concurrent reports applied=%d repeated=%d", applied, repeated)
	}
}

func BenchmarkImportLegacyHumanUsers100K(b *testing.B) {
	hash, err := bcrypt.GenerateFromPassword([]byte("legacy-password-123"), 10)
	if err != nil {
		b.Fatal(err)
	}
	phpHash := "$2y$" + string(hash[4:])
	users := make([]LegacyHumanUser, 100_000)
	for index := range users {
		id := int64(index + 1)
		users[index] = LegacyHumanUser{
			ID: id, Email: fmt.Sprintf("bench-%06d@example.test", id), PasswordHash: phpHash, IsAdmin: index == 0,
			UUID: fmt.Sprintf("00000000-0000-4000-8000-%012x", id), SubscriptionToken: fmt.Sprintf("%032x", id),
			CreatedAt: 100, UpdatedAt: 100,
		}
	}
	input := LegacyHumanUsersImport{
		Slice: LegacyHumanUsersSlice, SourceSHA256: strings.Repeat("d", 64), SourceSize: 64 << 20,
		Users: users, RollbackBackupPath: "/var/lib/xboard-backups/benchmark.xbbackup",
		RollbackBackupSHA256: strings.Repeat("f", 64), ReplaceBootstrapAdmin: true,
	}
	input.Checksum = LegacyHumanUsersChecksum(input.Users)
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		b.StopTimer()
		databasePath := filepath.Join(b.TempDir(), fmt.Sprintf("target-%d.db", time.Now().UnixNano()))
		database, err := OpenSQLite("file:" + filepath.ToSlash(databasePath))
		if err != nil {
			b.Fatal(err)
		}
		if err := database.Migrate(context.Background()); err != nil {
			b.Fatal(err)
		}
		if _, err := database.BootstrapAdmin(context.Background(), "bootstrap@example.test", "bootstrap-hash", time.Unix(50, 0)); err != nil {
			b.Fatal(err)
		}
		b.StartTimer()
		report, err := database.ImportLegacyHumanUsers(context.Background(), input, time.Unix(200, 0))
		b.StopTimer()
		if err != nil || report.Users.TargetRows != len(users) {
			_ = database.Close()
			b.Fatalf("ImportLegacyHumanUsers() rows=%d error=%v", report.Users.TargetRows, err)
		}
		if err := database.Close(); err != nil {
			b.Fatal(err)
		}
		b.StartTimer()
	}
	b.ReportMetric(100_000, "users/op")
}

func validLegacyHumanUsersImport(t testing.TB) LegacyHumanUsersImport {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte("legacy-password-123"), 10)
	if err != nil {
		t.Fatal(err)
	}
	phpHash := "$2y$" + string(hash[4:])
	groupID := int64(9)
	inviterID := int64(10)
	lastLoginAt := int64(1_700_000_100)
	expiredAt := int64(1_800_000_000)
	lastOnlineAt := int64(1_700_000_200)
	input := LegacyHumanUsersImport{
		Slice: LegacyHumanUsersSlice, SourceSHA256: strings.Repeat("d", 64), SourceSize: 16_384,
		RollbackBackupPath:   "/var/lib/xboard-backups/pre-human-users.xbbackup",
		RollbackBackupSHA256: strings.Repeat("f", 64), ReplaceBootstrapAdmin: true,
		Users: []LegacyHumanUser{
			{ID: 10, Email: "admin@example.test", PasswordHash: phpHash, IsAdmin: true, UUID: "11111111-1111-4111-8111-111111111111",
				GroupID: &groupID, LastLoginAt: &lastLoginAt, SubscriptionToken: "11111111111111111111111111111111", CreatedAt: 100, UpdatedAt: 110},
			{ID: 20, InviteUserID: &inviterID, Email: "user@example.test", PasswordHash: phpHash, UUID: "22222222-2222-4222-8222-222222222222",
				GroupID: &groupID, TransferEnable: 1_000, TrafficUpload: 10, TrafficDownload: 20, ExpiredAt: &expiredAt,
				SpeedLimit: 50, DeviceLimit: 3, LastOnlineAt: &lastOnlineAt, SubscriptionToken: "22222222222222222222222222222222", CreatedAt: 101, UpdatedAt: 111},
		},
	}
	input.Checksum = LegacyHumanUsersChecksum(input.Users)
	return input
}

func newLegacyHumanUserTarget(t *testing.T) *Store {
	t.Helper()
	database := newTestStore(t)
	if _, err := database.BootstrapAdmin(context.Background(), "bootstrap@example.test", "bootstrap-hash", time.Unix(50, 0)); err != nil {
		t.Fatalf("BootstrapAdmin() error = %v", err)
	}
	return database
}
