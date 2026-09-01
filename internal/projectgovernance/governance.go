package projectgovernance

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

const statusPath = "docs/project/STATUS.md"

type State struct {
	Requirements RequirementRegistry
	Decisions    DecisionRegistry
	Risks        RiskRegistry
	Exceptions   ExceptionRegistry
	WorkItems    WorkItemRegistry
	ReleaseGates ReleaseGateRegistry
}

type RequirementRegistry struct {
	SchemaVersion  int           `json:"schema_version"`
	Baseline       string        `json:"baseline"`
	AsOf           string        `json:"as_of"`
	BaselineCommit string        `json:"baseline_commit"`
	Source         string        `json:"source"`
	Requirements   []Requirement `json:"requirements"`
}

type Requirement struct {
	ID                   string     `json:"id"`
	Level                string     `json:"level"`
	Domain               string     `json:"domain"`
	Title                string     `json:"title"`
	ScopeStatus          string     `json:"scope_status"`
	ImplementationStatus string     `json:"implementation_status"`
	VerificationStatus   string     `json:"verification_status"`
	MigrationStatus      string     `json:"migration_status"`
	AcceptanceStatus     string     `json:"acceptance_status"`
	Milestone            string     `json:"milestone"`
	DecisionIDs          []string   `json:"decision_ids"`
	WorkItemIDs          []string   `json:"work_item_ids"`
	Evidence             []Evidence `json:"evidence"`
}

type Evidence struct {
	ID          string   `json:"id"`
	Kind        string   `json:"kind"`
	Environment string   `json:"environment"`
	CaseIDs     []string `json:"case_ids"`
	Artifact    string   `json:"artifact"`
	Commit      string   `json:"commit"`
	ObservedAt  string   `json:"observed_at"`
	Command     string   `json:"command"`
	Result      string   `json:"result"`
}

type DecisionRegistry struct {
	SchemaVersion int        `json:"schema_version"`
	AsOf          string     `json:"as_of"`
	Decisions     []Decision `json:"decisions"`
}

type Decision struct {
	ID          string   `json:"id"`
	Title       string   `json:"title"`
	Status      string   `json:"status"`
	Milestone   string   `json:"milestone"`
	Resolution  string   `json:"resolution"`
	Recommended string   `json:"recommended"`
	Blocks      []string `json:"blocks"`
}

type RiskRegistry struct {
	SchemaVersion int    `json:"schema_version"`
	AsOf          string `json:"as_of"`
	Risks         []Risk `json:"risks"`
}

type Risk struct {
	ID         string `json:"id"`
	Severity   string `json:"severity"`
	Status     string `json:"status"`
	Milestone  string `json:"milestone"`
	Title      string `json:"title"`
	Mitigation string `json:"mitigation"`
}

type ExceptionRegistry struct {
	SchemaVersion int                      `json:"schema_version"`
	Exceptions    []CompatibilityException `json:"exceptions"`
}

type CompatibilityException struct {
	ID             string   `json:"id"`
	Status         string   `json:"status"`
	DecisionID     string   `json:"decision_id"`
	RequirementIDs []string `json:"requirement_ids"`
	Title          string   `json:"title"`
	Rationale      string   `json:"rationale"`
}

type WorkItemRegistry struct {
	SchemaVersion int        `json:"schema_version"`
	AsOf          string     `json:"as_of"`
	WorkItems     []WorkItem `json:"work_items"`
}

type WorkItem struct {
	ID             string   `json:"id"`
	Title          string   `json:"title"`
	Status         string   `json:"status"`
	Milestone      string   `json:"milestone"`
	IssueNumber    int      `json:"issue_number"`
	RequirementIDs []string `json:"requirement_ids"`
	DecisionIDs    []string `json:"decision_ids"`
}

type ReleaseGateRegistry struct {
	SchemaVersion int         `json:"schema_version"`
	AsOf          string      `json:"as_of"`
	Milestones    []Milestone `json:"milestones"`
}

