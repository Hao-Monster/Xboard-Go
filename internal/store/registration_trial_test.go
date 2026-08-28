package store

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func BenchmarkRegisterUserTrialPolicy(b *testing.B) {
	for _, enabled := range []bool{false, true} {
		name := "disabled"
		if enabled {
			name = "enabled"
		}
		b.Run(name, func(b *testing.B) {
			database := newTestStore(b)
			now := time.Date(2026, 8, 29, 14, 0, 0, 0, time.UTC)
			if enabled {
				speed, devices, reset := 77, 5, 1
				plan, err := database.CreatePlan(b.Context(), SavePlanInput{
					Name: "Benchmark trial", TransferEnableGiB: 13, SpeedLimit: &speed, DeviceLimit: &devices,
					ResetTrafficMethod: &reset, Prices: PlanPrices{}, Tags: []string{},
				}, now)
				if err != nil {
					b.Fatal(err)
				}
				if _, err := database.db.ExecContext(b.Context(), `UPDATE app_settings SET try_out_plan_id=?,try_out_hour=2 WHERE id=1`, plan.ID); err != nil {
					b.Fatal(err)
				}
			}
			b.ReportAllocs()
			b.ResetTimer()
			for index := range b.N {
				if _, err := database.RegisterUser(b.Context(), RegisterUserInput{
					Email: fmt.Sprintf("benchmark-trial-%t-%08d@example.test", enabled, index), PasswordHash: "opaque-hash",
				}, now); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func TestRegistrationTrialPlanAppliesLegacyEntitlementAcrossRegistrationAndAdminCreation(t *testing.T) {
	database := newTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 8, 29, 9, 30, 0, 0, time.UTC)
	group, err := database.CreateServerGroup(ctx, "registration-trial", now)
	if err != nil {
		t.Fatal(err)
	}
	speedLimit, deviceLimit, resetMethod, capacityLimit := 77, 5, 1, 1
	trialPlan, err := database.CreatePlan(ctx, SavePlanInput{
		Name: "Registration trial", GroupID: &group.ID, TransferEnableGiB: 13,
		SpeedLimit: &speedLimit, DeviceLimit: &deviceLimit, ResetTrafficMethod: &resetMethod,
		CapacityLimit: &capacityLimit, Prices: PlanPrices{}, Tags: []string{},
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	capacityExpiry := now.Add(30 * 24 * time.Hour)
	if _, err := database.CreateAdminUser(ctx, CreateAdminUserInput{
		Email: "trial-capacity-occupant@example.test", PasswordHash: "hash", PlanID: &trialPlan.ID, ExpiredAt: &capacityExpiry,
	}, now); err != nil {
		t.Fatal(err)
	}
	administrator, err := database.CreateAdminUser(ctx, CreateAdminUserInput{
		Email: "trial-admin@example.test", PasswordHash: "hash", IsAdmin: true,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	settings := updateRegistrationPolicy(t, database, administrator.ID, func(input *SaveSiteSettingsInput) {
		input.TrialPlanID = pointerTo(trialPlan.ID)
		input.TrialHours = pointerTo(2)
	})
	if settings.TrialPlanID != trialPlan.ID || settings.TrialHours != 2 {
		t.Fatalf("saved trial settings = %#v", settings)
	}

	registered, err := database.RegisterUser(ctx, RegisterUserInput{
		Email: "registered-trial@example.test", PasswordHash: "hash",
	}, now)
	if err != nil {
		t.Fatalf("RegisterUser() error = %v", err)
	}
	assertTrialUserRow(t, database, registered.ID, trialPlan.ID, group.ID, 13*bytesPerGiB, speedLimit, deviceLimit, now.Add(2*time.Hour))

	created, err := database.CreateAdminUsers(ctx, []CreateAdminUserInput{
		{Email: "admin-trial-a@example.test", PasswordHash: "hash"},
		{Email: "admin-trial-distributor@example.test", PasswordHash: "hash", IsDistributor: true, DistributorName: "Trial distributor"},
	}, now)
	if err != nil {
		t.Fatalf("CreateAdminUsers() error = %v", err)
	}
	if len(created) != 2 {
		t.Fatalf("created users = %#v", created)
	}
	trialUser := created[0].User
	if trialUser.PlanID == nil || *trialUser.PlanID != trialPlan.ID || trialUser.GroupID == nil || *trialUser.GroupID != group.ID ||
		trialUser.TransferEnable != 13*bytesPerGiB || trialUser.SpeedLimit != speedLimit || trialUser.DeviceLimit != deviceLimit ||
		trialUser.ExpiredAt == nil || !trialUser.ExpiredAt.Equal(now.Add(2*time.Hour)) || trialUser.NextResetAt == nil {
		t.Fatalf("admin-created trial user = %#v", trialUser)
	}
	distributor := created[1].User
	if distributor.PlanID != nil || distributor.GroupID != nil || distributor.TransferEnable != 0 || distributor.ExpiredAt != nil || distributor.NextResetAt != nil {
		t.Fatalf("distributor unexpectedly received trial = %#v", distributor)
	}

	explicitPlan, err := database.CreatePlan(ctx, SavePlanInput{
		Name: "Explicit plan", TransferEnableGiB: 3, Prices: PlanPrices{}, Tags: []string{},
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	explicitExpiry := now.Add(24 * time.Hour)
	explicit, err := database.CreateAdminUser(ctx, CreateAdminUserInput{
		Email: "explicit-plan@example.test", PasswordHash: "hash", PlanID: &explicitPlan.ID, ExpiredAt: &explicitExpiry,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	if explicit.PlanID == nil || *explicit.PlanID != explicitPlan.ID || explicit.TransferEnable != 3*bytesPerGiB || explicit.ExpiredAt == nil || !explicit.ExpiredAt.Equal(explicitExpiry) {
		t.Fatalf("explicit plan did not win = %#v", explicit)
	}
	if err := database.DeletePlan(ctx, trialPlan.ID); !errors.Is(err, ErrConflict) {
		t.Fatalf("DeletePlan(configured trial) error = %v, want ErrConflict", err)
	}
}

func TestRegistrationTrialSettingsRejectUnsafeValuesAndMissingPlans(t *testing.T) {
	database := newTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 8, 29, 10, 0, 0, 0, time.UTC)
	administrator, err := database.CreateAdminUser(ctx, CreateAdminUserInput{
		Email: "trial-settings-admin@example.test", PasswordHash: "hash", IsAdmin: true,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	for name, testCase := range map[string]struct {
		planID int64
		hours  int
	}{
		"negative plan":   {-1, 1},
		"missing plan":    {999999, 1},
		"zero hours":      {0, 0},
		"negative hours":  {0, -2},
		"excessive hours": {0, 8761},
	} {
		t.Run(name, func(t *testing.T) {
			settings, getErr := database.GetSiteSettings(ctx)
			if getErr != nil {
				t.Fatal(getErr)
			}
			input := siteSettingsInput(settings)
			input.TrialPlanID = pointerTo(testCase.planID)
			input.TrialHours = pointerTo(testCase.hours)
			if _, updateErr := database.UpdateSiteSettings(ctx, administrator.ID, settings.Revision, input, now); !errors.Is(updateErr, ErrInvalidInput) {
				t.Fatalf("UpdateSiteSettings() error = %v, want ErrInvalidInput", updateErr)
			}
		})
	}
	configOnlyPlan, err := database.CreatePlan(ctx, SavePlanInput{
		Name: "Configured only", TransferEnableGiB: 1, Prices: PlanPrices{}, Tags: []string{},
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	settings := updateRegistrationPolicy(t, database, administrator.ID, func(input *SaveSiteSettingsInput) {
		input.TrialPlanID = pointerTo(configOnlyPlan.ID)
		input.TrialHours = pointerTo(24)
	})
	if err := database.DeletePlan(ctx, configOnlyPlan.ID); !errors.Is(err, ErrConflict) {
		t.Fatalf("DeletePlan(config-only trial) error = %v, want ErrConflict", err)
	}
	input := siteSettingsInput(settings)
	input.TrialPlanID = pointerTo(int64(0))
	input.TrialHours = pointerTo(1)
	if _, err := database.UpdateSiteSettings(ctx, administrator.ID, settings.Revision, input, now); err != nil {
		t.Fatal(err)
	}
	if err := database.DeletePlan(ctx, configOnlyPlan.ID); err != nil {
		t.Fatalf("DeletePlan(disabled trial) error = %v", err)
	}
}

func TestSchemaV44AddsSafeRegistrationTrialDefaultsAndConstraints(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "trial-v43.db")
	database, err := OpenSQLite("file:" + filepath.ToSlash(databasePath))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	ctx := context.Background()
	if err := database.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.ExecContext(ctx, `
		ALTER TABLE app_settings DROP COLUMN try_out_hour;
		ALTER TABLE app_settings DROP COLUMN try_out_plan_id;
		PRAGMA user_version = 43;
	`); err != nil {
		t.Fatal(err)
	}
	if err := database.Migrate(ctx); err != nil {
		t.Fatalf("Migrate(v43 to v44) error = %v", err)
	}
	settings, err := database.GetSiteSettings(ctx)
	if err != nil || settings.TrialPlanID != 0 || settings.TrialHours != 1 {
		t.Fatalf("migrated trial defaults = %#v err=%v", settings, err)
	}
	for _, statement := range []string{
		`UPDATE app_settings SET try_out_plan_id = -1 WHERE id = 1`,
		`UPDATE app_settings SET try_out_hour = 0 WHERE id = 1`,
		`UPDATE app_settings SET try_out_hour = 8761 WHERE id = 1`,
	} {
		if _, err := database.db.ExecContext(ctx, statement); err == nil || !strings.Contains(strings.ToLower(err.Error()), "constraint") {
			t.Fatalf("unsafe schema write %q error = %v", statement, err)
		}
	}
}

func TestImportLegacyRegistrationTrialSettingsVerifiesAndNormalizesMissingPlan(t *testing.T) {
	now := time.Date(2026, 8, 29, 11, 0, 0, 0, time.UTC)
	t.Run("existing plan", func(t *testing.T) {
		database := newTestStore(t)
		plan, err := database.CreatePlan(t.Context(), SavePlanInput{
			Name: "Imported trial", TransferEnableGiB: 9, Prices: PlanPrices{}, Tags: []string{},
		}, now)
		if err != nil {
			t.Fatal(err)
		}
		settings := LegacyRegistrationTrialSettings{PlanID: plan.ID, Hours: 72}
		input := legacyRegistrationTrialImport(settings, "a")
		report, err := database.ImportLegacyRegistrationTrialSettings(t.Context(), input, now)
		if err != nil || report.NormalizedMissingPlan || report.Settings.SourceChecksum != report.Settings.TargetChecksum {
			t.Fatalf("report=%#v err=%v", report, err)
		}
		actual, err := database.GetSiteSettings(t.Context())
		if err != nil || actual.TrialPlanID != plan.ID || actual.TrialHours != 72 {
			t.Fatalf("settings=%#v err=%v", actual, err)
		}
		repeated, err := database.ImportLegacyRegistrationTrialSettings(t.Context(), input, now.Add(time.Hour))
		if err != nil || !repeated.AlreadyApplied || repeated.AppliedAt != now {
			t.Fatalf("repeated=%#v err=%v", repeated, err)
		}
	})
	t.Run("missing plan", func(t *testing.T) {
		database := newTestStore(t)
		settings := LegacyRegistrationTrialSettings{PlanID: 999, Hours: 24}
		report, err := database.ImportLegacyRegistrationTrialSettings(t.Context(), legacyRegistrationTrialImport(settings, "c"), now)
		if err != nil || !report.NormalizedMissingPlan || report.Settings.SourceChecksum == report.Settings.TargetChecksum {
			t.Fatalf("report=%#v err=%v", report, err)
		}
		actual, err := database.GetSiteSettings(t.Context())
		if err != nil || actual.TrialPlanID != 0 || actual.TrialHours != 1 {
			t.Fatalf("normalized settings=%#v err=%v", actual, err)
		}
	})
}

func legacyRegistrationTrialImport(settings LegacyRegistrationTrialSettings, marker string) LegacyRegistrationTrialSettingsImport {
	return LegacyRegistrationTrialSettingsImport{
		Slice: LegacyRegistrationTrialSettingsSlice, SourceSHA256: strings.Repeat(marker, 64), SourceSize: 4096,
		Settings: settings, Checksum: LegacyRegistrationTrialSettingsChecksum(settings),
		RollbackBackupPath: "pre-trial.xbbackup", RollbackBackupSHA256: strings.Repeat("b", 64),
	}
}

func assertTrialUserRow(t *testing.T, database *Store, userID, planID, groupID, transfer int64, speed, devices int, expiresAt time.Time) {
	t.Helper()
	var actualPlanID, actualGroupID, actualTransfer, actualExpiry, actualNextReset int64
	var actualSpeed, actualDevices int
	if err := database.db.QueryRowContext(t.Context(), `
		SELECT plan_id, group_id, transfer_enable, speed_limit, device_limit, expired_at, next_reset_at
		FROM users WHERE id = ?
	`, userID).Scan(&actualPlanID, &actualGroupID, &actualTransfer, &actualSpeed, &actualDevices, &actualExpiry, &actualNextReset); err != nil {
		t.Fatal(err)
	}
	if actualPlanID != planID || actualGroupID != groupID || actualTransfer != transfer || actualSpeed != speed || actualDevices != devices ||
		actualExpiry != expiresAt.Unix() || actualNextReset != expiresAt.Unix() {
		t.Fatalf("trial row = plan=%d group=%d transfer=%d speed=%d devices=%d expiry=%d reset=%d", actualPlanID, actualGroupID, actualTransfer, actualSpeed, actualDevices, actualExpiry, actualNextReset)
	}
}
