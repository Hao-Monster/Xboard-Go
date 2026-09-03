package projectgovernance

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

func repositoryState(t *testing.T) (string, State) {
	t.Helper()
	root, err := FindRoot(".")
	if err != nil {
		t.Fatal(err)
	}
	state, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	return root, state
}

func TestRepositoryGovernanceIsValid(t *testing.T) {
	root, state := repositoryState(t)
	if err := Validate(state); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(root); err != nil {
		t.Fatal(err)
	}
}

func TestAcceptedRequirementNeedsCurrentEvidence(t *testing.T) {
	_, state := repositoryState(t)
	requirement := &state.Requirements.Requirements[0]
	requirement.VerificationStatus = "historical"
	requirement.AcceptanceStatus = "accepted"
	requirement.Evidence = nil
	if err := Validate(state); err == nil {
		t.Fatal("expected accepted historical requirement to fail validation")
	}
}

func TestBlockedRequirementNeedsPendingDecision(t *testing.T) {
	_, state := repositoryState(t)
	state.Requirements.Requirements[0].ScopeStatus = "blocked"
	state.Requirements.Requirements[0].DecisionIDs = []string{"D-001"}
	if err := Validate(state); err == nil {
		t.Fatal("expected a resolved decision to be insufficient for a blocked requirement")
	}
}

func TestCompleteMilestoneRequiresEveryGateToPass(t *testing.T) {
	_, state := repositoryState(t)
	state.ReleaseGates.Milestones[0].Status = "complete"
	state.ReleaseGates.Milestones[0].Gates[0].Status = "not_run"
	if err := Validate(state); err == nil {
		t.Fatal("expected a complete milestone with an unverified gate to fail validation")
	}
}

func TestRequirementRegistryRejectsReplacingAnAuditedID(t *testing.T) {
	_, state := repositoryState(t)
	state.Requirements.Requirements[0].ID = "AUTH-999"
	if err := Validate(state); err == nil {
		t.Fatal("expected replacing an audited requirement ID to fail validation")
	}
}

func TestReleaseGateMustBelongToItsMilestone(t *testing.T) {
	_, state := repositoryState(t)
	state.ReleaseGates.Milestones[0].Gates[0].ID = "M1-G99"
	if err := Validate(state); err == nil {
		t.Fatal("expected a release gate under the wrong milestone to fail validation")
	}
}

func TestCurrentEvidenceMustTargetCandidateCommit(t *testing.T) {
	_, state := repositoryState(t)
	requirement := &state.Requirements.Requirements[0]
	requirement.VerificationStatus = "current"
	evidence := validTestEvidence(state)
	evidence.Commit = strings.Repeat("f", 40)
	requirement.Evidence = []Evidence{evidence}
	if err := Validate(state); err == nil {
		t.Fatal("expected current evidence from a different candidate commit to fail validation")
	}
}

func TestCurrentEvidenceRequiresRFC3339ObservationTime(t *testing.T) {
	_, state := repositoryState(t)
	requirement := &state.Requirements.Requirements[0]
	requirement.VerificationStatus = "current"
	evidence := validTestEvidence(state)
	evidence.ObservedAt = "sometime"
	requirement.Evidence = []Evidence{evidence}
	if err := Validate(state); err == nil {
		t.Fatal("expected current evidence without an RFC3339 observation time to fail validation")
	}
}

func TestCurrentEvidenceMustHavePassed(t *testing.T) {
	_, state := repositoryState(t)
	requirement := &state.Requirements.Requirements[0]
	requirement.VerificationStatus = "current"
	evidence := validTestEvidence(state)
	evidence.Result = "fail"
	requirement.Evidence = []Evidence{evidence}
	if err := Validate(state); err == nil {
		t.Fatal("expected failing evidence to be insufficient for current verification")
	}
}