type Milestone struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Status string `json:"status"`
	Gates  []Gate `json:"gates"`
}

type Gate struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Status string `json:"status"`
}

func FindRoot(start string) (string, error) {
	current, err := filepath.Abs(start)
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(current, "go.mod")); err == nil {
			if _, err := os.Stat(filepath.Join(current, "docs", "project")); err == nil {
				return current, nil
			}
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", fmt.Errorf("repository root not found from %s", start)
		}
		current = parent
	}
}

func Load(root string) (State, error) {
	var state State
	files := []struct {
		name string
		dest any
	}{
		{"requirements.json", &state.Requirements},
		{"decisions.json", &state.Decisions},
		{"risks.json", &state.Risks},
		{"compatibility-exceptions.json", &state.Exceptions},
		{"work-items.json", &state.WorkItems},
		{"release-gates.json", &state.ReleaseGates},
	}
	for _, file := range files {
		path := filepath.Join(root, "docs", "project", file.name)
		data, err := os.ReadFile(path)
		if err != nil {
			return State{}, fmt.Errorf("read %s: %w", file.name, err)
		}
		decoder := json.NewDecoder(bytes.NewReader(data))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(file.dest); err != nil {
			return State{}, fmt.Errorf("decode %s: %w", file.name, err)
		}
		var trailing any
		if err := decoder.Decode(&trailing); err != io.EOF {
			if err == nil {
				return State{}, fmt.Errorf("decode %s: multiple JSON values", file.name)
			}
			return State{}, fmt.Errorf("decode %s trailing data: %w", file.name, err)
		}
	}
	return state, nil
}

func Check(root string) error {
	state, err := Load(root)
	if err != nil {
		return err
	}
	if err := Validate(state); err != nil {
		return err
	}
	if err := validateEvidenceTarget(root, state.Requirements.BaselineCommit); err != nil {
		return err
	}
	want, err := RenderStatus(state)
	if err != nil {
		return err
	}
	got, err := os.ReadFile(filepath.Join(root, statusPath))
	if err != nil {
		return fmt.Errorf("read generated status: %w; run `go run ./cmd/projectctl generate`", err)
	}
	if normalizeNewlines(string(got)) != normalizeNewlines(want) {
		return errors.New("docs/project/STATUS.md is stale; run `go run ./cmd/projectctl generate`")
	}
	return nil
}

func Generate(root string) error {
	state, err := Load(root)
	if err != nil {
		return err
	}
	if err := Validate(state); err != nil {
		return err
	}
	if err := validateEvidenceTarget(root, state.Requirements.BaselineCommit); err != nil {
		return err
	}
	content, err := RenderStatus(state)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(root, statusPath), []byte(content), 0o644)
}

