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
	state.Requirements.BaselineCommit = strings.Repeat("f", 40)
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
	runGit(t, temporaryRoot, "init")
	runGit(t, temporaryRoot, "config", "user.name", "governance-test")
	runGit(t, temporaryRoot, "config", "user.email", "governance-test@example.invalid")
	runGit(t, temporaryRoot, "add", ".")
	runGit(t, temporaryRoot, "commit", "-m", "fixture")
	if err := Check(temporaryRoot); err == nil {
		t.Fatal("expected a syntactically valid but nonexistent evidence target commit to fail validation")
	}
}

func runGit(t *testing.T, root string, arguments ...string) {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", root}, arguments...)...)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(arguments, " "), err, output)
	}
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
