package projectgovernance

import (
	"os"
	"path/filepath"
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