func Validate(state State) error {
	var problems []string
	for name, version := range map[string]int{
		"requirements":  state.Requirements.SchemaVersion,
		"decisions":     state.Decisions.SchemaVersion,
		"risks":         state.Risks.SchemaVersion,
		"exceptions":    state.Exceptions.SchemaVersion,
		"work_items":    state.WorkItems.SchemaVersion,
		"release_gates": state.ReleaseGates.SchemaVersion,
	} {
		if version != 1 {
			problems = append(problems, fmt.Sprintf("%s schema_version must be 1", name))
		}
	}

	milestones := map[string]bool{"M0": true, "M1": true, "M2": true, "M3": true, "M4": true}
	requirementIDs := make(map[string]bool)
	decisionIDs := make(map[string]bool)
	decisionStatuses := make(map[string]string)
	workItemIDs := make(map[string]bool)
	workItemStatuses := make(map[string]string)

	if len(state.Requirements.Requirements) != 80 {
		problems = append(problems, fmt.Sprintf("requirements must contain exactly 80 entries, got %d", len(state.Requirements.Requirements)))
	}
	if !regexp.MustCompile(`^[0-9a-f]{40}$`).MatchString(state.Requirements.BaselineCommit) {
		problems = append(problems, "requirements baseline_commit must be a full 40-character lowercase commit SHA")
	}

	for _, decision := range state.Decisions.Decisions {
		if !regexp.MustCompile(`^D-\d{3}$`).MatchString(decision.ID) {
			problems = append(problems, fmt.Sprintf("invalid decision id %q", decision.ID))
		}
		if decisionIDs[decision.ID] {
			problems = append(problems, fmt.Sprintf("duplicate decision id %s", decision.ID))
		}
		decisionIDs[decision.ID] = true
		decisionStatuses[decision.ID] = decision.Status
		if strings.TrimSpace(decision.Title) == "" {
			problems = append(problems, fmt.Sprintf("%s requires a title", decision.ID))
		}
		if !oneOf(decision.Status, "resolved", "pending") {
			problems = append(problems, fmt.Sprintf("%s has invalid status %q", decision.ID, decision.Status))
		}
		if !milestones[decision.Milestone] {
			problems = append(problems, fmt.Sprintf("%s has invalid milestone %q", decision.ID, decision.Milestone))
		}
		if decision.Status == "resolved" && strings.TrimSpace(decision.Resolution) == "" {
			problems = append(problems, fmt.Sprintf("%s is resolved without a resolution", decision.ID))
		}
		if decision.Status == "pending" && strings.TrimSpace(decision.Recommended) == "" {
			problems = append(problems, fmt.Sprintf("%s is pending without a recommendation", decision.ID))
		}
	}
	if len(state.Decisions.Decisions) != 18 {
		problems = append(problems, fmt.Sprintf("decisions must contain the audited D-001..D-018 set, got %d", len(state.Decisions.Decisions)))
	}
	for index := 1; index <= 18; index++ {
		id := fmt.Sprintf("D-%03d", index)
		if !decisionIDs[id] {
			problems = append(problems, fmt.Sprintf("missing audited decision %s", id))
		}
	}

	for _, workItem := range state.WorkItems.WorkItems {
		if !regexp.MustCompile(`^[A-Z]+-\d{3}$`).MatchString(workItem.ID) {
			problems = append(problems, fmt.Sprintf("invalid work item id %q", workItem.ID))
		}
		if workItemIDs[workItem.ID] {
			problems = append(problems, fmt.Sprintf("duplicate work item id %s", workItem.ID))
		}
		workItemIDs[workItem.ID] = true
		workItemStatuses[workItem.ID] = workItem.Status
		if strings.TrimSpace(workItem.Title) == "" {
			problems = append(problems, fmt.Sprintf("%s requires a title", workItem.ID))
		}
		if !oneOf(workItem.Status, "open", "in_progress", "blocked", "done") {
			problems = append(problems, fmt.Sprintf("%s has invalid status %q", workItem.ID, workItem.Status))
		}
		if !milestones[workItem.Milestone] {
			problems = append(problems, fmt.Sprintf("%s has invalid milestone %q", workItem.ID, workItem.Milestone))
		}
		if workItem.IssueNumber <= 0 {
			problems = append(problems, fmt.Sprintf("%s must reference a GitHub issue", workItem.ID))
		}
	}

	requirementPattern := regexp.MustCompile(`^[A-Z]+-\d{3}$`)
	evidenceIDPattern := regexp.MustCompile(`^EV-[A-Z0-9][A-Z0-9-]{2,63}$`)
	evidenceCasePattern := regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/-]{1,159}$`)
	githubArtifactPattern := regexp.MustCompile(`^https://github\.com/Hao-Monster/Xboard-Go/actions/runs/\d+(?:/job/\d+)?$`)
	bingoArtifactPattern := regexp.MustCompile(`^bingo-dev:sha256:[0-9a-f]{64}$`)
	expectedRequirements := expectedRequirementIDs()
	for _, requirement := range state.Requirements.Requirements {
		if !requirementPattern.MatchString(requirement.ID) {
			problems = append(problems, fmt.Sprintf("invalid requirement id %q", requirement.ID))
		}
		if !expectedRequirements[requirement.ID] {
			problems = append(problems, fmt.Sprintf("unexpected requirement id %s", requirement.ID))
		}
		if requirementIDs[requirement.ID] {
			problems = append(problems, fmt.Sprintf("duplicate requirement id %s", requirement.ID))
		}
		requirementIDs[requirement.ID] = true
		if strings.TrimSpace(requirement.Title) == "" || strings.TrimSpace(requirement.Domain) == "" {
			problems = append(problems, fmt.Sprintf("%s requires a title and domain", requirement.ID))
		}
		if !oneOf(requirement.Level, "V", "C", "D") {
			problems = append(problems, fmt.Sprintf("%s has invalid level %q", requirement.ID, requirement.Level))
		}
		if !oneOf(requirement.ScopeStatus, "decided", "blocked") {
			problems = append(problems, fmt.Sprintf("%s has invalid scope_status %q", requirement.ID, requirement.ScopeStatus))
		}
		if !oneOf(requirement.ImplementationStatus, "implemented", "partial", "blocked", "not_started") {
			problems = append(problems, fmt.Sprintf("%s has invalid implementation_status %q", requirement.ID, requirement.ImplementationStatus))
		}
		if !oneOf(requirement.VerificationStatus, "current", "historical", "partial", "none") {
			problems = append(problems, fmt.Sprintf("%s has invalid verification_status %q", requirement.ID, requirement.VerificationStatus))
		}
		if !oneOf(requirement.MigrationStatus, "current", "historical", "partial", "not_assessed", "not_applicable") {
			problems = append(problems, fmt.Sprintf("%s has invalid migration_status %q", requirement.ID, requirement.MigrationStatus))
		}
		if !oneOf(requirement.AcceptanceStatus, "accepted", "pending", "rejected") {
			problems = append(problems, fmt.Sprintf("%s has invalid acceptance_status %q", requirement.ID, requirement.AcceptanceStatus))
		}
		if !milestones[requirement.Milestone] {
			problems = append(problems, fmt.Sprintf("%s has invalid milestone %q", requirement.ID, requirement.Milestone))
		}
		if len(requirement.WorkItemIDs) == 0 {
			problems = append(problems, fmt.Sprintf("%s must reference at least one work item", requirement.ID))
		}
		if requirement.ScopeStatus == "blocked" && len(requirement.DecisionIDs) == 0 {
			problems = append(problems, fmt.Sprintf("%s is blocked without a decision reference", requirement.ID))
		}
		if requirement.ScopeStatus == "blocked" {
			pending := false
			for _, id := range requirement.DecisionIDs {
				pending = pending || decisionStatuses[id] == "pending"
			}
			if !pending {
				problems = append(problems, fmt.Sprintf("%s is blocked without a pending decision", requirement.ID))
			}
		}
		if requirement.VerificationStatus == "current" && len(requirement.Evidence) == 0 {
			problems = append(problems, fmt.Sprintf("%s is current without evidence", requirement.ID))
		}
		for _, evidence := range requirement.Evidence {
			_, observedAtErr := time.Parse(time.RFC3339, evidence.ObservedAt)
			validArtifact := (evidence.Environment == "github-actions" && githubArtifactPattern.MatchString(evidence.Artifact)) ||
				(evidence.Environment == "bingo-dev" && bingoArtifactPattern.MatchString(evidence.Artifact))
			validCases := len(evidence.CaseIDs) > 0
			seenCases := make(map[string]bool, len(evidence.CaseIDs))
			for _, caseID := range evidence.CaseIDs {
				if !evidenceCasePattern.MatchString(caseID) || seenCases[caseID] {
					validCases = false
				}
				seenCases[caseID] = true
			}
			if !evidenceIDPattern.MatchString(evidence.ID) || !oneOf(evidence.Kind, "unit", "integration", "contract", "browser", "differential", "migration", "security", "performance", "manual") ||
				!oneOf(evidence.Environment, "github-actions", "bingo-dev") || !validCases || !validArtifact ||
				!regexp.MustCompile(`^[0-9a-f]{40}$`).MatchString(evidence.Commit) || observedAtErr != nil || strings.TrimSpace(evidence.Command) == "" || !oneOf(evidence.Result, "pass", "fail") {
				problems = append(problems, fmt.Sprintf("%s has incomplete or invalid evidence", requirement.ID))
			}
			if requirement.VerificationStatus == "current" && evidence.Commit != state.Requirements.BaselineCommit {
				problems = append(problems, fmt.Sprintf("%s current evidence must target baseline_commit %s", requirement.ID, state.Requirements.BaselineCommit))
			}
			if requirement.VerificationStatus == "current" && evidence.Result != "pass" {
				problems = append(problems, fmt.Sprintf("%s current evidence must pass", requirement.ID))
			}
		}
		if requirement.AcceptanceStatus == "accepted" {
			if requirement.ScopeStatus != "decided" || requirement.ImplementationStatus != "implemented" || requirement.VerificationStatus != "current" || !oneOf(requirement.MigrationStatus, "current", "not_applicable") || len(requirement.Evidence) == 0 {
				problems = append(problems, fmt.Sprintf("%s is accepted without satisfying the acceptance invariant", requirement.ID))
			}
			for _, id := range requirement.WorkItemIDs {
				if workItemStatuses[id] != "done" {
					problems = append(problems, fmt.Sprintf("%s is accepted while work item %s is not done", requirement.ID, id))
				}
			}
		}
	}
	for id := range expectedRequirements {
		if !requirementIDs[id] {
			problems = append(problems, fmt.Sprintf("missing audited requirement %s", id))
		}
	}

	for _, requirement := range state.Requirements.Requirements {
		for _, id := range requirement.DecisionIDs {
			if !decisionIDs[id] {
				problems = append(problems, fmt.Sprintf("%s references unknown decision %s", requirement.ID, id))
			}
		}
		for _, id := range requirement.WorkItemIDs {
			if !workItemIDs[id] {
				problems = append(problems, fmt.Sprintf("%s references unknown work item %s", requirement.ID, id))
			}
		}
	}
	for _, decision := range state.Decisions.Decisions {
		for _, id := range decision.Blocks {
			if !requirementIDs[id] {
				problems = append(problems, fmt.Sprintf("%s blocks unknown requirement %s", decision.ID, id))
			}
		}
	}
	for _, workItem := range state.WorkItems.WorkItems {
		for _, id := range workItem.RequirementIDs {
			if !requirementIDs[id] {
				problems = append(problems, fmt.Sprintf("%s references unknown requirement %s", workItem.ID, id))
			}
		}
		for _, id := range workItem.DecisionIDs {
			if !decisionIDs[id] {
				problems = append(problems, fmt.Sprintf("%s references unknown decision %s", workItem.ID, id))
			}
		}
	}

	riskIDs := make(map[string]bool)
	for _, risk := range state.Risks.Risks {
		if !regexp.MustCompile(`^R-\d{3}$`).MatchString(risk.ID) || riskIDs[risk.ID] {
			problems = append(problems, fmt.Sprintf("invalid or duplicate risk id %q", risk.ID))
		}
		riskIDs[risk.ID] = true
		if strings.TrimSpace(risk.Title) == "" {
			problems = append(problems, fmt.Sprintf("%s requires a title", risk.ID))
		}
		if !oneOf(risk.Severity, "critical", "high", "medium", "low") || !oneOf(risk.Status, "open", "controlled", "accepted", "closed") || !milestones[risk.Milestone] {
			problems = append(problems, fmt.Sprintf("%s has invalid severity, status, or milestone", risk.ID))
		}
		if strings.TrimSpace(risk.Mitigation) == "" {
			problems = append(problems, fmt.Sprintf("%s requires mitigation", risk.ID))
		}
	}

	exceptionIDs := make(map[string]bool)
	for _, exception := range state.Exceptions.Exceptions {
		if !regexp.MustCompile(`^CE-\d{3}$`).MatchString(exception.ID) || exceptionIDs[exception.ID] {
			problems = append(problems, fmt.Sprintf("invalid or duplicate compatibility exception id %q", exception.ID))
		}
		exceptionIDs[exception.ID] = true
		if strings.TrimSpace(exception.Title) == "" || strings.TrimSpace(exception.Rationale) == "" {
			problems = append(problems, fmt.Sprintf("%s requires a title and rationale", exception.ID))
		}
		if !oneOf(exception.Status, "accepted", "proposed", "retired") || !decisionIDs[exception.DecisionID] {
			problems = append(problems, fmt.Sprintf("%s has invalid status or decision reference", exception.ID))
		}
		for _, id := range exception.RequirementIDs {
			if !requirementIDs[id] {
				problems = append(problems, fmt.Sprintf("%s references unknown requirement %s", exception.ID, id))
			}
		}
	}

	seenMilestones := make(map[string]bool)
	gateIDs := make(map[string]bool)
	for _, milestone := range state.ReleaseGates.Milestones {
		if !milestones[milestone.ID] || seenMilestones[milestone.ID] {
			problems = append(problems, fmt.Sprintf("invalid or duplicate release milestone %q", milestone.ID))
		}
		seenMilestones[milestone.ID] = true
		if strings.TrimSpace(milestone.Name) == "" {
			problems = append(problems, fmt.Sprintf("%s requires a release milestone name", milestone.ID))
		}
		if !oneOf(milestone.Status, "in_progress", "blocked", "complete") {
			problems = append(problems, fmt.Sprintf("%s has invalid release status %q", milestone.ID, milestone.Status))
		}
		if len(milestone.Gates) == 0 {
			problems = append(problems, fmt.Sprintf("%s must contain release gates", milestone.ID))
		}
		allGatesPass := len(milestone.Gates) > 0
		for _, gate := range milestone.Gates {
			if gateIDs[gate.ID] || !regexp.MustCompile(`^M[0-4]-G\d{2}$`).MatchString(gate.ID) {
				problems = append(problems, fmt.Sprintf("invalid or duplicate gate id %q", gate.ID))
			}
			if !strings.HasPrefix(gate.ID, milestone.ID+"-G") {
				problems = append(problems, fmt.Sprintf("%s does not belong to milestone %s", gate.ID, milestone.ID))
			}
			gateIDs[gate.ID] = true
			if strings.TrimSpace(gate.Title) == "" {
				problems = append(problems, fmt.Sprintf("%s requires a title", gate.ID))
			}
			if !oneOf(gate.Status, "pass", "fail", "blocked", "not_run") {
				problems = append(problems, fmt.Sprintf("%s has invalid status %q", gate.ID, gate.Status))
			}
			allGatesPass = allGatesPass && gate.Status == "pass"
		}
		if milestone.Status == "complete" && !allGatesPass {
			problems = append(problems, fmt.Sprintf("%s is complete without every release gate passing", milestone.ID))
		}
	}
	for id := range milestones {
		if !seenMilestones[id] {
			problems = append(problems, fmt.Sprintf("missing release milestone %s", id))
		}
	}

	if len(problems) > 0 {
		sort.Strings(problems)
		return fmt.Errorf("governance validation failed:\n- %s", strings.Join(problems, "\n- "))
	}
	return nil
}

