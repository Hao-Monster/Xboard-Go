package lifecycle

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"time"

	"github.com/Hao-Monster/Xboard-Go/internal/backup"
)

const runtimeImageTagPrefix = "xboard-go:lifecycle-"

var projectPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,62}$`)

type CommandOutput struct {
	Stdout string
	Stderr string
}

type CommandRunner interface {
	Run(context.Context, string, ...string) (CommandOutput, error)
}

type ExecRunner struct{}

func (ExecRunner) Run(ctx context.Context, name string, arguments ...string) (CommandOutput, error) {
	command := exec.CommandContext(ctx, name, arguments...)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	err := command.Run()
	return CommandOutput{Stdout: stdout.String(), Stderr: stderr.String()}, err
}

type DockerConfig struct {
	Project       string
	ComposeFile   string
	BaseEnvFile   string
	RuntimeEnvDir string
	HealthTimeout time.Duration
	PollInterval  time.Duration
}

type DockerPlatform struct {
	config DockerConfig
	runner CommandRunner
}

func NewDockerPlatform(config DockerConfig, runner CommandRunner) (*DockerPlatform, error) {
	config.Project = strings.TrimSpace(config.Project)
	if !projectPattern.MatchString(config.Project) {
		return nil, fmt.Errorf("invalid Docker Compose project %q", config.Project)
	}
	composeFile, err := filepath.Abs(config.ComposeFile)
	if err != nil {
		return nil, fmt.Errorf("resolve Compose file: %w", err)
	}
	info, err := os.Stat(composeFile)
	if err != nil || !info.Mode().IsRegular() {
		return nil, fmt.Errorf("Compose file must be an existing regular file: %s", composeFile)
	}
	config.ComposeFile = composeFile
	if strings.TrimSpace(config.BaseEnvFile) == "" {
		candidate := filepath.Join(filepath.Dir(composeFile), ".env")
		if candidateInfo, candidateErr := os.Stat(candidate); candidateErr == nil && candidateInfo.Mode().IsRegular() {
			config.BaseEnvFile = candidate
		}
	} else {
		config.BaseEnvFile, err = filepath.Abs(config.BaseEnvFile)
		if err != nil {
			return nil, fmt.Errorf("resolve base environment file: %w", err)
		}
		if envInfo, envErr := os.Stat(config.BaseEnvFile); envErr != nil || !envInfo.Mode().IsRegular() {
			return nil, fmt.Errorf("base environment file must be an existing regular file: %s", config.BaseEnvFile)
		}
	}
	if strings.TrimSpace(config.RuntimeEnvDir) == "" {
		return nil, errors.New("lifecycle environment directory is required")
	}
	config.RuntimeEnvDir, err = filepath.Abs(config.RuntimeEnvDir)
	if err != nil {
		return nil, fmt.Errorf("resolve lifecycle environment directory: %w", err)
	}
	if config.HealthTimeout <= 0 {
		config.HealthTimeout = 60 * time.Second
	}
	if config.PollInterval <= 0 {
		config.PollInterval = 500 * time.Millisecond
	}
	if runner == nil {
		runner = ExecRunner{}
	}
	return &DockerPlatform{config: config, runner: runner}, nil
}

func (p *DockerPlatform) Current(ctx context.Context) (Application, error) {
	output, err := p.runner.Run(ctx, "docker", "container", "inspect", p.containerName())
	if err != nil {
		return Application{}, commandFailure("inspect active application", err)
	}
	var records []struct {
		Image  string `json:"Image"`
		Config struct {
			Env    []string          `json:"Env"`
			Labels map[string]string `json:"Labels"`
		} `json:"Config"`
		State struct {
			Status string `json:"Status"`
			Health *struct {
				Status string `json:"Status"`
			} `json:"Health"`
		} `json:"State"`
	}
	if err := json.Unmarshal([]byte(output.Stdout), &records); err != nil || len(records) != 1 {
		return Application{}, errors.New("decode active application inspection")
	}
	record := records[0]
	image := Image{ID: record.Image, Revision: record.Config.Labels["org.opencontainers.image.revision"]}
	if err := validateImageLabels(record.Config.Labels); err != nil {
		return Application{}, err
	}
	dsn := ""
	for _, environment := range record.Config.Env {
		if strings.HasPrefix(environment, "XBOARD_DATABASE_DSN=") {
			dsn = strings.TrimPrefix(environment, "XBOARD_DATABASE_DSN=")
			break
		}
	}
	healthStatus := "missing"
	if record.State.Health != nil {
		healthStatus = record.State.Health.Status
	}
	healthy := record.State.Status == "running" && healthStatus == "healthy"
	return Application{
		Image: image, DSN: dsn, ContainerStatus: record.State.Status,
		HealthStatus: healthStatus, Healthy: healthy,
	}, nil
}

func (p *DockerPlatform) ResolveImage(ctx context.Context, reference string) (Image, error) {
	reference = strings.TrimSpace(reference)
	if reference == "" {
		return Image{}, errors.New("image reference is required")
	}
	output, err := p.runner.Run(ctx, "docker", "image", "inspect", reference)
	if err != nil {
		return Image{}, commandFailure("inspect target image", err)
	}
	var records []struct {
		ID     string `json:"Id"`
		Config struct {
			Labels map[string]string `json:"Labels"`
		} `json:"Config"`
	}
	if err := json.Unmarshal([]byte(output.Stdout), &records); err != nil || len(records) != 1 {
		return Image{}, errors.New("decode target image inspection")
	}
	if err := validateImageLabels(records[0].Config.Labels); err != nil {
		return Image{}, err
	}
	image := Image{ID: records[0].ID, Revision: records[0].Config.Labels["org.opencontainers.image.revision"]}
	if err := validateImage(image); err != nil {
		return Image{}, err
	}
	return image, nil
}

func (p *DockerPlatform) ResolveComponentImage(ctx context.Context, component Component, expected ComponentImage) (Image, error) {
	if err := validateComponentImage(expected); err != nil {
		return Image{}, err
	}
	titles := map[Component]string{
		ComponentGateway: "Xboard-Go Gateway", ComponentFrontend: "Xboard-Go Frontend", ComponentBackend: "Xboard-Go Backend",
	}
	title, ok := titles[component]
	if !ok {
		return Image{}, fmt.Errorf("unsupported deployment component %q", component)
	}
	output, err := p.runner.Run(ctx, "docker", "image", "inspect", expected.Reference)
	if err != nil {
		return Image{}, commandFailure("inspect target "+string(component)+" image", err)
	}
	var records []struct {
		ID     string `json:"Id"`
		Config struct {
			Labels map[string]string `json:"Labels"`
		} `json:"Config"`
	}
	if err := json.Unmarshal([]byte(output.Stdout), &records); err != nil || len(records) != 1 {
		return Image{}, fmt.Errorf("decode target %s image inspection", component)
	}
	labels := records[0].Config.Labels
	if labels["org.opencontainers.image.title"] != title || labels["org.opencontainers.image.licenses"] != "Apache-2.0" {
		return Image{}, fmt.Errorf("target %s image has an untrusted identity", component)
	}
	if records[0].ID != expected.ID {
		return Image{}, fmt.Errorf("target %s image ID %s does not match manifest %s", component, records[0].ID, expected.ID)
	}
	if labels["org.opencontainers.image.revision"] != expected.Revision {
		return Image{}, fmt.Errorf("target %s revision does not match manifest image revision", component)
	}
	return Image{ID: records[0].ID, Revision: expected.Revision}, nil
}

func (p *DockerPlatform) ResolveDeployment(ctx context.Context, manifest DeploymentManifest) (Deployment, error) {
	if err := ValidateDeploymentManifest(manifest); err != nil {
		return Deployment{}, err
	}
	gateway, err := p.ResolveComponentImage(ctx, ComponentGateway, manifest.Gateway)
	if err != nil {
		return Deployment{}, err
	}
	frontend, err := p.ResolveComponentImage(ctx, ComponentFrontend, manifest.Frontend)
	if err != nil {
		return Deployment{}, err
	}
	backend, err := p.ResolveComponentImage(ctx, ComponentBackend, manifest.Backend)
	if err != nil {
		return Deployment{}, err
	}
	deployment := Deployment{SourceRevision: manifest.Revision, Gateway: gateway, Frontend: frontend, Backend: backend}
	deployment.ID = deploymentFingerprint(gateway, frontend, backend)
	if err := validateDeployment(deployment); err != nil {
		return Deployment{}, err
	}
	return deployment, nil
}

func (p *DockerPlatform) CurrentDeployment(ctx context.Context) (DeploymentApplication, error) {
	names := []string{p.deploymentContainerName(ComponentGateway), p.deploymentContainerName(ComponentFrontend), p.deploymentContainerName(ComponentBackend)}
	output, err := p.runner.Run(ctx, "docker", "container", "inspect", names[0], names[1], names[2])
	if err != nil {
		return DeploymentApplication{}, commandFailure("inspect active deployment", err)
	}
	var records []struct {
		Image  string `json:"Image"`
		Config struct {
			Env    []string          `json:"Env"`
			Labels map[string]string `json:"Labels"`
		} `json:"Config"`
		State struct {
			Status string `json:"Status"`
			Health *struct {
				Status string `json:"Status"`
			} `json:"Health"`
		} `json:"State"`
	}
	if err := json.Unmarshal([]byte(output.Stdout), &records); err != nil || len(records) != 3 {
		return DeploymentApplication{}, errors.New("decode active deployment inspection")
	}
	deployment := Deployment{}
	healthy := true
	dsn := ""
	for index, component := range []Component{ComponentGateway, ComponentFrontend, ComponentBackend} {
		record := records[index]
		title := map[Component]string{ComponentGateway: "Xboard-Go Gateway", ComponentFrontend: "Xboard-Go Frontend", ComponentBackend: "Xboard-Go Backend"}[component]
		if record.Config.Labels["org.opencontainers.image.title"] != title || record.Config.Labels["org.opencontainers.image.licenses"] != "Apache-2.0" {
			return DeploymentApplication{}, fmt.Errorf("active %s image has an untrusted identity", component)
		}
		image := Image{ID: record.Image, Revision: record.Config.Labels["org.opencontainers.image.revision"]}
		switch component {
		case ComponentGateway:
			deployment.Gateway = image
		case ComponentFrontend:
			deployment.Frontend = image
		case ComponentBackend:
			deployment.Backend = image
			for _, environment := range record.Config.Env {
				if strings.HasPrefix(environment, "XBOARD_DATABASE_DSN=") {
					dsn = strings.TrimPrefix(environment, "XBOARD_DATABASE_DSN=")
				}
			}
		}
		healthy = healthy && record.State.Status == "running" && record.State.Health != nil && record.State.Health.Status == "healthy"
	}
	deployment.ID = deploymentFingerprint(deployment.Gateway, deployment.Frontend, deployment.Backend)
	application := DeploymentApplication{Deployment: deployment, DSN: dsn, Healthy: healthy}
	if err := validateDeployment(deployment); err != nil {
		return DeploymentApplication{}, err
	}
	return application, nil
}

func (p *DockerPlatform) FreshDeployment(ctx context.Context) (bool, error) {
	for _, component := range []Component{ComponentGateway, ComponentFrontend, ComponentBackend} {
		output, err := p.runner.Run(ctx, "docker", "container", "ls", "--all", "--quiet",
			"--filter", "label=com.docker.compose.project="+p.config.Project,
			"--filter", "label=com.docker.compose.service="+string(component))
		if err != nil {
			return false, commandFailure("list deployment containers", err)
		}
		if strings.TrimSpace(output.Stdout) != "" {
			return false, nil
		}
	}
	volume, err := p.runner.Run(ctx, "docker", "volume", "ls", "--quiet",
		"--filter", "label=com.docker.compose.project="+p.config.Project,
		"--filter", "label=com.docker.compose.volume=xboard-go-data")
	if err != nil {
		return false, commandFailure("list deployment data volume", err)
	}
	return strings.TrimSpace(volume.Stdout) == "", nil
}

func (p *DockerPlatform) ActivateDeployment(ctx context.Context, deployment Deployment, dsn string, changed []Component) (DeploymentApplication, error) {
	if err := validateDeployment(deployment); err != nil {
		return DeploymentApplication{}, err
	}
	if !databasePattern.MatchString(dsn) {
		return DeploymentApplication{}, fmt.Errorf("unsupported deployment database DSN %q", dsn)
	}
	if len(changed) == 0 {
		return DeploymentApplication{}, errors.New("deployment activation requires changed components")
	}
	images := map[Component]Image{ComponentGateway: deployment.Gateway, ComponentFrontend: deployment.Frontend, ComponentBackend: deployment.Backend}
	for _, component := range []Component{ComponentGateway, ComponentFrontend, ComponentBackend} {
		if _, err := p.runner.Run(ctx, "docker", "image", "tag", images[component].ID, p.deploymentRuntimeImageTag(component)); err != nil {
			return DeploymentApplication{}, commandFailure("bind target "+string(component)+" image", err)
		}
	}
	ordered := make([]string, 0, len(changed))
	for _, component := range []Component{ComponentFrontend, ComponentBackend, ComponentGateway} {
		if containsComponent(changed, component) {
			ordered = append(ordered, string(component))
		}
	}
	arguments := []string{"up", "-d", "--no-build", "--force-recreate"}
	if len(changed) != 3 {
		arguments = append(arguments, "--no-deps")
	}
	arguments = append(arguments, ordered...)
	if _, err := p.runDeploymentCompose(ctx, deployment, dsn, arguments...); err != nil {
		return DeploymentApplication{}, commandFailure("activate target deployment", err)
	}
	deadline := time.Now().Add(p.config.HealthTimeout)
	for {
		application, currentErr := p.CurrentDeployment(ctx)
		if currentErr == nil && sameDeploymentImages(application.Deployment, deployment) && application.DSN == dsn && application.Healthy {
			application.Deployment.SourceRevision = deployment.SourceRevision
			return application, nil
		}
		if time.Now().After(deadline) {
			return DeploymentApplication{}, errors.New("target deployment health check timed out")
		}
		timer := time.NewTimer(p.config.PollInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return DeploymentApplication{}, ctx.Err()
		case <-timer.C:
		}
	}
}

func (p *DockerPlatform) BackupDeployment(ctx context.Context, current DeploymentApplication, outputPath string) (backup.Manifest, error) {
	if err := validateDeploymentApplication(current); err != nil {
		return backup.Manifest{}, err
	}
	images := map[Component]Image{ComponentGateway: current.Deployment.Gateway, ComponentFrontend: current.Deployment.Frontend, ComponentBackend: current.Deployment.Backend}
	for _, component := range []Component{ComponentGateway, ComponentFrontend, ComponentBackend} {
		if _, err := p.runner.Run(ctx, "docker", "image", "tag", images[component].ID, p.deploymentRuntimeImageTag(component)); err != nil {
			return backup.Manifest{}, commandFailure("bind current "+string(component)+" image", err)
		}
	}
	created, err := p.runDeploymentBackupCommand(ctx, current.Deployment, current.DSN, "create", "--output", outputPath)
	if err != nil {
		return backup.Manifest{}, err
	}
	verified, err := p.runDeploymentBackupCommand(ctx, current.Deployment, current.DSN, "verify", "--input", outputPath)
	if err != nil {
		return backup.Manifest{}, err
	}
	if !reflect.DeepEqual(created, verified) {
		return backup.Manifest{}, errors.New("created and verified deployment backup manifests differ")
	}
	return verified, nil
}

func (p *DockerPlatform) RestoreDeployment(ctx context.Context, backendImage Image, inputPath, outputPath, attachmentOutputPath string) (backup.Manifest, error) {
	if err := validateImage(backendImage); err != nil {
		return backup.Manifest{}, err
	}
	expectedAttachmentRoot, err := attachmentRootForDSN("file:" + outputPath)
	if err != nil {
		return backup.Manifest{}, err
	}
	if attachmentOutputPath != "" && attachmentOutputPath != expectedAttachmentRoot {
		return backup.Manifest{}, errors.New("rollback attachment path does not match the restored database")
	}
	if _, err := p.runner.Run(ctx, "docker", "image", "tag", backendImage.ID, p.deploymentRuntimeImageTag(ComponentBackend)); err != nil {
		return backup.Manifest{}, commandFailure("bind rollback backend maintenance image", err)
	}
	deployment := Deployment{SourceRevision: backendImage.Revision, Gateway: backendImage, Frontend: backendImage, Backend: backendImage}
	deployment.ID = deploymentFingerprint(backendImage, backendImage, backendImage)
	arguments := []string{"--input", inputPath, "--output", outputPath}
	if attachmentOutputPath != "" {
		arguments = append(arguments, "--attachment-output", attachmentOutputPath)
	}
	return p.runDeploymentBackupCommand(ctx, deployment, defaultDatabaseDSN, "restore", arguments...)
}

func (p *DockerPlatform) Fresh(ctx context.Context) (bool, error) {
	containerOutput, err := p.runner.Run(ctx, "docker", "container", "ls", "--all", "--quiet",
		"--filter", "label=com.docker.compose.project="+p.config.Project,
		"--filter", "label=com.docker.compose.service=xboard-go")
	if err != nil {
		return false, commandFailure("list application containers", err)
	}
	volumeOutput, err := p.runner.Run(ctx, "docker", "volume", "ls", "--quiet",
		"--filter", "label=com.docker.compose.project="+p.config.Project,
		"--filter", "label=com.docker.compose.volume=xboard-go-data")
	if err != nil {
		return false, commandFailure("list application data volumes", err)
	}
	return strings.TrimSpace(containerOutput.Stdout) == "" && strings.TrimSpace(volumeOutput.Stdout) == "", nil
}

func (p *DockerPlatform) Activate(ctx context.Context, image Image, dsn string) (Application, error) {
	if err := validateImage(image); err != nil {
		return Application{}, err
	}
	if !databasePattern.MatchString(dsn) {
		return Application{}, fmt.Errorf("unsupported activation database DSN %q", dsn)
	}
	if _, err := p.runtimeEnvFile(dsn); err != nil {
		return Application{}, err
	}
	if _, err := p.runner.Run(ctx, "docker", "image", "tag", image.ID, p.runtimeImageTag()); err != nil {
		return Application{}, commandFailure("bind target runtime image", err)
	}
	if _, err := p.runCompose(ctx, dsn, "stop", "xboard-go"); err != nil {
		return Application{}, commandFailure("stop active application", err)
	}
	if _, err := p.runCompose(ctx, dsn, "up", "-d", "--no-build", "--force-recreate", "xboard-go"); err != nil {
		return Application{}, commandFailure("start target application", err)
	}

	deadline := time.Now().Add(p.config.HealthTimeout)
	for {
		application, err := p.Current(ctx)
		if err == nil {
			if application.Image.ID != image.ID {
				return Application{}, fmt.Errorf("active image ID %s does not match target %s", application.Image.ID, image.ID)
			}
			if application.Image.Revision != image.Revision {
				return Application{}, fmt.Errorf("active revision %s does not match target %s", application.Image.Revision, image.Revision)
			}
			if application.DSN != dsn {
				return Application{}, fmt.Errorf("active database DSN %q does not match target %q", application.DSN, dsn)
			}
			if application.Healthy {
				return application, nil
			}
			if application.ContainerStatus != "running" {
				return Application{}, fmt.Errorf("target application stopped before becoming healthy (status=%s)", application.ContainerStatus)
			}
			if application.HealthStatus == "unhealthy" {
				return Application{}, errors.New("target application reported unhealthy")
			}
		}
		if time.Now().After(deadline) {
			return Application{}, errors.New("target application health check timed out")
		}
		timer := time.NewTimer(p.config.PollInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return Application{}, ctx.Err()
		case <-timer.C:
		}
	}
}

func (p *DockerPlatform) Backup(ctx context.Context, current Application, outputPath string) (backup.Manifest, error) {
	if err := validateApplication(current); err != nil {
		return backup.Manifest{}, err
	}
	if _, err := p.runner.Run(ctx, "docker", "image", "tag", current.Image.ID, p.runtimeImageTag()); err != nil {
		return backup.Manifest{}, commandFailure("bind current maintenance image", err)
	}
	created, err := p.runBackupCommand(ctx, current.DSN, "create", "--output", outputPath)
	if err != nil {
		return backup.Manifest{}, err
	}
	verified, err := p.runBackupCommand(ctx, current.DSN, "verify", "--input", outputPath)
	if err != nil {
		return backup.Manifest{}, err
	}
	if !reflect.DeepEqual(created, verified) {
		return backup.Manifest{}, errors.New("created and verified backup manifests differ")
	}
	return verified, nil
}

func (p *DockerPlatform) Restore(ctx context.Context, image Image, inputPath, outputPath, attachmentOutputPath string) (backup.Manifest, error) {
	if err := validateImage(image); err != nil {
		return backup.Manifest{}, err
	}
	expectedAttachmentRoot, err := attachmentRootForDSN("file:" + outputPath)
	if err != nil {
		return backup.Manifest{}, err
	}
	if attachmentOutputPath != "" && attachmentOutputPath != expectedAttachmentRoot {
		return backup.Manifest{}, errors.New("rollback attachment path does not match the restored database")
	}
	if _, err := p.runner.Run(ctx, "docker", "image", "tag", image.ID, p.runtimeImageTag()); err != nil {
		return backup.Manifest{}, commandFailure("bind rollback maintenance image", err)
	}
	arguments := []string{"--input", inputPath, "--output", outputPath}
	if attachmentOutputPath != "" {
		arguments = append(arguments, "--attachment-output", attachmentOutputPath)
	}
	return p.runBackupCommand(ctx, defaultDatabaseDSN, "restore", arguments...)
}

func (p *DockerPlatform) runBackupCommand(ctx context.Context, dsn, subcommand string, arguments ...string) (backup.Manifest, error) {
	tail := []string{"--profile", "maintenance", "run", "--rm", "--no-deps", "maintenance", "backup", subcommand}
	tail = append(tail, arguments...)
	output, err := p.runCompose(ctx, dsn, tail...)
	if err != nil {
		return backup.Manifest{}, commandFailure("run backup "+subcommand, err)
	}
	var result struct {
		Status   string          `json:"status"`
		Action   string          `json:"action"`
		Manifest backup.Manifest `json:"manifest"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(output.Stdout)), &result); err != nil {
		return backup.Manifest{}, fmt.Errorf("decode backup %s result", subcommand)
	}
	if result.Status != "success" || result.Action != "backup."+subcommand {
		return backup.Manifest{}, fmt.Errorf("backup %s did not return success", subcommand)
	}
	return result.Manifest, nil
}

