package lifecycle

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Hao-Monster/Xboard-Go/internal/backup"
)

func TestDockerPlatformRejectsImagesWithoutTrustedIdentityLabels(t *testing.T) {
	platform, runner := newDockerTestPlatform(t)
	runner.outputs = []runnerResult{{stdout: `[{
        "Id":"sha256:` + strings.Repeat("1", 64) + `",
        "Config":{"Labels":{"org.opencontainers.image.revision":"` + strings.Repeat("a", 40) + `"}}
    }]`}}

	if _, err := platform.ResolveImage(context.Background(), "candidate"); err == nil || !strings.Contains(err.Error(), "not labeled") {
		t.Fatalf("ResolveImage() error = %v", err)
	}
	if !reflect.DeepEqual(runner.calls, []commandCall{{name: "docker", arguments: []string{"image", "inspect", "candidate"}}}) {
		t.Fatalf("calls = %#v", runner.calls)
	}
}

func TestDockerPlatformResolvesExactDeploymentComponent(t *testing.T) {
	platform, runner := newDockerTestPlatform(t)
	revision := strings.Repeat("a", 40)
	digest := strings.Repeat("1", 64)
	component := ComponentImage{
		Reference: "registry.example/xboard-frontend@sha256:" + digest,
		ID:        "sha256:" + digest,
		Revision:  revision,
	}
	runner.outputs = []runnerResult{{stdout: `[{"Id":"sha256:` + digest + `","Config":{"Labels":{` +
		`"org.opencontainers.image.title":"Xboard-Go Frontend",` +
		`"org.opencontainers.image.licenses":"Apache-2.0",` +
		`"org.opencontainers.image.revision":"` + revision + `"}}}]`}}

	image, err := platform.ResolveComponentImage(context.Background(), ComponentFrontend, component)
	if err != nil || image.ID != component.ID || image.Revision != revision {
		t.Fatalf("ResolveComponentImage() = (%#v, %v)", image, err)
	}
	if !reflect.DeepEqual(runner.calls, []commandCall{{name: "docker", arguments: []string{"image", "inspect", component.Reference}}}) {
		t.Fatalf("calls = %#v", runner.calls)
	}
}

func TestDockerPlatformRejectsDeploymentComponentIdentityMismatch(t *testing.T) {
	platform, runner := newDockerTestPlatform(t)
	revision := strings.Repeat("a", 40)
	digest := strings.Repeat("1", 64)
	component := ComponentImage{Reference: "registry.example/xboard-gateway@sha256:" + digest, ID: "sha256:" + digest, Revision: revision}
	runner.outputs = []runnerResult{{stdout: `[{"Id":"sha256:` + strings.Repeat("2", 64) + `","Config":{"Labels":{` +
		`"org.opencontainers.image.title":"Xboard-Go Gateway",` +
		`"org.opencontainers.image.licenses":"Apache-2.0",` +
		`"org.opencontainers.image.revision":"` + revision + `"}}}]`}}

	if _, err := platform.ResolveComponentImage(context.Background(), ComponentGateway, component); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("ResolveComponentImage() error = %v", err)
	}
}

func TestDockerPlatformCurrentDeploymentAllowsIndependentComponentRevisions(t *testing.T) {
	platform, runner := newDockerTestPlatform(t)
	deployment := resolvedTestDeployment(strings.Repeat("a", 40), "1", "2", "3")
	deployment.Frontend.Revision = strings.Repeat("b", 40)
	deployment.ID = deploymentFingerprint(deployment.Gateway, deployment.Frontend, deployment.Backend)
	runner.outputs = []runnerResult{{stdout: deploymentInspectionJSON(t, deployment, defaultDatabaseDSN, "healthy")}}

	application, err := platform.CurrentDeployment(context.Background())
	if err != nil || !application.Healthy || !sameDeploymentImages(application.Deployment, deployment) {
		t.Fatalf("CurrentDeployment() = (%#v, %v)", application, err)
	}
}