func validateEvidenceTarget(root, commit string) error {
	object := exec.Command("git", "-C", root, "cat-file", "-e", commit+"^{commit}")
	if output, err := object.CombinedOutput(); err != nil {
		return fmt.Errorf("requirements baseline_commit %s is not available as a Git commit; fetch repository history: %w (%s)", commit, err, strings.TrimSpace(string(output)))
	}
	ancestor := exec.Command("git", "-C", root, "merge-base", "--is-ancestor", commit, "HEAD")
	if output, err := ancestor.CombinedOutput(); err != nil {
		if exitError, ok := err.(*exec.ExitError); ok && exitError.ExitCode() == 1 {
			return fmt.Errorf("requirements baseline_commit %s is not an ancestor of HEAD", commit)
		}
		return fmt.Errorf("validate requirements baseline_commit ancestry: %w (%s)", err, strings.TrimSpace(string(output)))
	}
	return nil
}

func RenderStatus(state State) (string, error) {
	var out strings.Builder
	fmt.Fprintf(&out, "# Project Status\n\n")
	fmt.Fprintf(&out, "> Generated by `go run ./cmd/projectctl generate`. Do not edit manually.\n\n")
	fmt.Fprintf(&out, "Baseline: `%s` · as of %s · verification target `%s`\n\n", state.Requirements.Baseline, state.Requirements.AsOf, state.Requirements.BaselineCommit)
	fmt.Fprintf(&out, "## Requirement summary\n\n")
	fmt.Fprintf(&out, "| Dimension | Status | Count |\n| --- | --- | ---: |\n")
	dimensions := []struct {
		name   string
		values []string
		get    func(Requirement) string
	}{
		{"Scope", []string{"decided", "blocked"}, func(r Requirement) string { return r.ScopeStatus }},
		{"Implementation", []string{"implemented", "partial", "blocked", "not_started"}, func(r Requirement) string { return r.ImplementationStatus }},
		{"Verification", []string{"current", "historical", "partial", "none"}, func(r Requirement) string { return r.VerificationStatus }},
		{"Migration", []string{"current", "historical", "partial", "not_assessed", "not_applicable"}, func(r Requirement) string { return r.MigrationStatus }},
		{"Acceptance", []string{"accepted", "pending", "rejected"}, func(r Requirement) string { return r.AcceptanceStatus }},
	}
	for _, dimension := range dimensions {
		counts := make(map[string]int)
		for _, requirement := range state.Requirements.Requirements {
			counts[dimension.get(requirement)]++
		}
		for _, value := range dimension.values {
			fmt.Fprintf(&out, "| %s | `%s` | %d |\n", dimension.name, value, counts[value])
		}
	}

	decisionCounts := countDecisions(state.Decisions.Decisions)
	riskCounts := countRisks(state.Risks.Risks)
	fmt.Fprintf(&out, "\n## Control summary\n\n")
	fmt.Fprintf(&out, "- Decisions: %d resolved, %d pending (18 total).\n", decisionCounts["resolved"], decisionCounts["pending"])
	fmt.Fprintf(&out, "- Risks: %d open Critical, %d open High, %d total.\n", riskCounts["open:critical"], riskCounts["open:high"], len(state.Risks.Risks))
	fmt.Fprintf(&out, "- Compatibility exceptions: %d accepted, %d proposed.\n", countExceptionStatus(state.Exceptions.Exceptions, "accepted"), countExceptionStatus(state.Exceptions.Exceptions, "proposed"))
	fmt.Fprintf(&out, "- Current-head verification: %d/80; accepted: %d/80. Historical evidence is not current acceptance.\n", countRequirementStatus(state.Requirements.Requirements, "verification", "current"), countRequirementStatus(state.Requirements.Requirements, "acceptance", "accepted"))

	fmt.Fprintf(&out, "\n## Blocked or partial requirements\n\n")
	fmt.Fprintf(&out, "| ID | Milestone | Scope | Implementation | Verification | Decisions | Work items |\n| --- | --- | --- | --- | --- | --- | --- |\n")
	for _, requirement := range state.Requirements.Requirements {
		if requirement.ScopeStatus == "blocked" || requirement.ImplementationStatus != "implemented" {
			fmt.Fprintf(&out, "| `%s` | %s | %s | %s | %s | %s | %s |\n", requirement.ID, requirement.Milestone, requirement.ScopeStatus, requirement.ImplementationStatus, requirement.VerificationStatus, joinCode(requirement.DecisionIDs), joinCode(requirement.WorkItemIDs))
		}
	}

	fmt.Fprintf(&out, "\n## Work items\n\n")
	fmt.Fprintf(&out, "| ID | Milestone | Status | Issue | Title |\n| --- | --- | --- | ---: | --- |\n")
	for _, workItem := range state.WorkItems.WorkItems {
		issue := "not linked"
		if workItem.IssueNumber > 0 {
			issue = fmt.Sprintf("[#%d](https://github.com/Hao-Monster/Xboard-Go/issues/%d)", workItem.IssueNumber, workItem.IssueNumber)
		}
		fmt.Fprintf(&out, "| `%s` | %s | %s | %s | %s |\n", workItem.ID, workItem.Milestone, workItem.Status, issue, escapeTable(workItem.Title))
	}

	fmt.Fprintf(&out, "\n## Release gates\n\n")
	fmt.Fprintf(&out, "| Milestone | Status | Passed | Blocked/failed | Not run |\n| --- | --- | ---: | ---: | ---: |\n")
	for _, milestone := range state.ReleaseGates.Milestones {
		counts := make(map[string]int)
		for _, gate := range milestone.Gates {
			counts[gate.Status]++
		}
		fmt.Fprintf(&out, "| %s — %s | %s | %d | %d | %d |\n", milestone.ID, milestone.Name, milestone.Status, counts["pass"], counts["blocked"]+counts["fail"], counts["not_run"])
	}

	fmt.Fprintf(&out, "\n## Interpretation\n\n")
	fmt.Fprintf(&out, "The repository has broad historical implementation coverage, but it is not release-ready: current-head verification and formal acceptance are still 0/80. M0 establishes the control plane; M1–M4 close business decisions, migration/operations evidence, current candidate acceptance, and production readiness in that order.\n")
	return out.String(), nil
}

