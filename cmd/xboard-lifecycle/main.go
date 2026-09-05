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
	topology := flags.String("topology", "combined", "deployment topology: combined or split")
	manifestPath := flags.String("deployment-manifest", "", "versioned split deployment manifest")
	healthTimeout := flags.Duration("health-timeout", 60*time.Second, "target container health timeout")
	if err := flags.Parse(arguments[1:]); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("lifecycle commands do not accept positional arguments")
	}
	if *topology != "combined" && *topology != "split" {
		return errors.New("topology must be combined or split")
	}
	if (command == "status" || command == "rollback") && (strings.TrimSpace(*imageReference) != "" || strings.TrimSpace(*manifestPath) != "") {
		return fmt.Errorf("lifecycle %s does not accept --image", command)
	}
	if *topology == "combined" && strings.TrimSpace(*manifestPath) != "" {
		return errors.New("combined topology does not accept --deployment-manifest")
	}
	if *topology == "combined" && (command == "install" || command == "upgrade") && strings.TrimSpace(*imageReference) == "" {
		return fmt.Errorf("lifecycle %s requires --image", command)
	}
	if *topology == "split" && strings.TrimSpace(*imageReference) != "" {
		return errors.New("split topology does not accept --image")
	}
	if *topology == "split" && (command == "install" || command == "upgrade") && strings.TrimSpace(*manifestPath) == "" {
		return fmt.Errorf("split lifecycle %s requires --deployment-manifest", command)
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
	if *topology == "split" {
		return runSplit(ctx, command, *manifestPath, absoluteStateDirectory, *project, platform, stdout, now)
	}

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

func runSplit(ctx context.Context, command, manifestPath, stateDirectory, project string, platform *lifecycle.DockerPlatform, stdout io.Writer, now func() time.Time) error {
	journal := lifecycle.NewDeploymentJournal(filepath.Join(stateDirectory, project+"-split.jsonl"))
	orchestrator := lifecycle.NewDeploymentOrchestrator(platform, journal, now)
	if command == "status" {
		result, err := orchestrator.Status(ctx)
		return encodeDeploymentResult(stdout, result, err)
	}
	lock, err := lifecycle.AcquireLock(filepath.Join(stateDirectory, project+"-split.lock"), now().UTC())
	if err != nil {
		return err
	}
	var result lifecycle.DeploymentResult
	switch command {
	case "install", "upgrade":
		manifest, loadErr := lifecycle.LoadDeploymentManifest(manifestPath)
		if loadErr != nil {
			return errors.Join(loadErr, lock.Release())
		}
		if command == "install" {
			result, err = orchestrator.Install(ctx, manifest)
		} else {
			result, err = orchestrator.Upgrade(ctx, manifest)
		}
	case "rollback":
		result, err = orchestrator.Rollback(ctx)
	}
	return errors.Join(encodeDeploymentResult(stdout, result, err), lock.Release())
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

func encodeDeploymentResult(output io.Writer, result lifecycle.DeploymentResult, operationErr error) error {
	if result.Action != "" {
		encoder := json.NewEncoder(output)
		encoder.SetEscapeHTML(false)
		if err := encoder.Encode(result); err != nil {
			return fmt.Errorf("encode deployment lifecycle result: %w", err)
		}
	}
	return operationErr
}
