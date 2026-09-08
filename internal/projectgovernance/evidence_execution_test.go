package projectgovernance

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPassedEvidenceRejectsUnexecutedCaseKinds(t *testing.T) {
	for _, testCase := range []struct {
		name, kind, caseID, command string
	}{
		{"vitest is not browser", "browser", "e2e/nodes.spec.ts", "corepack pnpm --dir web test"},
		{"Go tests are not differential", "differential", "parity/admin-surface.spec.ts:140", "go test -race ./..."},
		{"browser listing is not execution", "browser", "e2e/nodes.spec.ts", "pnpm exec playwright test --list"},
		{"different browser file", "browser", "e2e/nodes.spec.ts", "pnpm exec playwright test e2e/plans.spec.ts"},
		{"echo is not execution", "browser", "e2e/nodes.spec.ts", "echo playwright test"},
		{"unit tests do not run benchmarks", "performance", "BenchmarkListAdminOrders100K", "go test -race -cover ./..."},
		{"large attachment is opt in", "performance", "TestOneGiBAttachmentCompletesWithChunkBoundedMemory", "go test ./..."},
		{"capacity is opt in", "performance", "TestCAPR014WebSocketConnectionDrain/256", "go test ./..."},
		{"external node smoke is not Go unit test", "contract", "REAL-NODE-CLIENT-SMOKE", "go test ./..."},
		{"real client is not Go unit test", "contract", "client/sing-box-1.14.0", "go test ./..."},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			_, state := repositoryState(t)
			evidence := validTestEvidence(state)
			evidence.Kind, evidence.CaseIDs, evidence.Command = testCase.kind, []string{testCase.caseID}, testCase.command
			state.Requirements.Requirements[0].Evidence = []Evidence{evidence}
			if err := Validate(state); err == nil || !strings.Contains(err.Error(), "execution") {
				t.Fatalf("expected case execution mismatch, got %v", err)
			}
		})
	}
}

func TestEvidenceTargetRejectsDeletedProductFile(t *testing.T) {
	_, state := repositoryState(t)
	root := t.TempDir()
	runGit(t, root, "init")
	runGit(t, root, "config", "user.name", "governance-test")
	runGit(t, root, "config", "user.email", "governance-test@example.invalid")
	path := filepath.Join(root, "runtime.go")
	if err := os.WriteFile(path, []byte("package runtime\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, root, "add", "runtime.go")
	runGit(t, root, "commit", "-m", "verified product")
	state.Requirements.BaselineCommit = strings.TrimSpace(runGitOutput(t, root, "rev-parse", "HEAD"))
	state.Requirements.Requirements[0].VerificationStatus = "current"
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	runGit(t, root, "add", "runtime.go")
	runGit(t, root, "commit", "-m", "remove product behavior")
	if err := validateEvidenceTarget(root, state); err == nil || !strings.Contains(err.Error(), "runtime.go") {
		t.Fatalf("expected deletion to invalidate product evidence, got %v", err)
	}
}

func TestPassedEvidenceAcceptsMatchingExecutionCommands(t *testing.T) {
	for _, testCase := range []struct {
		kind, caseID, command string
	}{
		{"browser", "e2e/nodes.spec.ts", "corepack pnpm --dir web test:e2e"},
		{"browser", "e2e/nodes.spec.ts", "pnpm --dir web exec playwright test e2e/nodes.spec.ts --project chromium"},
		{"browser", "e2e/nodes.spec.ts", "pnpm exec playwright test --project mobile-chromium"},
		{"differential", "parity/admin-surface.spec.ts:140", "pnpm --dir web test:parity"},
		{"differential", "parity/admin-surface.spec.ts:140", "pnpm exec playwright test --config=playwright.parity.config.ts"},
		{"performance", "BenchmarkListAdminOrders100K", "go test ./internal/store -bench=BenchmarkListAdminOrders100K -benchmem"},
		{"performance", "BenchmarkListAdminOrders100K", "go test -bench . ./internal/store"},
		{"performance", "TestOneGiBAttachmentCompletesWithChunkBoundedMemory", "XBOARD_RUN_LARGE_ATTACHMENT_TEST=1 go test ./internal/attachments -run TestOneGiBAttachmentCompletesWithChunkBoundedMemory -v"},
		{"performance", "TestCAPR014WebSocketConnectionDrain/256", "XBOARD_WS_CAPACITY_CONNECTIONS=256 XBOARD_TEST_REDIS_URL=redis://127.0.0.1:6379 go test ./internal/httpapi -run TestCAPR014WebSocketConnectionDrain -v"},
		{"contract", "client/sing-box-1.14.0", ".github/scripts/check-real-client-compat.sh artifacts/client-compat"},
	} {
		t.Run(testCase.command, func(t *testing.T) {
			_, state := repositoryState(t)
			evidence := validTestEvidence(state)
			evidence.Kind, evidence.CaseIDs, evidence.Command = testCase.kind, []string{testCase.caseID}, testCase.command
			state.Requirements.Requirements[0].Evidence = []Evidence{evidence}
			if err := Validate(state); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestNotRunEvidenceRequiresReasonAndCannotSupportCurrentVerification(t *testing.T) {
	_, state := repositoryState(t)
	requirement := &state.Requirements.Requirements[0]
	requirement.VerificationStatus, requirement.AcceptanceStatus = "partial", "pending"
	evidence := validTestEvidence(state)
	evidence.Kind, evidence.CaseIDs, evidence.Result = "browser", []string{"e2e/nodes.spec.ts"}, "not_run"
	requirement.Evidence = []Evidence{evidence}
	if err := Validate(state); err == nil || !strings.Contains(err.Error(), "not_run_reason") {
		t.Fatalf("expected unexplained not_run to fail, got %v", err)
	}
	requirement.Evidence[0].NotRunReason = "The referenced Go command did not run the browser case."
	if err := Validate(state); err != nil {
		t.Fatalf("explicitly unverified partial evidence must remain auditable: %v", err)
	}
	requirement.VerificationStatus = "current"
	if err := Validate(state); err == nil || !strings.Contains(err.Error(), "current evidence must pass") {
		t.Fatalf("not_run must not support current verification, got %v", err)
	}
	// A reason cannot remain after someone changes the result back to pass.
	requirement.VerificationStatus = "partial"
	requirement.Evidence[0].Result = "pass"
	if err := Validate(state); err == nil || !strings.Contains(err.Error(), "not_run_reason") {
		t.Fatalf("expected contradictory pass and not_run reason to fail, got %v", err)
	}
}
