package bulkops

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Hao-Monster/Xboard-Go/internal/mailer"
	appsettings "github.com/Hao-Monster/Xboard-Go/internal/settings"
	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

func TestRenderTemplateKeepsLegacyVariablesDefaultsAndUnknownTokens(t *testing.T) {
	variables := map[string]string{
		"app.name": "U5 Board", "user.email": "target@example.test", "user.transfer_left": "1024",
	}
	input := `{{app.name}} {{ user.email }} {{missing|默认值}} {{unknown}} {{user.transfer_left|0}}`
	want := `U5 Board target@example.test 默认值 {{unknown}} 1024`
	if got := RenderTemplate(input, variables); got != want {
		t.Fatalf("RenderTemplate() = %q, want %q", got, want)
	}
	if got := RenderTemplate("{{bad\nkey|fallback}}", variables); got != "{{bad\nkey|fallback}}" {
		t.Fatalf("unsafe variable changed: %q", got)
	}
}

func TestServiceDeliversBulkMailFromSnapshot(t *testing.T) {
	database, administrator, target, cipherBox, now := newBulkServiceFixture(t)
	job, err := database.CreateAdminUserBulkJob(context.Background(), store.CreateAdminUserBulkJobInput{
		Kind: store.AdminUserBulkKindMail, AdministratorID: administrator.ID,
		Scope:   store.AdminUserBulkScope{Scope: store.AdminUserBulkScopeSelected, UserIDs: []int64{target.ID}},
		Subject: "通知 {{user.email}}", Content: "{{app.name}} / {{user.id}} / {{user.plan_name|无订阅}} / {{unknown}}",
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	sender := &captureSender{}
	service, err := New(database, Options{
		Cipher: cipherBox, Sender: sender, ExportRoot: t.TempDir(), PanelURL: "https://fallback.example.test",
	})
	if err != nil {
		t.Fatal(err)
	}
	worked, err := service.RunMailOnce(context.Background(), now)
	if err != nil || !worked {
		t.Fatalf("RunMailOnce() = %v, %v", worked, err)
	}
	messages := sender.Messages()
	if len(messages) != 1 || messages[0].To != target.Email || messages[0].Subject != "通知 "+target.Email ||
		messages[0].Text != "U5 Board / "+integerString(target.ID)+" / 无订阅 / {{unknown}}" {
		t.Fatalf("messages = %#v", messages)
	}
	finished, err := database.GetAdminUserBulkJob(context.Background(), job.ID)
	if err != nil || finished.Status != store.AdminUserBulkStatusSucceeded || finished.SuccessCount != 1 {
		t.Fatalf("finished mail job = %#v, %v", finished, err)
	}
}

func TestServiceRecoversExpiredMailLeaseAndStopsAfterThreeAttempts(t *testing.T) {
	database, administrator, target, cipherBox, now := newBulkServiceFixture(t)
	ctx := context.Background()
	recoveredJob, err := database.CreateAdminUserBulkJob(ctx, store.CreateAdminUserBulkJobInput{
		Kind: store.AdminUserBulkKindMail, AdministratorID: administrator.ID,
		Scope:   store.AdminUserBulkScope{Scope: store.AdminUserBulkScopeSelected, UserIDs: []int64{target.ID}},
		Subject: "恢复测试", Content: "lease recovery",
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, claimed, err := database.ClaimAdminUserBulkMail(ctx, "abandoned-mail-claim", now, mailClaimLease); err != nil || !claimed {
		t.Fatalf("abandoned claim = %v, %v", claimed, err)
	}
	sender := &captureSender{}
	service, err := New(database, Options{Cipher: cipherBox, Sender: sender, ExportRoot: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if worked, err := service.RunMailOnce(ctx, now.Add(mailClaimLease-time.Second)); err != nil || worked {
		t.Fatalf("unexpired lease RunMailOnce() = %v, %v", worked, err)
	}
	if worked, err := service.RunMailOnce(ctx, now.Add(mailClaimLease+time.Second)); err != nil || !worked {
		t.Fatalf("expired lease RunMailOnce() = %v, %v", worked, err)
	}
	recovered, err := database.GetAdminUserBulkJob(ctx, recoveredJob.ID)
	if err != nil || recovered.Status != store.AdminUserBulkStatusSucceeded || recovered.SuccessCount != 1 {
		t.Fatalf("recovered job = %#v, %v", recovered, err)
	}
	targets, err := database.ListAdminUserBulkTargets(ctx, recoveredJob.ID, 0, 10)
	if err != nil || len(targets) != 1 || targets[0].AttemptCount != 2 {
		t.Fatalf("recovered targets = %#v, %v", targets, err)
	}

	failingJob, err := database.CreateAdminUserBulkJob(ctx, store.CreateAdminUserBulkJobInput{
		Kind: store.AdminUserBulkKindMail, AdministratorID: administrator.ID,
		Scope:   store.AdminUserBulkScope{Scope: store.AdminUserBulkScopeSelected, UserIDs: []int64{target.ID}},
		Subject: "重试测试", Content: "retry limit",
	}, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	failingSender := &captureSender{failure: errors.New("temporary SMTP failure containing secret-token"), failuresRemaining: 3}
	failingService, err := New(database, Options{Cipher: cipherBox, Sender: failingSender, ExportRoot: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	for index, attemptAt := range []time.Time{now.Add(time.Minute), now.Add(time.Minute + 10*time.Second), now.Add(time.Minute + 40*time.Second)} {
		if worked, err := failingService.RunMailOnce(ctx, attemptAt); !worked || err == nil {
			t.Fatalf("failure attempt %d RunMailOnce() = %v, %v", index+1, worked, err)
		}
	}
	failed, err := database.GetAdminUserBulkJob(ctx, failingJob.ID)
	if err != nil || failed.Status != store.AdminUserBulkStatusFailed || failed.FailureCount != 1 || failed.ProcessedCount != 1 {
		t.Fatalf("failed job = %#v, %v", failed, err)
	}
	failedTargets, err := database.ListAdminUserBulkTargets(ctx, failingJob.ID, 0, 10)
	if err != nil || len(failedTargets) != 1 {
		t.Fatalf("failed targets = %#v, %v", failedTargets, err)
	}
	if failedTargets[0].LastError != "SMTP delivery failed" || strings.Contains(failed.LastError, "secret-token") || strings.Contains(failedTargets[0].LastError, "secret-token") {
		t.Fatalf("SMTP failure was not redacted: job=%q target=%q", failed.LastError, failedTargets[0].LastError)
	}
	if failed.LastError != "SMTP delivery failed" {
		t.Fatalf("failed job error = %q", failed.LastError)
	}
	if worked, err := failingService.RunMailOnce(ctx, now.Add(2*time.Minute)); err != nil || worked {
		t.Fatalf("exhausted mail remained claimable: worked=%v err=%v", worked, err)
	}
}

func TestServiceFailsAnExpiredThirdMailAttemptWithoutSendingAgain(t *testing.T) {
	database, administrator, target, cipherBox, now := newBulkServiceFixture(t)
	ctx := context.Background()
	job, err := database.CreateAdminUserBulkJob(ctx, store.CreateAdminUserBulkJobInput{
		Kind: store.AdminUserBulkKindMail, AdministratorID: administrator.ID,
		Scope:   store.AdminUserBulkScope{Scope: store.AdminUserBulkScopeSelected, UserIDs: []int64{target.ID}},
		Subject: "第三次租约恢复", Content: "do not deliver a fourth time",
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	for attempt := 1; attempt <= 3; attempt++ {
		claimToken := fmt.Sprintf("abandoned-attempt-%d", attempt)
		claimed, ok, err := database.ClaimAdminUserBulkMail(ctx, claimToken, now.Add(time.Duration(attempt-1)*time.Minute), mailClaimLease)
		if err != nil || !ok || claimed.Attempt != attempt {
			t.Fatalf("claim attempt %d = %#v, %v, %v", attempt, claimed, ok, err)
		}
		if attempt < 3 {
			attemptAt := now.Add(time.Duration(attempt-1) * time.Minute)
			if err := database.FailAdminUserBulkMail(ctx, job.ID, claimed.Sequence, claimToken, "temporary failure", attemptAt, attemptAt); err != nil {
				t.Fatal(err)
			}
		}
	}
	sender := &captureSender{}
	service, err := New(database, Options{Cipher: cipherBox, Sender: sender, ExportRoot: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if worked, err := service.RunMailOnce(ctx, now.Add(2*time.Minute+mailClaimLease)); err != nil || worked {
		t.Fatalf("expired third attempt RunMailOnce() = %v, %v", worked, err)
	}
	finished, err := database.GetAdminUserBulkJob(ctx, job.ID)
	if err != nil || finished.Status != store.AdminUserBulkStatusFailed || finished.FailureCount != 1 ||
		finished.LastError != "mail delivery result unknown after worker restart" {
		t.Fatalf("finished job = %#v, %v", finished, err)
	}
	if len(sender.Messages()) != 0 {
		t.Fatalf("expired third attempt was delivered again: %#v", sender.Messages())
	}
}

func TestServiceRunsFourBoundedMailWorkersWithoutStarvingJobs(t *testing.T) {
	database, administrator, firstTarget, cipherBox, fixtureNow := newBulkServiceFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	userIDs := []int64{firstTarget.ID}
	for index := 1; index < 8; index++ {
		user, err := database.CreateAdminUser(ctx, store.CreateAdminUserInput{
			Email: fmt.Sprintf("bulk-worker-%d@example.test", index), PasswordHash: "hash", TransferEnable: 1 << 30,
		}, fixtureNow)
		if err != nil {
			t.Fatal(err)
		}
		userIDs = append(userIDs, user.ID)
	}
	job, err := database.CreateAdminUserBulkJob(ctx, store.CreateAdminUserBulkJobInput{
		Kind: store.AdminUserBulkKindMail, AdministratorID: administrator.ID,
		Scope:   store.AdminUserBulkScope{Scope: store.AdminUserBulkScopeSelected, UserIDs: userIDs},
		Subject: "并发边界", Content: "bounded workers",
	}, time.Now().UTC().Add(-time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	sender := newBlockingSender(8)
	service, err := New(database, Options{
		Cipher: cipherBox, Sender: sender, ExportRoot: t.TempDir(), PollInterval: 5 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { service.Run(ctx); close(done) }()
	for index := 0; index < mailWorkerCount; index++ {
		select {
		case <-sender.started:
		case <-time.After(3 * time.Second):
			t.Fatalf("only %d mail workers started", index)
		}
	}
	select {
	case <-sender.started:
		t.Fatal("more than four mail sends ran concurrently")
	case <-time.After(75 * time.Millisecond):
	}
	if sender.Maximum() != mailWorkerCount {
		t.Fatalf("maximum concurrent sends = %d, want %d", sender.Maximum(), mailWorkerCount)
	}
	close(sender.release)
	deadline := time.Now().Add(5 * time.Second)
	for {
		finished, err := database.GetAdminUserBulkJob(ctx, job.ID)
		if err != nil {
			t.Fatal(err)
		}
		if finished.Status == store.AdminUserBulkStatusSucceeded {
			if finished.SuccessCount != len(userIDs) {
				t.Fatalf("finished job = %#v", finished)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("parallel mail job did not finish: %#v", finished)
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("bulk service did not stop after cancellation")
	}
}

func TestServiceStreamsSafeLegacyCompatibleCSV(t *testing.T) {
	database, administrator, target, cipherBox, now := newBulkServiceFixture(t)
	subscribeURL := "https://subscriptions.example.test/base"
	if _, err := database.UpdateLegacySiteSettings(context.Background(), administrator.ID, store.SaveLegacySiteSettingsInput{
		SubscribeURL: &subscribeURL,
	}, now); err != nil {
		t.Fatal(err)
	}
	job, err := database.CreateAdminUserBulkJob(context.Background(), store.CreateAdminUserBulkJobInput{
		Kind: store.AdminUserBulkKindCSV, AdministratorID: administrator.ID,
		Scope: store.AdminUserBulkScope{Scope: store.AdminUserBulkScopeSelected, UserIDs: []int64{target.ID}},
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	exportRoot := t.TempDir()
	service, err := New(database, Options{
		Cipher: cipherBox, Sender: &captureSender{}, ExportRoot: exportRoot, PanelURL: "https://fallback.example.test/base",
	})
	if err != nil {
		t.Fatal(err)
	}
	finished, err := service.ProcessCSVJob(context.Background(), job.ID, now)
	if err != nil {
		t.Fatalf("ProcessCSVJob() error = %v", err)
	}
	if finished.Status != store.AdminUserBulkStatusSucceeded || finished.OutputRelativePath == "" || finished.OutputSHA256 == "" || finished.OutputSize == nil {
		t.Fatalf("finished CSV job = %#v", finished)
	}
	contents, err := os.ReadFile(filepath.Join(exportRoot, filepath.FromSlash(finished.OutputRelativePath)))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(contents, []byte{0xef, 0xbb, 0xbf}) || !bytes.Contains(contents, []byte("邮箱,余额,推广佣金,总流量,剩余流量,套餐到期时间,订阅计划,订阅地址\r\n")) ||
		!bytes.Contains(contents, []byte(target.Email+",0.00,0.00,20 GB,20 GB,长期有效,无订阅,https://subscriptions.example.test/base/s/")) {
		t.Fatalf("CSV contents:\n%s", contents)
	}
	info, err := os.Stat(filepath.Join(exportRoot, filepath.FromSlash(finished.OutputRelativePath)))
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		t.Fatalf("CSV permissions = %o, want no group/other access", info.Mode().Perm())
	}
	cleaned, err := service.CleanupExpired(context.Background(), now.Add(25*time.Hour), 100)
	if err != nil || cleaned != 1 {
		t.Fatalf("CleanupExpired() = %d, %v", cleaned, err)
	}
	if _, err := os.Stat(filepath.Join(exportRoot, filepath.FromSlash(finished.OutputRelativePath))); !os.IsNotExist(err) {
		t.Fatalf("expired CSV still exists: %v", err)
	}
	if _, _, err := service.OpenCSV(context.Background(), job.ID, now.Add(25*time.Hour)); !errors.Is(err, store.ErrAdminUserBulkExpired) {
		t.Fatalf("OpenCSV(expired) error = %v", err)
	}
	for _, value := range []string{"=cmd", "+SUM(1,1)", "-1+2", "@evil", "\tformula", "\nformula"} {
		if got := safeSpreadsheetCell(value); !strings.HasPrefix(got, "'") {
			t.Fatalf("safeSpreadsheetCell(%q) = %q", value, got)
		}
	}
}

func TestServiceWaitsForACSVClaimedByTheBackgroundWorker(t *testing.T) {
	database, administrator, target, cipherBox, now := newBulkServiceFixture(t)
	job, err := database.CreateAdminUserBulkJob(context.Background(), store.CreateAdminUserBulkJobInput{
		Kind: store.AdminUserBulkKindCSV, AdministratorID: administrator.ID,
		Scope: store.AdminUserBulkScope{Scope: store.AdminUserBulkScopeSelected, UserIDs: []int64{target.ID}},
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	const backgroundClaim = "background-csv-claim"
	claimedJob, claimed, err := database.ClaimAdminUserBulkCSV(context.Background(), job.ID, backgroundClaim, now, csvClaimLease)
	if err != nil || !claimed {
		t.Fatalf("background claim = %#v, %v, %v", claimedJob, claimed, err)
	}
	service, err := New(database, Options{Cipher: cipherBox, Sender: &captureSender{}, ExportRoot: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	waitContext, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if _, err := service.ProcessCSVJob(waitContext, job.ID, now); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("ProcessCSVJob() error = %v, want context deadline", err)
	}
	if _, err := service.processClaimedCSVWithToken(context.Background(), claimedJob, backgroundClaim, now); err != nil {
		t.Fatal(err)
	}
	finished, err := service.ProcessCSVJob(context.Background(), job.ID, now)
	if err != nil || finished.Status != store.AdminUserBulkStatusSucceeded {
		t.Fatalf("finished CSV = %#v, %v", finished, err)
	}
}

func TestServiceRejectsASymbolicLinkExportRoot(t *testing.T) {
	parent := t.TempDir()
	target := t.TempDir()
	link := filepath.Join(parent, "exports")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symbolic links are unavailable: %v", err)
	}
	database, _, _, cipherBox, _ := newBulkServiceFixture(t)
	if _, err := New(database, Options{Cipher: cipherBox, Sender: &captureSender{}, ExportRoot: link}); err == nil || !strings.Contains(err.Error(), "symbolic link") {
		t.Fatalf("New() error = %v", err)
	}
}

func TestBoundedWriterRejectsOverflowBeforeWriting(t *testing.T) {
	var output bytes.Buffer
	writer := &boundedWriter{writer: &output, maximum: 5}
	if written, err := writer.Write([]byte("12345")); err != nil || written != 5 {
		t.Fatalf("bounded write = %d, %v", written, err)
	}
	if written, err := writer.Write([]byte("6")); err == nil || written != 0 {
		t.Fatalf("overflow write = %d, %v", written, err)
	}
	if output.String() != "12345" || writer.written != 5 {
		t.Fatalf("overflow changed output=%q written=%d", output.String(), writer.written)
	}
}

func BenchmarkCSVTenThousandTargets(b *testing.B) {
	database, administrator, cipherBox, now := newBulkCSVBenchmarkFixture(b, 9_999)
	jobs := make([]store.AdminUserBulkJob, b.N)
	for index := range jobs {
		job, err := database.CreateAdminUserBulkJob(context.Background(), store.CreateAdminUserBulkJobInput{
			Kind: store.AdminUserBulkKindCSV, AdministratorID: administrator.ID,
			Scope: store.AdminUserBulkScope{Scope: store.AdminUserBulkScopeAll},
		}, now.Add(time.Duration(index+1)*time.Second))
		if err != nil || job.TotalCount != 10_000 {
			b.Fatalf("create benchmark job = %#v, %v", job, err)
		}
		jobs[index] = job
	}
	service, err := New(database, Options{Cipher: cipherBox, ExportRoot: b.TempDir(), PanelURL: "https://panel.example.test"})
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for _, job := range jobs {
		finished, err := service.ProcessCSVJob(context.Background(), job.ID, now.Add(time.Minute))
		if err != nil || finished.OutputSize == nil || *finished.OutputSize <= 0 {
			b.Fatalf("generate benchmark CSV = %#v, %v", finished, err)
		}
	}
}

func newBulkCSVBenchmarkFixture(tb testing.TB, additionalUsers int) (*store.Store, store.AdminUser, *appsettings.Cipher, time.Time) {
	tb.Helper()
	path := filepath.Join(tb.TempDir(), "bulk-performance.db")
	database, err := store.OpenSQLite("file:" + filepath.ToSlash(path))
	if err != nil {
		tb.Fatal(err)
	}
	tb.Cleanup(func() { _ = database.Close() })
	ctx := context.Background()
	if err := database.Migrate(ctx); err != nil {
		tb.Fatal(err)
	}
	now := time.Date(2026, 8, 28, 13, 0, 0, 0, time.UTC)
	administrator, err := database.CreateAdminUser(ctx, store.CreateAdminUserInput{
		Email: "bulk-performance-admin@example.test", PasswordHash: "hash", IsAdmin: true,
	}, now)
	if err != nil {
		tb.Fatal(err)
	}
	cipherBox, err := appsettings.NewCipher(bytes.Repeat([]byte{0x52}, 32))
	if err != nil {
		tb.Fatal(err)
	}
	settings, err := database.GetTicketSettings(ctx)
	if err != nil {
		tb.Fatal(err)
	}
	if _, err := database.UpdateTicketSettings(ctx, administrator.ID, settings.Revision, store.SaveTicketSettingsInput{
		AppName: "Performance Board", AppURL: "https://panel.example.test", SMTPEnabled: true,
		SMTPHost: "smtp.example.test", SMTPPort: 587, SMTPEncryption: mailer.EncryptionStartTLS,
		SMTPFromAddress: "no-reply@example.test",
	}, now); err != nil {
		tb.Fatal(err)
	}
	raw, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path))
	if err != nil {
		tb.Fatal(err)
	}
	defer raw.Close()
	tx, err := raw.BeginTx(ctx, nil)
	if err != nil {
		tb.Fatal(err)
	}
	defer tx.Rollback()
	statement, err := tx.PrepareContext(ctx, `
		INSERT INTO users (email,password_hash,is_admin,banned,account_kind,subscription_token,transfer_enable,created_at,updated_at)
		VALUES (?, 'hash', 0, 0, 'human', ?, 107374182400, ?, ?)
	`)
	if err != nil {
		tb.Fatal(err)
	}
	defer statement.Close()
	for index := 0; index < additionalUsers; index++ {
		if _, err := statement.ExecContext(ctx, fmt.Sprintf("csv-%05d@example.test", index), fmt.Sprintf("%032x", index+1), now.Unix(), now.Unix()); err != nil {
			tb.Fatalf("seed CSV user %d: %v", index, err)
		}
	}
	if err := tx.Commit(); err != nil {
		tb.Fatal(err)
	}
	return database, administrator, cipherBox, now
}

func newBulkServiceFixture(t *testing.T) (*store.Store, store.AdminUser, store.AdminUser, *appsettings.Cipher, time.Time) {
	t.Helper()
	database, err := store.OpenSQLite("file:" + filepath.ToSlash(filepath.Join(t.TempDir(), "bulk.db")))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	ctx := context.Background()
	if err := database.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)
	administrator, err := database.CreateAdminUser(ctx, store.CreateAdminUserInput{
		Email: "bulk-admin@example.test", PasswordHash: "hash", IsAdmin: true,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	target, err := database.CreateAdminUser(ctx, store.CreateAdminUserInput{
		Email: "bulk-target@example.test", PasswordHash: "hash", TransferEnable: 20 << 30,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	cipherBox, err := appsettings.NewCipher(bytes.Repeat([]byte{0x51}, 32))
	if err != nil {
		t.Fatal(err)
	}
	settings, err := database.GetTicketSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.UpdateTicketSettings(ctx, administrator.ID, settings.Revision, store.SaveTicketSettingsInput{
		AppName: "U5 Board", AppURL: "https://panel.example.test", SMTPEnabled: true,
		SMTPHost: "smtp.example.test", SMTPPort: 587, SMTPEncryption: mailer.EncryptionStartTLS,
		SMTPFromAddress: "no-reply@example.test",
	}, now); err != nil {
		t.Fatal(err)
	}
	return database, administrator, target, cipherBox, now
}

type captureSender struct {
	mu                sync.Mutex
	messages          []mailer.Message
	failure           error
	failuresRemaining int
}

func (sender *captureSender) Send(_ context.Context, _ mailer.SMTPConfig, message mailer.Message) error {
	sender.mu.Lock()
	defer sender.mu.Unlock()
	sender.messages = append(sender.messages, message)
	if sender.failuresRemaining > 0 {
		sender.failuresRemaining--
		return sender.failure
	}
	return nil
}

func (sender *captureSender) Messages() []mailer.Message {
	sender.mu.Lock()
	defer sender.mu.Unlock()
	return append([]mailer.Message(nil), sender.messages...)
}

type blockingSender struct {
	mu      sync.Mutex
	current int
	maximum int
	started chan struct{}
	release chan struct{}
}

func newBlockingSender(capacity int) *blockingSender {
	return &blockingSender{started: make(chan struct{}, capacity), release: make(chan struct{})}
}

func (sender *blockingSender) Send(ctx context.Context, _ mailer.SMTPConfig, _ mailer.Message) error {
	sender.mu.Lock()
	sender.current++
	if sender.current > sender.maximum {
		sender.maximum = sender.current
	}
	sender.mu.Unlock()
	sender.started <- struct{}{}
	select {
	case <-ctx.Done():
		sender.mu.Lock()
		sender.current--
		sender.mu.Unlock()
		return ctx.Err()
	case <-sender.release:
		sender.mu.Lock()
		sender.current--
		sender.mu.Unlock()
		return nil
	}
}

func (sender *blockingSender) Maximum() int {
	sender.mu.Lock()
	defer sender.mu.Unlock()
	return sender.maximum
}

func integerString(value int64) string {
	return strconv.FormatInt(value, 10)
}