func countDecisions(decisions []Decision) map[string]int {
	counts := make(map[string]int)
	for _, decision := range decisions {
		counts[decision.Status]++
	}
	return counts
}

func countRisks(risks []Risk) map[string]int {
	counts := make(map[string]int)
	for _, risk := range risks {
		counts[risk.Status+":"+risk.Severity]++
	}
	return counts
}

func countExceptionStatus(exceptions []CompatibilityException, status string) int {
	count := 0
	for _, exception := range exceptions {
		if exception.Status == status {
			count++
		}
	}
	return count
}

func countRequirementStatus(requirements []Requirement, dimension, status string) int {
	count := 0
	for _, requirement := range requirements {
		value := ""
		switch dimension {
		case "verification":
			value = requirement.VerificationStatus
		case "acceptance":
			value = requirement.AcceptanceStatus
		}
		if value == status {
			count++
		}
	}
	return count
}

func joinCode(values []string) string {
	if len(values) == 0 {
		return "—"
	}
	quoted := make([]string, 0, len(values))
	for _, value := range values {
		quoted = append(quoted, "`"+value+"`")
	}
	return strings.Join(quoted, ", ")
}

func escapeTable(value string) string {
	return strings.ReplaceAll(value, "|", "\\|")
}

func oneOf(value string, allowed ...string) bool {
	for _, candidate := range allowed {
		if value == candidate {
			return true
		}
	}
	return false
}

func expectedRequirementIDs() map[string]bool {
	groups := map[string]int{
		"AUTH": 9, "USER": 3, "PLAN": 3, "SUB": 5,
		"ORD": 4, "PAY": 2, "COUP": 1, "GIFT": 2, "INV": 1, "FIN": 1,
		"DIST": 14, "MACH": 4, "NODE": 7, "SCH": 4,
		"CONT": 3, "ATT": 5, "CLIENT": 2, "TICKET": 1, "NOTICE": 1,
		"CFG": 2, "PLUG": 2, "THEME": 1, "OPS": 3,
	}
	result := make(map[string]bool, 80)
	for prefix, count := range groups {
		for index := 1; index <= count; index++ {
			result[fmt.Sprintf("%s-%03d", prefix, index)] = true
		}
	}
	return result
}

func normalizeNewlines(value string) string {
	return strings.ReplaceAll(value, "\r\n", "\n")
}
