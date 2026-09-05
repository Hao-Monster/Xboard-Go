package lifecycle

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestValidateDeploymentManifestRequiresExactThreeComponentIdentity(t *testing.T) {
	revision := strings.Repeat("a", 40)
	manifest := DeploymentManifest{
		FormatVersion: 1,
		Revision:      revision,
		Gateway:       ComponentImage{Reference: "registry.example/xboard-gateway@sha256:" + strings.Repeat("1", 64), ID: "sha256:" + strings.Repeat("1", 64), Revision: revision},
		Frontend:      ComponentImage{Reference: "registry.example/xboard-frontend@sha256:" + strings.Repeat("2", 64), ID: "sha256:" + strings.Repeat("2", 64), Revision: revision},
		Backend:       ComponentImage{Reference: "registry.example/xboard-backend@sha256:" + strings.Repeat("3", 64), ID: "sha256:" + strings.Repeat("3", 64), Revision: revision},
	}
	if err := ValidateDeploymentManifest(manifest); err != nil {
		t.Fatalf("ValidateDeploymentManifest() error = %v", err)
	}

	tests := []struct {
		name   string
		mutate func(*DeploymentManifest)
	}{
		{name: "format", mutate: func(value *DeploymentManifest) { value.FormatVersion = 2 }},
		{name: "revision", mutate: func(value *DeploymentManifest) { value.Revision = "main" }},
		{name: "missing backend", mutate: func(value *DeploymentManifest) { value.Backend = ComponentImage{} }},
		{name: "mutable reference", mutate: func(value *DeploymentManifest) { value.Frontend.Reference = "registry.example/xboard-frontend:latest" }},
		{name: "option-like reference", mutate: func(value *DeploymentManifest) {
			value.Frontend.Reference = "--format@sha256:" + strings.Repeat("2", 64)
		}},
		{name: "invalid reference digest", mutate: func(value *DeploymentManifest) {
			value.Gateway.Reference = "registry.example/xboard-gateway@sha256:not-a-digest"
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := manifest
			test.mutate(&candidate)
			if err := ValidateDeploymentManifest(candidate); err == nil {
				t.Fatal("ValidateDeploymentManifest() accepted invalid manifest")
			}
		})
	}
}

func TestDecodeDeploymentManifestRejectsUnknownAndTrailingData(t *testing.T) {
	manifest := testDeploymentManifest(strings.Repeat("a", 40), "1", "2", "3")
	payload, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeDeploymentManifest(strings.NewReader(string(payload)))
	if err != nil || decoded != manifest {
		t.Fatalf("DecodeDeploymentManifest() = (%#v, %v)", decoded, err)
	}
	for _, invalid := range []string{
		strings.TrimSuffix(string(payload), "}") + `,"unexpected":true}`,
		strings.Replace(string(payload), `"format_version":1`, `"format_version":1,"format_version":1`, 1),
		string(payload) + `{}`,
		strings.Repeat(" ", maxDeploymentManifestBytes+1),
	} {
		if _, err := DecodeDeploymentManifest(strings.NewReader(invalid)); err == nil {
			t.Fatal("DecodeDeploymentManifest() accepted invalid input")
		}
	}
}

func TestDeploymentManifestChangedComponents(t *testing.T) {
	revisionA := strings.Repeat("a", 40)
	revisionB := strings.Repeat("b", 40)
	base := testDeploymentManifest(revisionA, "1", "2", "3")
	target := base
	target.Revision = revisionB
	target.Frontend = testDeploymentManifest(revisionB, "1", "4", "3").Frontend
	changed := target.ChangedComponents(base)
	if len(changed) != 1 || changed[0] != ComponentFrontend {
		t.Fatalf("ChangedComponents() = %v, want [frontend]", changed)
	}
}

func testDeploymentManifest(revision, gateway, frontend, backend string) DeploymentManifest {
	component := func(name, marker string) ComponentImage {
		digest := strings.Repeat(marker, 64)
		return ComponentImage{Reference: "registry.example/xboard-" + name + "@sha256:" + digest, ID: "sha256:" + digest, Revision: revision}
	}
	return DeploymentManifest{FormatVersion: 1, Revision: revision, Gateway: component("gateway", gateway), Frontend: component("frontend", frontend), Backend: component("backend", backend)}
}