func TestCurrentEvidenceRequiresAuditableCaseMetadata(t *testing.T) {
	_, state := repositoryState(t)
	requirement := &state.Requirements.Requirements[0]
	requirement.VerificationStatus = "current"
	requirement.Evidence = []Evidence{{
		Commit: state.Requirements.BaselineCommit, ObservedAt: "2026-09-02T00:00:00Z",
		Command: "go test ./internal/httpapi", Result: "pass",
	}}
	if err := Validate(state); err == nil {
		t.Fatal("expected current evidence without kind, environment, cases, and an artifact to fail validation")
	}
}

func TestCurrentEvidenceWithAuditableMetadataIsValid(t *testing.T) {
	_, state := repositoryState(t)
	requirement := &state.Requirements.Requirements[0]
	requirement.VerificationStatus = "current"
	requirement.Evidence = []Evidence{validTestEvidence(state)}
	if err := Validate(state); err != nil {
		t.Fatal(err)
	}
}

func TestAcceptedRequirementRequiresCompletedWorkItems(t *testing.T) {
	_, state := repositoryState(t)
	requirement := &state.Requirements.Requirements[0]
	requirement.VerificationStatus = "current"
	requirement.MigrationStatus = "not_applicable"
	requirement.AcceptanceStatus = "accepted"
	requirement.Evidence = []Evidence{validTestEvidence(state)}
	for index := range state.WorkItems.WorkItems {
		if state.WorkItems.WorkItems[index].ID == requirement.WorkItemIDs[0] {
			state.WorkItems.WorkItems[index].Status = "open"
		}
	}
	if err := Validate(state); err == nil {
		t.Fatal("expected an accepted requirement with an open work item to fail validation")
	}
}

func TestAcceptedRequirementWithCompletedWorkItemsIsValid(t *testing.T) {
	_, state := repositoryState(t)
	requirement := &state.Requirements.Requirements[0]
	requirement.VerificationStatus = "current"
	requirement.MigrationStatus = "not_applicable"
	requirement.AcceptanceStatus = "accepted"
	requirement.Evidence = []Evidence{validTestEvidence(state)}
	for index := range state.WorkItems.WorkItems {
		if state.WorkItems.WorkItems[index].ID == requirement.WorkItemIDs[0] {
			state.WorkItems.WorkItems[index].Status = "done"
		}
	}
	if err := Validate(state); err != nil {
		t.Fatal(err)
	}
}

func TestRenderStatusInterpretationUsesCurrentCounts(t *testing.T) {
	_, state := repositoryState(t)
	for index := range state.Requirements.Requirements {
		state.Requirements.Requirements[index].VerificationStatus = "historical"
		state.Requirements.Requirements[index].AcceptanceStatus = "pending"
	}
	state.Requirements.Requirements[0].VerificationStatus = "current"
	state.Requirements.Requirements[0].AcceptanceStatus = "accepted"

	status, err := RenderStatus(state)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(status, "Current-head verification is 1/80 and formal acceptance is 1/80.") {
		t.Fatalf("status interpretation did not use current counts:\n%s", status)
	}
}

func validTestEvidence(state State) Evidence {
	return Evidence{
		ID: "EV-TEST-CURRENT", Kind: "integration", Environment: "github-actions",
		CaseIDs:  []string{"TestCurrentRequirement"},
		Artifact: "https://github.com/Hao-Monster/Xboard-Go/actions/runs/1",
		Commit:   state.Requirements.BaselineCommit, ObservedAt: "2026-09-02T00:00:00Z",
		Command: "go test ./internal/httpapi", Result: "pass",
	}
}

func TestCheckRejectsUnknownEvidenceTargetCommit(t *testing.T) {
	root, state := repositoryState(t)
	temporaryRoot := copyProjectFixture(t, root)
	retargetCurrentEvidence(&state, strings.Repeat("f", 40))
	writeRequirementFixture(t, temporaryRoot, state)
	runGit(t, temporaryRoot, "init")
	runGit(t, temporaryRoot, "config", "user.name", "governance-test")
	runGit(t, temporaryRoot, "config", "user.email", "governance-test@example.invalid")
	runGit(t, temporaryRoot, "add", ".")
	runGit(t, temporaryRoot, "commit", "-m", "fixture")
	if err := Check(temporaryRoot); err == nil {
		t.Fatal("expected a syntactically valid but nonexistent evidence target commit to fail validation")
	}
}

