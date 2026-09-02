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
	state.Requirements.Requirements[0].AcceptanceStatus = "accepted"
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

func TestPartialRequirementNeedsStatusReason(t *testing.T) {
	_, state := repositoryState(t)
	requirement := &state.Requirements.Requirements[0]
	requirement.VerificationStatus = "partial"
	requirement.StatusReason = ""
	if err := Validate(state); err == nil {
		t.Fatal("expected partial requirement without a status reason to fail validation")
	}
}

func TestProgressEvidenceMustTargetCandidateCommit(t *testing.T) {
	_, state := repositoryState(t)
	requirement := &state.Requirements.Requirements[0]
	requirement.VerificationStatus = "partial"
	requirement.StatusReason = "Local verification is not durable acceptance evidence."
	requirement.ProgressEvidence = []ProgressEvidence{{
		ID: "PE-TEST-LOCAL", Kind: "integration", Environment: "local",
		CaseIDs: []string{"TestProgressRequirement"}, Commit: strings.Repeat("f", 40),
		ObservedAt: "2026-09-02T00:00:00Z", Command: "go test ./internal/httpapi", Result: "pass",
	}}
	if err := Validate(state); err == nil {
		t.Fatal("expected progress evidence from another commit to fail validation")
	}
	requirement.ProgressEvidence[0].Commit = state.Requirements.BaselineCommit
	if err := Validate(state); err != nil {
		t.Fatal(err)
	}
}

func TestCurrentEvidenceMustCoverAcceptanceCriteria(t *testing.T) {
	_, state := repositoryState(t)
	requirement := &state.Requirements.Requirements[0]
	requirement.VerificationStatus = "current"
	requirement.AcceptanceCriteria = []AcceptanceCriterion{{ID: "AUTH-001-AC1", Title: "A stable acceptance criterion"}}
	requirement.Evidence = []Evidence{validTestEvidence(state)}
	if err := Validate(state); err == nil {
		t.Fatal("expected current evidence without acceptance-criterion coverage to fail validation")
	}
	requirement.Evidence[0].CaseIDs = append(requirement.Evidence[0].CaseIDs, "AUTH-001-AC1")
	if err := Validate(state); err != nil {
		t.Fatal(err)
	}
}

func TestWorkTrackNeedsKnownDependencies(t *testing.T) {
	_, state := repositoryState(t)
	state.WorkItems.Tracks = []WorkTrack{{
		ID: "FIN-A", Title: "Ledger", Status: "in_progress", Milestone: "M1",
		WorkItemIDs: []string{"FUNC-001"}, DependsOn: []string{"FIN-Z"}, CompletionGate: "Tests pass",
	}}
	if err := Validate(state); err == nil {
		t.Fatal("expected unknown work-track dependency to fail validation")
	}
}

func TestAcceptedRequirementRequiresCompletedWorkItems(t *testing.T) {
	_, state := repositoryState(t)
	requirement := &state.Requirements.Requirements[0]
	requirement.VerificationStatus = "current"
	requirement.MigrationStatus = "not_applicable"
	requirement.AcceptanceStatus = "accepted"
	requirement.Evidence = []Evidence{validTestEvidence(state)}
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
	state.Requirements.BaselineCommit = strings.Repeat("f", 40)
	retargetProgressEvidence(&state)
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
	state.Requirements.BaselineCommit = strings.TrimSpace(runGitOutput(t, temporaryRoot, "rev-parse", "HEAD"))
	retargetProgressEvidence(&state)
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
	state.Requirements.BaselineCommit = strings.TrimSpace(runGitOutput(t, temporaryRoot, "rev-parse", "HEAD"))
	retargetProgressEvidence(&state)
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

func retargetProgressEvidence(state *State) {
	for requirementIndex := range state.Requirements.Requirements {
		for evidenceIndex := range state.Requirements.Requirements[requirementIndex].ProgressEvidence {
			state.Requirements.Requirements[requirementIndex].ProgressEvidence[evidenceIndex].Commit = state.Requirements.BaselineCommit
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
