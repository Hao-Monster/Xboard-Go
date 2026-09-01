package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/Hao-Monster/Xboard-Go/internal/projectgovernance"
)

func main() {
	if len(os.Args) < 2 {
		fail("usage: projectctl <check|generate|pr-check>")
	}
	root, err := projectgovernance.FindRoot(".")
	if err != nil {
		fail(err.Error())
	}

	switch os.Args[1] {
	case "check":
		err = projectgovernance.Check(root)
	case "generate":
		err = projectgovernance.Generate(root)
	case "pr-check":
		flags := flag.NewFlagSet("pr-check", flag.ContinueOnError)
		eventPath := flags.String("event", os.Getenv("GITHUB_EVENT_PATH"), "path to the GitHub event JSON")
		milestone := flags.String("milestone", "", "current GitHub pull request milestone title")
		if parseErr := flags.Parse(os.Args[2:]); parseErr != nil {
			fail(parseErr.Error())
		}
		if *eventPath == "" {
			fail("pr-check requires --event or GITHUB_EVENT_PATH")
		}
		var milestoneOverride *string
		flags.Visit(func(current *flag.Flag) {
			if current.Name == "milestone" {
				milestoneOverride = milestone
			}
		})
		err = projectgovernance.CheckPREvent(root, *eventPath, milestoneOverride)
	default:
		fail("unknown command: " + os.Args[1])
	}
	if err != nil {
		fail(err.Error())
	}
	fmt.Println("project governance check passed")
}

func fail(message string) {
	fmt.Fprintln(os.Stderr, message)
	os.Exit(1)
}