func (p *DockerPlatform) runDeploymentBackupCommand(ctx context.Context, deployment Deployment, dsn, subcommand string, arguments ...string) (backup.Manifest, error) {
	tail := []string{"--profile", "maintenance", "run", "--rm", "--no-deps", "maintenance", "backup", subcommand}
	tail = append(tail, arguments...)
	output, err := p.runDeploymentCompose(ctx, deployment, dsn, tail...)
	if err != nil {
		return backup.Manifest{}, commandFailure("run deployment backup "+subcommand, err)
	}
	var result struct {
		Status   string          `json:"status"`
		Action   string          `json:"action"`
		Manifest backup.Manifest `json:"manifest"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(output.Stdout)), &result); err != nil {
		return backup.Manifest{}, fmt.Errorf("decode deployment backup %s result", subcommand)
	}
	if result.Status != "success" || result.Action != "backup."+subcommand {
		return backup.Manifest{}, fmt.Errorf("deployment backup %s did not return success", subcommand)
	}
	return result.Manifest, nil
}

func (p *DockerPlatform) runCompose(ctx context.Context, dsn string, tail ...string) (CommandOutput, error) {
	runtimeEnvFile, err := p.runtimeEnvFile(dsn)
	if err != nil {
		return CommandOutput{}, err
	}
	arguments := []string{"compose", "--project-name", p.config.Project, "--file", p.config.ComposeFile}
	if p.config.BaseEnvFile != "" {
		arguments = append(arguments, "--env-file", p.config.BaseEnvFile)
	}
	arguments = append(arguments, "--env-file", runtimeEnvFile)
	arguments = append(arguments, tail...)
	return p.runner.Run(ctx, "docker", arguments...)
}

func (p *DockerPlatform) runDeploymentCompose(ctx context.Context, deployment Deployment, dsn string, tail ...string) (CommandOutput, error) {
	runtimeEnvFile, err := p.deploymentRuntimeEnvFile(deployment, dsn)
	if err != nil {
		return CommandOutput{}, err
	}
	arguments := []string{"compose", "--project-name", p.config.Project, "--file", p.config.ComposeFile}
	if p.config.BaseEnvFile != "" {
		arguments = append(arguments, "--env-file", p.config.BaseEnvFile)
	}
	arguments = append(arguments, "--env-file", runtimeEnvFile)
	arguments = append(arguments, tail...)
	return p.runner.Run(ctx, "docker", arguments...)
}

func (p *DockerPlatform) runtimeEnvFile(dsn string) (string, error) {
	if !databasePattern.MatchString(dsn) {
		return "", fmt.Errorf("unsupported runtime database DSN %q", dsn)
	}
	attachmentRoot, err := attachmentRootForDSN(dsn)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(p.config.RuntimeEnvDir, stateDirectoryMode); err != nil {
		return "", fmt.Errorf("create lifecycle environment directory: %w", err)
	}
	if err := os.Chmod(p.config.RuntimeEnvDir, stateDirectoryMode); err != nil {
		return "", fmt.Errorf("restrict lifecycle environment directory: %w", err)
	}
	payload := []byte("XBOARD_DATABASE_DSN=" + dsn + "\nXBOARD_ATTACHMENT_ROOT=" + attachmentRoot + "\nXBOARD_GO_IMAGE=" + p.runtimeImageTag() + "\n")
	digest := sha256.Sum256(payload)
	path := filepath.Join(p.config.RuntimeEnvDir, "runtime-"+hex.EncodeToString(digest[:8])+".env")
	if existing, err := os.ReadFile(path); err == nil {
		if bytes.Equal(existing, payload) {
			if err := os.Chmod(path, stateFileMode); err != nil {
				return "", fmt.Errorf("restrict lifecycle environment file: %w", err)
			}
			return path, nil
		}
		return "", errors.New("lifecycle environment file collision")
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("read lifecycle environment file: %w", err)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, stateFileMode)
	if err != nil {
		return "", fmt.Errorf("create lifecycle environment file: %w", err)
	}
	defer file.Close()
	if _, err := file.Write(payload); err != nil {
		return "", fmt.Errorf("write lifecycle environment file: %w", err)
	}
	if err := file.Sync(); err != nil {
		return "", fmt.Errorf("sync lifecycle environment file: %w", err)
	}
	return path, nil
}

func (p *DockerPlatform) deploymentRuntimeEnvFile(deployment Deployment, dsn string) (string, error) {
	if err := validateDeployment(deployment); err != nil {
		return "", err
	}
	if !databasePattern.MatchString(dsn) {
		return "", fmt.Errorf("unsupported runtime database DSN %q", dsn)
	}
	attachmentRoot, err := attachmentRootForDSN(dsn)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(p.config.RuntimeEnvDir, stateDirectoryMode); err != nil {
		return "", fmt.Errorf("create lifecycle environment directory: %w", err)
	}
	if err := os.Chmod(p.config.RuntimeEnvDir, stateDirectoryMode); err != nil {
		return "", fmt.Errorf("restrict lifecycle environment directory: %w", err)
	}
	payload := []byte(strings.Join([]string{
		"XBOARD_DATABASE_DSN=" + dsn,
		"XBOARD_ATTACHMENT_ROOT=" + attachmentRoot,
		"XBOARD_GO_GATEWAY_IMAGE=" + p.deploymentRuntimeImageTag(ComponentGateway),
		"XBOARD_GO_FRONTEND_IMAGE=" + p.deploymentRuntimeImageTag(ComponentFrontend),
		"XBOARD_GO_BACKEND_IMAGE=" + p.deploymentRuntimeImageTag(ComponentBackend),
	}, "\n") + "\n")
	digest := sha256.Sum256(payload)
	path := filepath.Join(p.config.RuntimeEnvDir, "deployment-"+hex.EncodeToString(digest[:8])+".env")
	if existing, readErr := os.ReadFile(path); readErr == nil {
		if !bytes.Equal(existing, payload) {
			return "", errors.New("deployment lifecycle environment file collision")
		}
		if err := os.Chmod(path, stateFileMode); err != nil {
			return "", fmt.Errorf("restrict deployment lifecycle environment file: %w", err)
		}
		return path, nil
	} else if !errors.Is(readErr, os.ErrNotExist) {
		return "", fmt.Errorf("read deployment lifecycle environment file: %w", readErr)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, stateFileMode)
	if err != nil {
		return "", fmt.Errorf("create deployment lifecycle environment file: %w", err)
	}
	defer file.Close()
	if _, err := file.Write(payload); err != nil {
		return "", fmt.Errorf("write deployment lifecycle environment file: %w", err)
	}
	if err := file.Sync(); err != nil {
		return "", fmt.Errorf("sync deployment lifecycle environment file: %w", err)
	}
	return path, nil
}

func (p *DockerPlatform) containerName() string {
	return p.config.Project + "-xboard-go-1"
}

func (p *DockerPlatform) deploymentContainerName(component Component) string {
	return p.config.Project + "-" + string(component) + "-1"
}

func (p *DockerPlatform) deploymentRuntimeImageTag(component Component) string {
	return "xboard-go-" + string(component) + ":lifecycle-" + p.config.Project
}

func (p *DockerPlatform) runtimeImageTag() string {
	return runtimeImageTagPrefix + p.config.Project
}

func validateImageLabels(labels map[string]string) error {
	if labels["org.opencontainers.image.title"] != "Xboard-Go" {
		return errors.New("target image is not labeled as Xboard-Go")
	}
	if labels["org.opencontainers.image.licenses"] != "Apache-2.0" {
		return errors.New("target image does not declare the Apache-2.0 license")
	}
	return nil
}

func commandFailure(stage string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s failed: %w", stage, err)
}