func TestDockerPlatformActivatesOnlyChangedFrontendWithExactBoundImages(t *testing.T) {
	platform, runner := newDockerTestPlatform(t)
	deployment := resolvedTestDeployment(strings.Repeat("a", 40), "1", "2", "3")
	runner.outputs = []runnerResult{{}, {}, {}, {}, {stdout: deploymentInspectionJSON(t, deployment, defaultDatabaseDSN, "healthy")}}

	application, err := platform.ActivateDeployment(context.Background(), deployment, defaultDatabaseDSN, []Component{ComponentFrontend})
	if err != nil || !application.Healthy {
		t.Fatalf("ActivateDeployment() = (%#v, %v)", application, err)
	}
	if len(runner.calls) != 5 {
		t.Fatalf("calls = %#v", runner.calls)
	}
	compose := runner.calls[3]
	if !slicesContain(compose.arguments, "--no-deps") || !slicesContain(compose.arguments, "frontend") || slicesContain(compose.arguments, "backend") || slicesContain(compose.arguments, "gateway") {
		t.Fatalf("activation compose call = %#v", compose)
	}
	entries, err := os.ReadDir(platform.config.RuntimeEnvDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("runtime environment entries = %d", len(entries))
	}
	payload, err := os.ReadFile(filepath.Join(platform.config.RuntimeEnvDir, entries[0].Name()))
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"XBOARD_GO_GATEWAY_IMAGE=xboard-go-gateway:lifecycle-lifecycle-test", "XBOARD_GO_FRONTEND_IMAGE=xboard-go-frontend:lifecycle-lifecycle-test", "XBOARD_GO_BACKEND_IMAGE=xboard-go-backend:lifecycle-lifecycle-test"} {
		if !strings.Contains(string(payload), expected) {
			t.Fatalf("runtime environment %q lacks %q", payload, expected)
		}
	}
}

func TestDockerPlatformResolvesAllManifestImages(t *testing.T) {
	platform, runner := newDockerTestPlatform(t)
	revision := strings.Repeat("a", 40)
	manifest := testDeploymentManifest(revision, "1", "2", "3")
	for _, value := range []struct {
		image ComponentImage
		title string
	}{{manifest.Gateway, "Xboard-Go Gateway"}, {manifest.Frontend, "Xboard-Go Frontend"}, {manifest.Backend, "Xboard-Go Backend"}} {
		runner.outputs = append(runner.outputs, runnerResult{stdout: `[{"Id":"` + value.image.ID + `","Config":{"Labels":{` +
			`"org.opencontainers.image.title":"` + value.title + `","org.opencontainers.image.licenses":"Apache-2.0",` +
			`"org.opencontainers.image.revision":"` + value.image.Revision + `"}}}]`})
	}
	deployment, err := platform.ResolveDeployment(context.Background(), manifest)
	if err != nil || deployment.SourceRevision != revision || deployment.ID == "" {
		t.Fatalf("ResolveDeployment() = (%#v, %v)", deployment, err)
	}
}

func TestDockerPlatformDeploymentBackupBindsEveryExactImage(t *testing.T) {
	platform, runner := newDockerTestPlatform(t)
	deployment := resolvedTestDeployment(strings.Repeat("a", 40), "1", "2", "3")
	application := DeploymentApplication{Deployment: deployment, DSN: defaultDatabaseDSN, Healthy: true}
	manifest := testBackupManifest(deployment.Backend.Revision)
	runner.outputs = []runnerResult{{}, {}, {}, {stdout: backupResultJSON(t, "backup.create", manifest)}, {stdout: backupResultJSON(t, "backup.verify", manifest)}}
	got, err := platform.BackupDeployment(context.Background(), application, "/var/lib/xboard-backups/test.xbbackup")
	if err != nil || !reflect.DeepEqual(got, manifest) {
		t.Fatalf("BackupDeployment() = (%#v, %v)", got, err)
	}
	for index, component := range []Component{ComponentGateway, ComponentFrontend, ComponentBackend} {
		if runner.calls[index].name != "docker" || !slicesContain(runner.calls[index].arguments, platform.deploymentRuntimeImageTag(component)) {
			t.Fatalf("tag call %d = %#v", index, runner.calls[index])
		}
	}
}

