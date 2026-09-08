package projectgovernance

import (
	"regexp"
	"strings"
)

// This rejects known execution mismatches; it does not authenticate a log or
// infer that a test passed. Reviewers must still inspect the referenced artifact.
func evidenceExecutionProblem(evidence Evidence) string {
	commands := evidenceCommands(evidence.Command)
	hasGoTest := false
	for _, command := range commands {
		hasGoTest = hasGoTest || (len(command) >= 2 && command[0] == "go" && command[1] == "test")
	}
	for _, caseID := range evidence.CaseIDs {
		if strings.HasPrefix(caseID, "e2e/") || strings.HasPrefix(caseID, "parity/") || evidence.Kind == "browser" {
			covered := false
			for _, command := range commands {
				covered = covered || browserCommandCovers(command, caseID, evidence.Kind)
			}
			if !covered {
				return "declared browser/parity case has no matching Playwright execution"
			}
		}
		if strings.HasPrefix(caseID, "Benchmark") {
			covered := false
			for _, command := range commands {
				if len(command) < 2 || command[0] != "go" || command[1] != "test" {
					continue
				}
				pattern := commandOption(command[2:], "-bench")
				if pattern != "" {
					matched, err := regexp.MatchString(pattern, caseID)
					covered = covered || (err == nil && matched)
				}
			}
			if !covered {
				return "declared benchmark has no matching go test -bench execution"
			}
		}
		switch {
		case strings.Contains(caseID, "TestOneGiBAttachmentCompletesWithChunkBoundedMemory"):
			if !hasGoTest || !strings.Contains(evidence.Command, "XBOARD_RUN_LARGE_ATTACHMENT_TEST=1") {
				return "large attachment case requires its explicit opt-in environment"
			}
		case strings.Contains(caseID, "TestCAPR014WebSocketConnectionDrain"):
			count := `[1-9][0-9]*`
			if _, suffix, ok := strings.Cut(caseID, "/"); ok {
				count = regexp.QuoteMeta(suffix)
			}
			if !hasGoTest || !regexp.MustCompile(`XBOARD_WS_CAPACITY_CONNECTIONS=`+count+`(\s|$)`).MatchString(evidence.Command) || !strings.Contains(evidence.Command, "XBOARD_TEST_REDIS_URL=") {
				return "capacity case requires connection count and Redis environment"
			}
		case caseID == "REAL-NODE-CLIENT-SMOKE", caseID == "TestR014ReconnectJitterDistribution", caseID == "TestWSClient_ReconnectOnDisconnect":
			if !strings.Contains(evidence.Command, "Xboard-Node") {
				return "external node case requires explicit Xboard-Node execution"
			}
		case strings.HasPrefix(caseID, "client/"):
			found := false
			for _, command := range commands {
				found = found || (len(command) > 0 && command[0] == ".github/scripts/check-real-client-compat.sh")
			}
			if !found {
				return "real-client case requires the pinned client compatibility runner"
			}
		}
	}
	return ""
}

var evidenceCommandSeparator = regexp.MustCompile(`[;\r\n]+|&&|\|\|`)

func evidenceCommands(command string) [][]string {
	var commands [][]string
	for _, segment := range evidenceCommandSeparator.Split(command, -1) {
		words := strings.Fields(segment)
		for len(words) > 0 && (words[0] == "env" || strings.Contains(words[0], "=")) {
			words = words[1:]
		}
		if len(words) > 0 {
			commands = append(commands, words)
		}
	}
	return commands
}

func browserCommandCovers(words []string, caseID, kind string) bool {
	if len(words) > 0 && words[0] == "corepack" {
		words = words[1:]
	}
	packageRunner := len(words) > 0 && words[0] == "pnpm"
	if packageRunner {
		words = words[1:]
		if len(words) >= 2 && (words[0] == "--dir" || words[0] == "-C") {
			words = words[2:]
		}
		if len(words) > 0 && (words[0] == "exec" || words[0] == "run") {
			words = words[1:]
		}
	}
	parity := strings.HasPrefix(caseID, "parity/") || kind == "differential"
	if packageRunner && len(words) == 1 {
		if parity {
			return words[0] == "test:parity"
		}
		return oneOf(words[0], "test:e2e", "test:e2e:desktop", "test:e2e:mobile")
	}
	if len(words) < 2 || words[0] != "playwright" || words[1] != "test" {
		return false
	}
	args := words[2:]
	for _, arg := range args {
		if oneOf(arg, "--list", "--list-tests", "--help", "-h") {
			return false
		}
	}
	config := commandOption(args, "--config")
	if parity != strings.HasSuffix(config, "playwright.parity.config.ts") {
		return false
	}
	casePath := strings.Split(caseID, ":")[0]
	hasFile, matchesFile := false, false
	for _, arg := range args {
		arg = strings.Trim(arg, "\"'")
		if strings.Contains(arg, ".spec.") {
			hasFile = true
			matchesFile = matchesFile || arg == casePath || arg == "web/"+casePath
		}
	}
	return !hasFile || matchesFile
}

func commandOption(args []string, option string) string {
	for index, arg := range args {
		if arg == option && index+1 < len(args) {
			return strings.Trim(args[index+1], "\"'")
		}
		if strings.HasPrefix(arg, option+"=") {
			return strings.Trim(strings.TrimPrefix(arg, option+"="), "\"'")
		}
	}
	return ""
}
