package store

import (
	"bytes"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestSchemaV20MigrationPreservesV18DataAndAddsSecureLoginLinkDefaults(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "schema-v18.db")
	database, err := OpenSQLite("file:" + filepath.ToSlash(databasePath))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	ctx := t.Context()
	for step, schema := range []string{
		schemaV1, schemaV2, schemaV3, schemaV4, schemaV5, schemaV6, schemaV7, schemaV7Constraints,
		schemaV8, schemaV9, schemaV10, schemaV11, schemaV12, schemaV13, schemaV14, schemaV15, schemaV16, schemaV17, schemaV18,
	} {
		if _, err := database.db.ExecContext(ctx, schema); err != nil {
			t.Fatalf("apply pre-v19 schema step %d: %v", step+1, err)
		}
	}
	if _, err := database.db.ExecContext(ctx, `PRAGMA user_version = 18`); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 25, 3, 0, 0, 0, time.UTC)
	user, err := database.CreateAdminUser(ctx, CreateAdminUserInput{Email: "v18@example.test", PasswordHash: "preserved-hash"}, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := database.Migrate(ctx); err != nil {
		t.Fatalf("Migrate(v18 to current) error = %v", err)
	}
	settings, err := database.GetSiteSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var version, preservedUsers, tables, codes, relationships int
	for query, target := range map[string]*int{
		`PRAGMA user_version`: &version,
		`SELECT COUNT(*) FROM users WHERE id = ` + fmt.Sprint(user.ID):                      &preservedUsers,
		`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='invitation_codes'`: &tables,
		`SELECT COUNT(*) FROM invitation_codes`:                                             &codes,
		`SELECT COUNT(*) FROM users WHERE invite_user_id IS NOT NULL`:                       &relationships,
	} {
		if err := database.db.QueryRowContext(ctx, query).Scan(target); err != nil {
			t.Fatal(err)
		}
	}
	if version != currentSchemaVersion || preservedUsers != 1 || tables != 1 || codes != 0 || relationships != 0 ||
		settings.InvitationForceEnabled || settings.InvitationCodeLimit != 5 || settings.InvitationNeverExpire || settings.MailLoginEnabled {
		t.Fatalf("migration version=%d users=%d tables=%d codes=%d relationships=%d settings=%#v", version, preservedUsers, tables, codes, relationships, settings)
	}
}

