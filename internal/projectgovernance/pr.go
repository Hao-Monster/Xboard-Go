package projectgovernance

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"
)

type pullRequestEvent struct {
	PullRequest *struct {
		Body      string `json:"body"`
		Milestone *struct {
			Title string `json:"title"`
		} `json:"milestone"`
		User struct {
			Login string `json:"login"`
		} `json:"user"`
	} `json:"pull_request"`
	Sender struct {
		Login string `json:"login"`
	} `json:"sender"`
}

func CheckPREvent(root, eventPath string) error {
	data, err := os.ReadFile(eventPath)
	if err != nil {
		return fmt.Errorf("read GitHub event: %w", err)
	}
	var event pullRequestEvent
	if err := json.Unmarshal(data, &event); err != nil {
		return fmt.Errorf("decode GitHub event: %w", err)
	}
	if event.PullRequest == nil {
		return nil
	}
	actor := event.Sender.Login
	if actor == "" {
		actor = event.PullRequest.User.Login
	}
	if strings.EqualFold(actor, "dependabot[bot]") {
		return nil
	}
	state, err := Load(root)
	if err != nil {
		return err
	}
	return ValidatePRMetadata(event.PullRequest.Body, milestoneTitle(event), state)
}

func ValidatePRMetadata(body, actualMilestone string, state State) error {
	body = regexp.MustCompile(`(?s)<!--.*?-->`).ReplaceAllString(body, "")
	fields := map[string]string{}
	for _, name := range []string{"Requirement IDs", "Work item IDs", "Milestone", "Closes"} {
		match := regexp.MustCompile(`(?mi)^` + regexp.QuoteMeta(name) + `:\s*(.+?)\s*$`).FindStringSubmatch(body)
		if len(match) != 2 {
			return fmt.Errorf("PR body is missing `%s:` governance metadata", name)
		}
		fields[name] = strings.TrimSpace(match[1])
	}

	requirementIDs := make(map[string]bool)
	for _, requirement := range state.Requirements.Requirements {
		requirementIDs[requirement.ID] = true
	}
	workItemIDs := make(map[string]bool)
	for _, workItem := range state.WorkItems.WorkItems {
		workItemIDs[workItem.ID] = true
	}

	requirementCount, err := validateMetadataIDs(fields["Requirement IDs"], requirementIDs, regexp.MustCompile(`[A-Z]+-\d{3}`), "requirement")
	if err != nil {
		return err
	}
	workItemCount, err := validateMetadataIDs(fields["Work item IDs"], workItemIDs, regexp.MustCompile(`[A-Z]+-\d{3}`), "work item")
	if err != nil {
		return err
	}
	if requirementCount+workItemCount == 0 {
		return fmt.Errorf("PR must reference at least one recognized requirement or work item")
	}

	milestone := fields["Milestone"]
	if !regexp.MustCompile(`^M[0-4]$`).MatchString(milestone) {
		return fmt.Errorf("Milestone must be one of M0, M1, M2, M3, or M4")
	}
	if actualMilestone == "" || !strings.HasPrefix(actualMilestone, milestone+" ") {
		return fmt.Errorf("PR must be assigned to a GitHub milestone beginning with %s", milestone)
	}
	if !regexp.MustCompile(`#\d+`).MatchString(fields["Closes"]) && !validNA(fields["Closes"]) {
		return fmt.Errorf("Closes must reference an issue number or use `N/A: reason`")
	}
	return nil
}

func validateMetadataIDs(value string, known map[string]bool, pattern *regexp.Regexp, label string) (int, error) {
	if validNA(value) {
		return 0, nil
	}
	ids := pattern.FindAllString(value, -1)
	if len(ids) == 0 {
		return 0, fmt.Errorf("%s metadata must contain recognized IDs or `N/A: reason`", label)
	}
	seen := make(map[string]bool)
	for _, id := range ids {
		if !known[id] {
			return 0, fmt.Errorf("unknown %s ID %s", label, id)
		}
		seen[id] = true
	}
	return len(seen), nil
}

func validNA(value string) bool {
	parts := strings.SplitN(value, ":", 2)
	return len(parts) == 2 && strings.EqualFold(strings.TrimSpace(parts[0]), "N/A") && len(strings.TrimSpace(parts[1])) >= 5
}

func milestoneTitle(event pullRequestEvent) string {
	if event.PullRequest.Milestone == nil {
		return ""
	}
	return event.PullRequest.Milestone.Title
}