func TestDockerPlatformDeploymentRestoreRejectsUnrelatedAttachmentPath(t *testing.T) {
	platform, runner := newDockerTestPlatform(t)
	image := Image{ID: "sha256:" + strings.Repeat("3", 64), Revision: strings.Repeat("a", 40)}
	if _, err := platform.RestoreDeployment(context.Background(), image, "/var/lib/xboard-backups/test.xbbackup", "/var/lib/xboard/rollback.db", "/var/lib/xboard/unrelated"); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("RestoreDeployment() error = %v", err)
	}
	if len(runner.calls) != 0 {
		t.Fatalf("commands executed before validation: %#v", runner.calls)
	}
}

func TestDockerPlatformActivateUsesArgumentVectorsAndNonSecretRuntimeEnvironment(t *testing.T) {
	platform, runner := newDockerTestPlatform(t)
	image := Image{ID: "sha256:" + strings.Repeat("2", 64), Revision: strings.Repeat("b", 40)}
	runner.outputs = []runnerResult{
		{}, {}, {},
		{stdout: applicationInspection(t, image, defaultDatabaseDSN, "healthy")},
	}

	application, err := platform.Activate(context.Background(), image, defaultDatabaseDSN)
	if err != nil {
		t.Fatal(err)
	}
	if !application.Healthy || application.Image != image || application.DSN != defaultDatabaseDSN {
		t.Fatalf("application = %#v", application)
	}
	if len(runner.calls) != 4 {
		t.Fatalf("calls = %#v", runner.calls)
	}
	if !reflect.DeepEqual(runner.calls[0], commandCall{name: "docker", arguments: []string{"image", "tag", image.ID, "xboard-go:lifecycle-lifecycle-test"}}) {
		t.Fatalf("tag call = %#v", runner.calls[0])
	}
	for _, call := range runner.calls {
		if call.name != "docker" {
			t.Fatalf("unexpected executable: %q", call.name)
		}
		for _, argument := range call.arguments {
			if strings.Contains(argument, "password") || strings.Contains(argument, "secret") {
				t.Fatalf("sensitive argument in %#v", call)
			}
		}
	}
	entries, err := os.ReadDir(platform.config.RuntimeEnvDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("runtime environment entries = %d", len(entries))
	}
	payload, err := os.ReadFile(filepath.Join(platform.config.RuntimeEnvDir, entries[0].Name()))
	if err != nil {
		t.Fatal(err)
	}
	if string(payload) != "XBOARD_DATABASE_DSN="+defaultDatabaseDSN+"\nXBOARD_ATTACHMENT_ROOT=/var/lib/xboard/knowledge-attachments\nXBOARD_GO_IMAGE=xboard-go:lifecycle-lifecycle-test\n" {
		t.Fatalf("runtime environment = %q", payload)
	}
}

func TestDockerRuntimeImageTagsAreProjectScoped(t *testing.T) {
	first, _ := newDockerTestPlatform(t)
	directory := t.TempDir()
	composeFile := filepath.Join(directory, "compose.yaml")
	if err := os.WriteFile(composeFile, []byte("services: {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	second, err := NewDockerPlatform(DockerConfig{
		Project: "another-project", ComposeFile: composeFile,
		RuntimeEnvDir: filepath.Join(directory, "runtime"),
	}, &queueRunner{})
	if err != nil {
		t.Fatal(err)
	}

	if first.runtimeImageTag() == second.runtimeImageTag() {
		t.Fatalf("projects share runtime image tag %q", first.runtimeImageTag())
	}
}

