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
	if string(payload) != "XBOARD_DATABASE_DSN="+defaultDatabaseDSN+"\nXBOARD_GO_IMAGE=xboard-go:lifecycle-lifecycle-test\n" {
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
		!slicesContain(last.arguments, "/var/lib/xboard/xboard-rollback-safe.db") {
		t.Fatalf("restore command = %#v", last)
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