func TestInvitationCodeGenerationLimitAndOwnershipAreAtomic(t *testing.T) {
	database := newTestStore(t)
	ctx := t.Context()
	now := time.Date(2026, 8, 25, 3, 30, 0, 0, time.UTC)
	owner := createTicketTestUser(t, database, "invitation-owner@example.test", now)
	other := createTicketTestUser(t, database, "invitation-other@example.test", now)
	setInvitationPolicy(t, database, owner.ID, false, 1, false, now)
	if required, err := database.InvitationProtectionRequired(ctx); err != nil || required {
		t.Fatalf("empty optional invitation protection required=%t err=%v", required, err)
	}
	if ownerID, ciphertext, exists, err := database.InvitationProtectionProbe(ctx); err != nil || exists || ownerID != 0 || ciphertext != nil {
		t.Fatalf("empty invitation protection probe owner=%d cipher=%x exists=%t err=%v", ownerID, ciphertext, exists, err)
	}

	inputs := []CreateInvitationCodeInput{
		{CodeDigest: bytes.Repeat([]byte{0x11}, 32), CodeCipher: bytes.Repeat([]byte{0x21}, 40)},
		{CodeDigest: bytes.Repeat([]byte{0x12}, 32), CodeCipher: bytes.Repeat([]byte{0x22}, 40)},
	}
	var group sync.WaitGroup
	errorsFound := make(chan error, len(inputs))
	for _, input := range inputs {
		input := input
		group.Add(1)
		go func() {
			defer group.Done()
			_, err := database.CreateInvitationCode(ctx, owner.ID, input, now)
			errorsFound <- err
		}()
	}
	group.Wait()
	close(errorsFound)
	var created, limited int
	for err := range errorsFound {
		switch {
		case err == nil:
			created++
		case errors.Is(err, ErrInvitationCodeLimit):
			limited++
		default:
			t.Fatalf("CreateInvitationCode() error = %v", err)
		}
	}
	if created != 1 || limited != 1 {
		t.Fatalf("concurrent generation created=%d limited=%d", created, limited)
	}
	if required, err := database.InvitationProtectionRequired(ctx); err != nil || !required {
		t.Fatalf("stored invitation protection required=%t err=%v", required, err)
	}
	if ownerID, ciphertext, exists, err := database.InvitationProtectionProbe(ctx); err != nil || !exists || ownerID != owner.ID || len(ciphertext) != 40 {
		t.Fatalf("stored invitation protection probe owner=%d cipher_bytes=%d exists=%t err=%v", ownerID, len(ciphertext), exists, err)
	}
	summary, err := database.GetInvitationSummary(ctx, owner.ID)
	if err != nil || len(summary.Codes) != 1 || summary.InvitedCount != 0 {
		t.Fatalf("owner summary=%#v err=%v", summary, err)
	}
	if len(summary.Codes[0].CodeCipher) != 40 || len(summary.Codes[0].CodeDigest) != 0 {
		t.Fatalf("public store record leaked digest or lost cipher: %#v", summary.Codes[0])
	}
	otherSummary, err := database.GetInvitationSummary(ctx, other.ID)
	if err != nil || len(otherSummary.Codes) != 0 {
		t.Fatalf("other summary=%#v err=%v", otherSummary, err)
	}
	var plaintextMatches int
	if err := database.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM invitation_codes WHERE code_cipher IN ('Abcd1234', 'Zyxw9876')`).Scan(&plaintextMatches); err != nil {
		t.Fatal(err)
	}
	if plaintextMatches != 0 {
		t.Fatal("invitation code was stored as plaintext")
	}
	setInvitationPolicy(t, database, owner.ID, false, 0, false, now.Add(time.Minute))
	if _, err := database.CreateInvitationCode(ctx, other.ID, CreateInvitationCodeInput{
		CodeDigest: bytes.Repeat([]byte{0x13}, 32), CodeCipher: bytes.Repeat([]byte{0x23}, 40),
	}, now.Add(time.Minute)); !errors.Is(err, ErrInvitationCodeLimit) {
		t.Fatalf("zero generation limit error=%v, want ErrInvitationCodeLimit", err)
	}
}

func TestInvitationRegistrationSingleUseReusableAndOptionalInvalid(t *testing.T) {
	database := newTestStore(t)
	ctx := t.Context()
	now := time.Date(2026, 8, 25, 4, 0, 0, 0, time.UTC)
	owner := createTicketTestUser(t, database, "referrer@example.test", now)
	singleDigest := bytes.Repeat([]byte{0x31}, 32)
	setInvitationPolicy(t, database, owner.ID, true, 5, false, now)
	if _, err := database.CreateInvitationCode(ctx, owner.ID, CreateInvitationCodeInput{
		CodeDigest: singleDigest, CodeCipher: bytes.Repeat([]byte{0x41}, 40),
	}, now); err != nil {
		t.Fatal(err)
	}
	if err := database.CheckInvitationCode(ctx, singleDigest); err != nil {
		t.Fatalf("CheckInvitationCode(valid) error = %v", err)
	}
	if err := database.CheckInvitationCode(ctx, bytes.Repeat([]byte{0xff}, 32)); !errors.Is(err, ErrInvitationCodeInvalid) {
		t.Fatalf("CheckInvitationCode(invalid) error = %v", err)
	}

	registrationErrors := make(chan error, 2)
	var group sync.WaitGroup
	for index := range 2 {
		index := index
		group.Add(1)
		go func() {
			defer group.Done()
			_, err := database.RegisterUserWithSession(ctx, RegisterUserInput{
				Email: fmt.Sprintf("single-%d@example.test", index), PasswordHash: "hash", InvitationCodeDigest: singleDigest,
			}, RegistrationSessionInput{
				TokenHash: strings.Repeat(string(rune('a'+index)), 64),
				CSRFHash:  strings.Repeat(string(rune('c'+index)), 64), ExpiresAt: now.Add(time.Hour),
			}, now)
			registrationErrors <- err
		}()
	}
	group.Wait()
	close(registrationErrors)
	var registered, rejected int
	for err := range registrationErrors {
		switch {
		case err == nil:
			registered++
		case errors.Is(err, ErrInvitationCodeInvalid):
			rejected++
		default:
			t.Fatalf("single-use registration error = %v", err)
		}
	}
	summary, err := database.GetInvitationSummary(ctx, owner.ID)
	if err != nil || registered != 1 || rejected != 1 || len(summary.Codes) != 0 || summary.InvitedCount != 1 {
		t.Fatalf("single-use registered=%d rejected=%d summary=%#v err=%v", registered, rejected, summary, err)
	}
	if err := database.IncrementInvitationCodeView(ctx, singleDigest, now.Add(time.Minute)); err != nil {
		t.Fatalf("consumed invitation view error = %v", err)
	}
	var consumedPV int64
	if err := database.db.QueryRowContext(ctx, `SELECT pv FROM invitation_codes WHERE code_digest = ?`, singleDigest).Scan(&consumedPV); err != nil || consumedPV != 1 {
		t.Fatalf("consumed invitation pv=%d err=%v", consumedPV, err)
	}

	setInvitationPolicy(t, database, owner.ID, false, 5, false, now.Add(2*time.Minute))
	optionalUser, err := database.RegisterUser(ctx, RegisterUserInput{
		Email: "optional-invalid@example.test", PasswordHash: "hash", InvitationCodeDigest: bytes.Repeat([]byte{0xfe}, 32),
	}, now.Add(2*time.Minute))
	if err != nil {
		t.Fatalf("optional invalid registration error = %v", err)
	}
	var optionalInviter *int64
	if err := database.db.QueryRowContext(ctx, `SELECT invite_user_id FROM users WHERE id = ?`, optionalUser.ID).Scan(&optionalInviter); err != nil {
		t.Fatal(err)
	}
	if optionalInviter != nil {
		t.Fatalf("optional invalid code created relationship to %v", optionalInviter)
	}

	reusableDigest := bytes.Repeat([]byte{0x32}, 32)
	setInvitationPolicy(t, database, owner.ID, true, 5, true, now.Add(3*time.Minute))
	if _, err := database.CreateInvitationCode(ctx, owner.ID, CreateInvitationCodeInput{
		CodeDigest: reusableDigest, CodeCipher: bytes.Repeat([]byte{0x42}, 40),
	}, now.Add(3*time.Minute)); err != nil {
		t.Fatal(err)
	}
	for index := range 2 {
		if _, err := database.RegisterUser(ctx, RegisterUserInput{
			Email: fmt.Sprintf("reusable-%d@example.test", index), PasswordHash: "hash", InvitationCodeDigest: reusableDigest,
		}, now.Add(time.Duration(index+4)*time.Minute)); err != nil {
			t.Fatalf("reusable registration %d error = %v", index, err)
		}
	}
	summary, err = database.GetInvitationSummary(ctx, owner.ID)
	if err != nil || len(summary.Codes) != 1 || summary.InvitedCount != 3 {
		t.Fatalf("reusable summary=%#v err=%v", summary, err)
	}
}

func TestInvitationConsumptionRollsBackWithRegistrationAndPVIsPrivate(t *testing.T) {
	database := newTestStore(t)
	ctx := t.Context()
	now := time.Date(2026, 8, 25, 5, 0, 0, 0, time.UTC)
	owner := createTicketTestUser(t, database, "rollback-referrer@example.test", now)
	digest := bytes.Repeat([]byte{0x51}, 32)
	setInvitationPolicy(t, database, owner.ID, true, 5, false, now)
	if _, err := database.CreateInvitationCode(ctx, owner.ID, CreateInvitationCodeInput{
		CodeDigest: digest, CodeCipher: bytes.Repeat([]byte{0x61}, 40),
	}, now); err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.ExecContext(ctx, `
		CREATE TRIGGER fail_invitation_session BEFORE INSERT ON admin_sessions
		WHEN NEW.user_id <> 1 BEGIN SELECT RAISE(ABORT, 'forced session failure'); END;
	`); err != nil {
		t.Fatal(err)
	}
	_, err := database.RegisterUserWithSession(ctx, RegisterUserInput{
		Email: "rollback-invite@example.test", PasswordHash: "hash", InvitationCodeDigest: digest,
	}, RegistrationSessionInput{
		TokenHash: strings.Repeat("a", 64), CSRFHash: strings.Repeat("b", 64), ExpiresAt: now.Add(time.Hour),
	}, now)
	if err == nil {
		t.Fatal("registration unexpectedly succeeded through the failure trigger")
	}
	if err := database.CheckInvitationCode(ctx, digest); err != nil {
		t.Fatalf("failed registration consumed the invitation: %v", err)
	}
	if _, err := database.db.ExecContext(ctx, `DROP TRIGGER fail_invitation_session`); err != nil {
		t.Fatal(err)
	}

	if err := database.IncrementInvitationCodeView(ctx, digest, now); err != nil {
		t.Fatal(err)
	}
	if err := database.IncrementInvitationCodeView(ctx, bytes.Repeat([]byte{0xee}, 32), now); err != nil {
		t.Fatalf("unknown invitation view leaked existence: %v", err)
	}
	summary, err := database.GetInvitationSummary(ctx, owner.ID)
	if err != nil || len(summary.Codes) != 1 || summary.Codes[0].PV != 1 {
		t.Fatalf("view summary=%#v err=%v", summary, err)
	}
}

func setInvitationPolicy(t testing.TB, database *Store, administratorID int64, force bool, limit int, neverExpire bool, now time.Time) {
	t.Helper()
	settings, err := database.GetSiteSettings(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	_, err = database.UpdateSiteSettings(t.Context(), administratorID, settings.Revision, SaveSiteSettingsInput{
		AppName: settings.AppName, AppDescription: settings.AppDescription, AppURL: settings.AppURL,
		TOSURL: settings.TOSURL, Logo: settings.Logo, StopRegister: settings.StopRegister,
		EmailVerificationEnabled: settings.EmailVerificationEnabled,
		EmailWhitelistEnabled:    settings.EmailWhitelistEnabled, EmailWhitelistSuffixes: settings.EmailWhitelistSuffixes,
		GmailAliasLimitEnabled:     settings.GmailAliasLimitEnabled,
		RegistrationIPLimitEnabled: settings.RegistrationIPLimitEnabled,
		RegistrationIPLimitCount:   settings.RegistrationIPLimitCount, RegistrationIPLimitMinutes: settings.RegistrationIPLimitMinutes,
		PasswordLimitEnabled: settings.PasswordLimitEnabled, PasswordLimitCount: settings.PasswordLimitCount,
		PasswordLimitMinutes:   settings.PasswordLimitMinutes,
		InvitationForceEnabled: force, InvitationCodeLimit: limit, InvitationNeverExpire: neverExpire,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
}