func TestDockerPlatformBackupRequiresMatchingCreateAndVerifyManifests(t *testing.T) {
	platform, runner := newDockerTestPlatform(t)
	image := Image{ID: "sha256:" + strings.Repeat("3", 64), Revision: strings.Repeat("c", 40)}
	application := healthyApplication(image, defaultDatabaseDSN)
	manifest := backup.Manifest{
		FormatVersion: 1, CreatedAt: time.Date(2026, 8, 25, 8, 0, 0, 0, time.UTC),
		AppRevision: image.Revision, SchemaVersion: 23, DatabaseSize: 1024,
		DatabaseSHA256: strings.Repeat("d", 64),
	}
	created := backupResultJSON(t, "backup.create", manifest)
	verified := manifest
	verified.DatabaseSHA256 = strings.Repeat("e", 64)
	runner.outputs = []runnerResult{{}, {stdout: created}, {stdout: backupResultJSON(t, "backup.verify", verified)}}

	if _, err := platform.Backup(context.Background(), application, "/var/lib/xboard-backups/test.xbbackup"); err == nil || !strings.Contains(err.Error(), "manifests differ") {
		t.Fatalf("Backup() error = %v", err)
	}
}

func TestDockerPlatformFreshRequiresBothContainerAndDataVolumeToBeAbsent(t *testing.T) {
	tests := []struct {
		name      string
		container string
		volume    string
		want      bool
	}{
		{name: "empty", want: true},
		{name: "container remains", container: "container-id\n"},
		{name: "data volume remains", volume: "volume-name\n"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			platform, runner := newDockerTestPlatform(t)
			runner.outputs = []runnerResult{{stdout: test.container}, {stdout: test.volume}}

			fresh, err := platform.Fresh(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if fresh != test.want {
				t.Fatalf("Fresh() = %v, want %v", fresh, test.want)
			}
			if len(runner.calls) != 2 || !slicesContain(runner.calls[0].arguments, "label=com.docker.compose.service=xboard-go") ||
				!slicesContain(runner.calls[1].arguments, "label=com.docker.compose.volume=xboard-go-data") {
				t.Fatalf("Fresh() calls = %#v", runner.calls)
			}
		})
	}
}

func TestDockerPlatformRestoreBindsTheExactImageAndUsesArgumentVectors(t *testing.T) {
	platform, runner := newDockerTestPlatform(t)
	image := Image{ID: "sha256:" + strings.Repeat("4", 64), Revision: strings.Repeat("e", 40)}
	manifest := testBackupManifest(image.Revision)
	runner.outputs = []runnerResult{{}, {stdout: backupResultJSON(t, "backup.restore", manifest)}}

	restored, err := platform.Restore(
		context.Background(), image,
		"/var/lib/xboard-backups/pre-upgrade.xbbackup",
		"/var/lib/xboard/xboard-rollback-safe.db",
		"/var/lib/xboard/xboard-rollback-safe-attachments",
	)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(restored, manifest) {
		t.Fatalf("Restore() manifest = %#v, want %#v", restored, manifest)
	}
	if !reflect.DeepEqual(runner.calls[0], commandCall{
		name: "docker", arguments: []string{"image", "tag", image.ID, "xboard-go:lifecycle-lifecycle-test"},
	}) {
		t.Fatalf("restore image binding = %#v", runner.calls[0])
	}
	last := runner.calls[1]
	if last.name != "docker" || !slicesContain(last.arguments, "restore") ||
		!slicesContain(last.arguments, "/var/lib/xboard-backups/pre-upgrade.xbbackup") ||
		!slicesContain(last.arguments, "/var/lib/xboard/xboard-rollback-safe.db") ||
		!slicesContain(last.arguments, "--attachment-output") ||
		!slicesContain(last.arguments, "/var/lib/xboard/xboard-rollback-safe-attachments") {
		t.Fatalf("restore command = %#v", last)
	}
}

func TestDockerPlatformRestoreRejectsAnUnrelatedAttachmentPath(t *testing.T) {
	platform, runner := newDockerTestPlatform(t)
	image := Image{ID: "sha256:" + strings.Repeat("4", 64), Revision: strings.Repeat("e", 40)}
	if _, err := platform.Restore(context.Background(), image,
		"/var/lib/xboard-backups/pre-upgrade.xbbackup",
		"/var/lib/xboard/xboard-rollback-safe.db",
		"/var/lib/xboard/unrelated-attachments"); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("Restore() error = %v", err)
	}
	if len(runner.calls) != 0 {
		t.Fatalf("Restore() executed commands before path validation: %#v", runner.calls)
	}
}