func TestCheckRejectsProductDriftAfterCurrentEvidenceTarget(t *testing.T) {
	root, state := repositoryState(t)
	temporaryRoot := copyProjectFixture(t, root)
	runGit(t, temporaryRoot, "init")
	runGit(t, temporaryRoot, "config", "user.name", "governance-test")
	runGit(t, temporaryRoot, "config", "user.email", "governance-test@example.invalid")
	runGit(t, temporaryRoot, "add", ".")
	runGit(t, temporaryRoot, "commit", "-m", "verification target")
	retargetCurrentEvidence(&state, strings.TrimSpace(runGitOutput(t, temporaryRoot, "rev-parse", "HEAD")))
	requirement := &state.Requirements.Requirements[0]
	requirement.VerificationStatus = "current"
	requirement.Evidence = []Evidence{validTestEvidence(state)}
	writeRequirementFixture(t, temporaryRoot, state)
	if err := os.WriteFile(filepath.Join(temporaryRoot, "runtime.go"), []byte("package runtime\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, temporaryRoot, "add", ".")
	runGit(t, temporaryRoot, "commit", "-m", "metadata and unverified product drift")
	if err := Check(temporaryRoot); err == nil {
		t.Fatal("expected product changes after the current evidence target to fail validation")
	}
}

func TestCheckAllowsMetadataOnlyCommitAfterCurrentEvidenceTarget(t *testing.T) {
	root, state := repositoryState(t)
	temporaryRoot := copyProjectFixture(t, root)
	runGit(t, temporaryRoot, "init")
	runGit(t, temporaryRoot, "config", "user.name", "governance-test")
	runGit(t, temporaryRoot, "config", "user.email", "governance-test@example.invalid")
	runGit(t, temporaryRoot, "add", ".")
	runGit(t, temporaryRoot, "commit", "-m", "verification target")
	retargetCurrentEvidence(&state, strings.TrimSpace(runGitOutput(t, temporaryRoot, "rev-parse", "HEAD")))
	requirement := &state.Requirements.Requirements[0]
	requirement.VerificationStatus = "current"
	requirement.Evidence = []Evidence{validTestEvidence(state)}
	writeRequirementFixture(t, temporaryRoot, state)
	runGit(t, temporaryRoot, "add", ".")
	runGit(t, temporaryRoot, "commit", "-m", "evidence metadata")
	if err := Check(temporaryRoot); err != nil {
		t.Fatal(err)
	}
}

func TestCheckAllowsSyntheticTestdataChangesAfterCurrentEvidenceTarget(t *testing.T) {
	root, state := repositoryState(t)
	temporaryRoot := copyProjectFixture(t, root)
	runGit(t, temporaryRoot, "init")
	runGit(t, temporaryRoot, "config", "user.name", "governance-test")
	runGit(t, temporaryRoot, "config", "user.email", "governance-test@example.invalid")
	runGit(t, temporaryRoot, "add", ".")
	runGit(t, temporaryRoot, "commit", "-m", "verification target")
	retargetCurrentEvidence(&state, strings.TrimSpace(runGitOutput(t, temporaryRoot, "rev-parse", "HEAD")))
	requirement := &state.Requirements.Requirements[0]
	requirement.VerificationStatus = "current"
	requirement.Evidence = []Evidence{validTestEvidence(state)}
	writeRequirementFixture(t, temporaryRoot, state)
	testdataPath := filepath.Join(temporaryRoot, "internal", "testdata", "legacy", "gen", "fixture.go")
	if err := os.MkdirAll(filepath.Dir(testdataPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(testdataPath, []byte("package gen\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, temporaryRoot, "add", ".")
	runGit(t, temporaryRoot, "commit", "-m", "synthetic testdata tooling")
	if err := Check(temporaryRoot); err != nil {
		t.Fatalf("synthetic testdata changes must not invalidate product evidence: %v", err)
	}
}

func TestEvidenceMetadataPathsExcludePackagedApplicationCode(t *testing.T) {
	for _, path := range []string{
		"docs/project/requirements.json",
		".github/workflows/ci.yml",
		".github/scripts/check-production-licenses.mjs",
		"cmd/projectctl/main.go",
		"internal/projectgovernance/governance.go",
		"cmd/testdatagen/main.go",
		"internal/testdata/legacy/gen/generator.go",
	} {
		if !isEvidenceMetadataPath(path) {
			t.Errorf("expected %s to be governance metadata", path)
		}
	}
	for _, path := range []string{"cmd/xboard/main.go", "internal/store/sqlite.go", "web/src/App.tsx", "web/scripts/check-entry-budget.mjs"} {
		if isEvidenceMetadataPath(path) {
			t.Errorf("expected %s to invalidate product evidence", path)
		}
	}
}

// retargetCurrentEvidence keeps these tests independent from whichever
// requirements happen to be current in the repository fixture. A baseline
// change is valid only when every current evidence record follows it.
func retargetCurrentEvidence(state *State, commit string) {
	state.Requirements.BaselineCommit = commit
	for requirementIndex := range state.Requirements.Requirements {
		requirement := &state.Requirements.Requirements[requirementIndex]
		if requirement.VerificationStatus != "current" {
			continue
		}
		for evidenceIndex := range requirement.Evidence {
			requirement.Evidence[evidenceIndex].Commit = commit
		}
	}
}

func copyProjectFixture(t *testing.T, root string) string {
	t.Helper()
	temporaryRoot := t.TempDir()
	projectDir := filepath.Join(temporaryRoot, "docs", "project")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{
		"requirements.json", "decisions.json", "risks.json", "compatibility-exceptions.json",
		"work-items.json", "release-gates.json",
	} {
		data, err := os.ReadFile(filepath.Join(root, "docs", "project", name))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(projectDir, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	workflowDirectory := filepath.Join(temporaryRoot, ".github", "workflows")
	if err := os.MkdirAll(workflowDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	workflows, err := os.ReadDir(filepath.Join(root, ".github", "workflows"))
	if err != nil {
		t.Fatal(err)
	}
	for _, workflow := range workflows {
		if workflow.IsDir() {
			continue
		}
		data, err := os.ReadFile(filepath.Join(root, ".github", "workflows", workflow.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(workflowDirectory, workflow.Name()), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return temporaryRoot
}

func writeRequirementFixture(t *testing.T, root string, state State) {
	t.Helper()
	projectDir := filepath.Join(root, "docs", "project")
	requirementsJSON, err := json.MarshalIndent(state.Requirements, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projectDir, "requirements.json"), append(requirementsJSON, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	status, err := RenderStatus(state)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projectDir, "STATUS.md"), []byte(status), 0o644); err != nil {
		t.Fatal(err)
	}
}

func runGit(t *testing.T, root string, arguments ...string) {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", root}, arguments...)...)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(arguments, " "), err, output)
	}
}

func runGitOutput(t *testing.T, root string, arguments ...string) string {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", root}, arguments...)...)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(arguments, " "), err, output)
	}
	return string(output)
}

func TestPRMetadata(t *testing.T) {
	_, state := repositoryState(t)
	body := `Requirement IDs: N/A: governance-only change
Work item IDs: GOV-001
Milestone: M0
Closes: #123`
	if err := ValidatePRMetadata(body, "M0 Project Governance Baseline", state); err != nil {
		t.Fatal(err)
	}
}

func TestPRMetadataRejectsUnknownID(t *testing.T) {
	_, state := repositoryState(t)
	body := `Requirement IDs: NOPE-999
Work item IDs: N/A: no work item
Milestone: M0
Closes: #123`
	if err := ValidatePRMetadata(body, "M0 Project Governance Baseline", state); err == nil {
		t.Fatal("expected unknown requirement to fail validation")
	}
}

func TestPREventUsesCurrentMilestoneOverride(t *testing.T) {
	root, _ := repositoryState(t)
	event := `{
  "pull_request": {
    "body": "Requirement IDs: N/A: governance-only change\nWork item IDs: GOV-001\nMilestone: M0\nCloses: #113",
    "milestone": null,
    "user": {"login": "maintainer"}
  },
  "sender": {"login": "maintainer"}
}`
	path := filepath.Join(t.TempDir(), "event.json")
	if err := os.WriteFile(path, []byte(event), 0o600); err != nil {
		t.Fatal(err)
	}
	milestone := "M0 Project Governance Baseline"
	if err := CheckPREvent(root, path, &milestone); err != nil {
		t.Fatal(err)
	}
}

func TestPREventRejectsExplicitlyRemovedMilestone(t *testing.T) {
	root, _ := repositoryState(t)
	event := `{
  "pull_request": {
    "body": "Requirement IDs: N/A: governance-only change\nWork item IDs: GOV-001\nMilestone: M0\nCloses: #113",
    "milestone": {"title": "M0 Project Governance Baseline"},
    "user": {"login": "maintainer"}
  },
  "sender": {"login": "maintainer"}
}`
	path := filepath.Join(t.TempDir(), "event.json")
	if err := os.WriteFile(path, []byte(event), 0o600); err != nil {
		t.Fatal(err)
	}
	removedMilestone := ""
	if err := CheckPREvent(root, path, &removedMilestone); err == nil || !strings.Contains(err.Error(), "assigned to a GitHub milestone") {
		t.Fatalf("expected removed current milestone to be rejected, got %v", err)
	}
}

func TestPRMetadataRejectsUnfilledTemplate(t *testing.T) {
	root, state := repositoryState(t)
	body, err := os.ReadFile(filepath.Join(root, ".github", "PULL_REQUEST_TEMPLATE.md"))
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidatePRMetadata(string(body), "M0 Project Governance Baseline", state); err == nil {
		t.Fatal("expected unfilled pull request template to fail validation")
	}
}

func TestWorkflowActionPinsRequireImmutableCommits(t *testing.T) {
	root := t.TempDir()
	workflowDirectory := filepath.Join(root, ".github", "workflows")
	if err := os.MkdirAll(workflowDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	workflow := `name: pins
on: push
jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v7
      - uses: docker://alpine:3.23
      - uses: ./local-action
`
	if err := os.WriteFile(filepath.Join(workflowDirectory, "pins.yml"), []byte(workflow), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := validateWorkflowActionPins(root); err == nil || !strings.Contains(err.Error(), "actions/checkout@v7") || !strings.Contains(err.Error(), "docker://alpine:3.23") {
		t.Fatalf("expected mutable action reference to fail, got %v", err)
	}
}

func TestWorkflowActionPinsAcceptCommitAndLocalAction(t *testing.T) {
	root := t.TempDir()
	workflowDirectory := filepath.Join(root, ".github", "workflows")
	if err := os.MkdirAll(workflowDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	workflow := `name: pins
on: push
jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1
      - uses: docker://alpine@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef
      - uses: ./local-action
`
	if err := os.WriteFile(filepath.Join(workflowDirectory, "pins.yaml"), []byte(workflow), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := validateWorkflowActionPins(root); err != nil {
		t.Fatal(err)
	}
}

func TestGovernanceYAMLParses(t *testing.T) {
	root, _ := repositoryState(t)
	paths := []string{
		filepath.Join(".github", "workflows", "ci.yml"),
		filepath.Join(".github", "ISSUE_TEMPLATE", "work-item.yml"),
		filepath.Join(".github", "ISSUE_TEMPLATE", "decision.yml"),
		filepath.Join(".github", "ISSUE_TEMPLATE", "risk.yml"),
		filepath.Join(".github", "ISSUE_TEMPLATE", "config.yml"),
	}
	for _, path := range paths {
		data, err := os.ReadFile(filepath.Join(root, path))
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		var document any
		if err := yaml.Unmarshal(data, &document); err != nil {
			t.Errorf("parse %s: %v", path, err)
		}
	}
}
