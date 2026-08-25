package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/Hao-Monster/Xboard-Go/internal/lifecycle"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:], os.Stdout, os.Stderr, time.Now); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, arguments []string, stdout, stderr io.Writer, now func() time.Time) error {
	if len(arguments) == 0 {
		return errors.New("lifecycle subcommand is required: status, install, upgrade, or rollback")
	}
	command := arguments[0]
	if command != "status" && command != "install" && command != "upgrade" && command != "rollback" {
		return fmt.Errorf("unknown lifecycle subcommand %q", command)
	}

	flags := flag.NewFlagSet("xboard-lifecycle "+command, flag.ContinueOnError)
	flags.SetOutput(stderr)
	project := flags.String("project", "xboard-go-local", "Docker Compose project name")
	composeFile := flags.String("compose-file", "compose.local.yaml", "Docker Compose file")
	stateDirectory := flags.String("state-dir", ".local/lifecycle", "local lifecycle state directory")
	baseEnvFile := flags.String("env-file", "", "optional operator-owned Compose environment file")
	imageReference := flags.String("image", "", "existing local immutable Xboard-Go image reference")
	healthTimeout := flags.Duration("health-timeout", 60*time.Second, "target container health timeout")
	if err := flags.Parse(arguments[1:]); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("lifecycle commands do not accept positional arguments")
	}
	if (command == "install" || command == "upgrade") && strings.TrimSpace(*imageReference) == "" {
		return fmt.Errorf("lifecycle %s requires --image", command)
	}
	if (command == "status" || command == "rollback") && strings.TrimSpace(*imageReference) != "" {
		return fmt.Errorf("lifecycle %s does not accept --image", command)
	}
	if *healthTimeout <= 0 || *healthTimeout > 10*time.Minute {
		return errors.New("health-timeout must be between 1ns and 10m")
	}

	absoluteStateDirectory, err := filepath.Abs(*stateDirectory)
	if err != nil {
		return fmt.Errorf("resolve lifecycle state directory: %w", err)
	}
	platform, err := lifecycle.NewDockerPlatform(lifecycle.DockerConfig{
		Project: *project, ComposeFile: *composeFile, BaseEnvFile: *baseEnvFile,
		RuntimeEnvDir: filepath.Join(absoluteStateDirectory, "environment", *project),
		HealthTimeout: *healthTimeout,
	}, nil)
	if err != nil {
		return err
	}
	journal := lifecycle.NewJournal(filepath.Join(absoluteStateDirectory, *project+".jsonl"))
	orchestrator := lifecycle.NewOrchestrator(platform, journal, now)

	if command == "status" {
		result, err := orchestrator.Status(ctx)
		return encodeResult(stdout, result, err)
	}
	lock, err := lifecycle.AcquireLock(filepath.Join(absoluteStateDirectory, *project+".lock"), now().UTC())
	if err != nil {
		return err
	}

	var result lifecycle.Result
	switch command {
	case "install":
		result, err = orchestrator.Install(ctx, *imageReference)
	case "upgrade":
		result, err = orchestrator.Upgrade(ctx, *imageReference)
	case "rollback":
		result, err = orchestrator.Rollback(ctx)
	}
	operationErr := encodeResult(stdout, result, err)
	return errors.Join(operationErr, lock.Release())
}

func encodeResult(output io.Writer, result lifecycle.Result, operationErr error) error {
	if result.Action != "" {
		encoder := json.NewEncoder(output)
		encoder.SetEscapeHTML(false)
		if err := encoder.Encode(result); err != nil {
			return fmt.Errorf("encode lifecycle result: %w", err)
		}
	}
	return operationErr
}