func TestDockerPlatformRejectsAnEmptyRuntimeEnvironmentDirectory(t *testing.T) {
	directory := t.TempDir()
	composeFile := filepath.Join(directory, "compose.yaml")
	if err := os.WriteFile(composeFile, []byte("services: {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewDockerPlatform(DockerConfig{
		Project: "lifecycle-test", ComposeFile: composeFile,
	}, &queueRunner{}); err == nil || !strings.Contains(err.Error(), "environment directory") {
		t.Fatalf("NewDockerPlatform() error = %v", err)
	}
}

func newDockerTestPlatform(t *testing.T) (*DockerPlatform, *queueRunner) {
	t.Helper()
	directory := t.TempDir()
	composeFile := filepath.Join(directory, "compose.yaml")
	if err := os.WriteFile(composeFile, []byte("services: {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runner := &queueRunner{}
	platform, err := NewDockerPlatform(DockerConfig{
		Project: "lifecycle-test", ComposeFile: composeFile,
		RuntimeEnvDir: filepath.Join(directory, "runtime"), HealthTimeout: time.Second, PollInterval: time.Millisecond,
	}, runner)
	if err != nil {
		t.Fatal(err)
	}
	return platform, runner
}

func applicationInspection(t *testing.T, image Image, dsn, health string) string {
	t.Helper()
	payload := []map[string]any{{
		"Image": image.ID,
		"Config": map[string]any{
			"Env": []string{"XBOARD_DATABASE_DSN=" + dsn},
			"Labels": map[string]string{
				"org.opencontainers.image.title": "Xboard-Go", "org.opencontainers.image.licenses": "Apache-2.0",
				"org.opencontainers.image.revision": image.Revision,
			},
		},
		"State": map[string]any{"Status": "running", "Health": map[string]string{"Status": health}},
	}}
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

func deploymentInspectionJSON(t *testing.T, deployment Deployment, dsn, health string) string {
	t.Helper()
	records := make([]map[string]any, 0, 3)
	for _, value := range []struct {
		component Component
		image     Image
		title     string
	}{
		{ComponentGateway, deployment.Gateway, "Xboard-Go Gateway"},
		{ComponentFrontend, deployment.Frontend, "Xboard-Go Frontend"},
		{ComponentBackend, deployment.Backend, "Xboard-Go Backend"},
	} {
		environment := []string{}
		if value.component == ComponentBackend {
			environment = append(environment, "XBOARD_DATABASE_DSN="+dsn)
		}
		records = append(records, map[string]any{
			"Image": value.image.ID,
			"Config": map[string]any{"Env": environment, "Labels": map[string]string{
				"org.opencontainers.image.title": value.title, "org.opencontainers.image.licenses": "Apache-2.0", "org.opencontainers.image.revision": value.image.Revision,
			}},
			"State": map[string]any{"Status": "running", "Health": map[string]string{"Status": health}},
		})
	}
	payload, err := json.Marshal(records)
	if err != nil {
		t.Fatal(err)
	}
	return string(payload)
}

func backupResultJSON(t *testing.T, action string, manifest backup.Manifest) string {
	t.Helper()
	encoded, err := json.Marshal(map[string]any{"status": "success", "action": action, "manifest": manifest})
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

type commandCall struct {
	name      string
	arguments []string
}

type runnerResult struct {
	stdout string
	stderr string
	err    error
}

type queueRunner struct {
	outputs []runnerResult
	calls   []commandCall
}

func slicesContain(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func (r *queueRunner) Run(_ context.Context, name string, arguments ...string) (CommandOutput, error) {
	r.calls = append(r.calls, commandCall{name: name, arguments: append([]string(nil), arguments...)})
	if len(r.outputs) == 0 {
		return CommandOutput{}, errors.New("unexpected command")
	}
	result := r.outputs[0]
	r.outputs = r.outputs[1:]
	return CommandOutput{Stdout: result.stdout, Stderr: result.stderr}, result.err
}
