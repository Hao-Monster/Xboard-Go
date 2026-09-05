package lifecycle

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
)

type Component string

const (
	ComponentGateway  Component = "gateway"
	ComponentFrontend Component = "frontend"
	ComponentBackend  Component = "backend"

	DeploymentManifestVersion  = 1
	maxDeploymentManifestBytes = 64 << 10
)

var immutableImageReferencePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._:/-]*@sha256:[0-9a-f]{64}$`)

type ComponentImage struct {
	Reference string `json:"reference"`
	ID        string `json:"id"`
	Revision  string `json:"revision"`
}

func LoadDeploymentManifest(path string) (DeploymentManifest, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return DeploymentManifest{}, fmt.Errorf("inspect deployment manifest: %w", err)
	}
	if !info.Mode().IsRegular() {
		return DeploymentManifest{}, errors.New("deployment manifest must be a regular file")
	}
	file, err := os.Open(path)
	if err != nil {
		return DeploymentManifest{}, fmt.Errorf("open deployment manifest: %w", err)
	}
	defer file.Close()
	return DecodeDeploymentManifest(file)
}

func DecodeDeploymentManifest(input io.Reader) (DeploymentManifest, error) {
	limited := io.LimitReader(input, maxDeploymentManifestBytes+1)
	payload, err := io.ReadAll(limited)
	if err != nil {
		return DeploymentManifest{}, fmt.Errorf("read deployment manifest: %w", err)
	}
	if len(payload) > maxDeploymentManifestBytes {
		return DeploymentManifest{}, fmt.Errorf("deployment manifest exceeds %d bytes", maxDeploymentManifestBytes)
	}
	if err := rejectDuplicateJSONKeys(payload); err != nil {
		return DeploymentManifest{}, err
	}
	decoder := json.NewDecoder(strings.NewReader(string(payload)))
	decoder.DisallowUnknownFields()
	var manifest DeploymentManifest
	if err := decoder.Decode(&manifest); err != nil {
		return DeploymentManifest{}, fmt.Errorf("decode deployment manifest: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return DeploymentManifest{}, errors.New("deployment manifest contains trailing data")
		}
		return DeploymentManifest{}, fmt.Errorf("decode deployment manifest trailing data: %w", err)
	}
	if err := ValidateDeploymentManifest(manifest); err != nil {
		return DeploymentManifest{}, err
	}
	return manifest, nil
}

func rejectDuplicateJSONKeys(payload []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	var consume func() error
	consume = func() error {
		token, err := decoder.Token()
		if err != nil {
			return err
		}
		delimiter, ok := token.(json.Delim)
		if !ok {
			return nil
		}
		switch delimiter {
		case '{':
			seen := map[string]struct{}{}
			for decoder.More() {
				keyToken, err := decoder.Token()
				if err != nil {
					return err
				}
				key, ok := keyToken.(string)
				if !ok {
					return errors.New("deployment manifest contains a non-string object key")
				}
				if _, exists := seen[key]; exists {
					return fmt.Errorf("deployment manifest contains duplicate key %q", key)
				}
				seen[key] = struct{}{}
				if err := consume(); err != nil {
					return err
				}
			}
		case '[':
			for decoder.More() {
				if err := consume(); err != nil {
					return err
				}
			}
		default:
			return errors.New("deployment manifest contains an unexpected delimiter")
		}
		_, err = decoder.Token()
		return err
	}
	if err := consume(); err != nil {
		return fmt.Errorf("validate deployment manifest JSON: %w", err)
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("deployment manifest contains trailing data")
		}
		return fmt.Errorf("validate deployment manifest trailing data: %w", err)
	}
	return nil
}

type DeploymentManifest struct {
	FormatVersion int            `json:"format_version"`
	Revision      string         `json:"revision"`
	Gateway       ComponentImage `json:"gateway"`
	Frontend      ComponentImage `json:"frontend"`
	Backend       ComponentImage `json:"backend"`
}

type Deployment struct {
	ID             string `json:"id"`
	SourceRevision string `json:"source_revision,omitempty"`
	Gateway        Image  `json:"gateway"`
	Frontend       Image  `json:"frontend"`
	Backend        Image  `json:"backend"`
}

func (deployment Deployment) ChangedComponents(previous Deployment) []Component {
	changed := make([]Component, 0, 3)
	if deployment.Gateway.ID != previous.Gateway.ID {
		changed = append(changed, ComponentGateway)
	}
	if deployment.Frontend.ID != previous.Frontend.ID {
		changed = append(changed, ComponentFrontend)
	}
	if deployment.Backend.ID != previous.Backend.ID {
		changed = append(changed, ComponentBackend)
	}
	return changed
}

func validateDeployment(deployment Deployment) error {
	if !imageIDPattern.MatchString(deployment.ID) {
		return fmt.Errorf("invalid deployment fingerprint %q", deployment.ID)
	}
	if deployment.SourceRevision != "" && !revisionPattern.MatchString(deployment.SourceRevision) {
		return fmt.Errorf("invalid deployment source revision %q", deployment.SourceRevision)
	}
	for _, value := range []struct {
		name  Component
		image Image
	}{
		{name: ComponentGateway, image: deployment.Gateway},
		{name: ComponentFrontend, image: deployment.Frontend},
		{name: ComponentBackend, image: deployment.Backend},
	} {
		if err := validateImage(value.image); err != nil {
			return fmt.Errorf("validate %s image: %w", value.name, err)
		}
	}
	if deployment.ID != deploymentFingerprint(deployment.Gateway, deployment.Frontend, deployment.Backend) {
		return errors.New("deployment fingerprint does not match component images")
	}
	return nil
}

func deploymentFingerprint(gateway, frontend, backend Image) string {
	digest := sha256.Sum256([]byte(strings.Join([]string{
		string(ComponentGateway), gateway.ID, gateway.Revision,
		string(ComponentFrontend), frontend.ID, frontend.Revision,
		string(ComponentBackend), backend.ID, backend.Revision,
	}, "\x00")))
	return "sha256:" + hex.EncodeToString(digest[:])
}

func sameDeploymentImages(first, second Deployment) bool {
	return first.ID == second.ID && first.Gateway == second.Gateway && first.Frontend == second.Frontend && first.Backend == second.Backend
}

func ValidateDeploymentManifest(manifest DeploymentManifest) error {
	if manifest.FormatVersion != DeploymentManifestVersion {
		return fmt.Errorf("unsupported deployment manifest version %d", manifest.FormatVersion)
	}
	if !revisionPattern.MatchString(manifest.Revision) {
		return fmt.Errorf("invalid deployment revision %q", manifest.Revision)
	}
	for _, value := range []struct {
		name  Component
		image ComponentImage
	}{
		{name: ComponentGateway, image: manifest.Gateway},
		{name: ComponentFrontend, image: manifest.Frontend},
		{name: ComponentBackend, image: manifest.Backend},
	} {
		if err := validateComponentImage(value.image); err != nil {
			return fmt.Errorf("validate %s image: %w", value.name, err)
		}
	}
	return nil
}

func validateComponentImage(image ComponentImage) error {
	if !imageIDPattern.MatchString(image.ID) {
		return fmt.Errorf("invalid immutable image ID %q", image.ID)
	}
	if !revisionPattern.MatchString(image.Revision) {
		return fmt.Errorf("invalid image revision %q", image.Revision)
	}
	reference := strings.TrimSpace(image.Reference)
	if !immutableImageReferencePattern.MatchString(reference) {
		return fmt.Errorf("image reference %q must be a normalized exact sha256 reference", reference)
	}
	separator := strings.LastIndex(reference, "@sha256:")
	if separator <= 0 || separator+len("@sha256:")+64 != len(reference) {
		return fmt.Errorf("image reference %q must use an exact sha256 digest", reference)
	}
	digest := reference[separator+len("@sha256:"):]
	if len(digest) != 64 || strings.IndexFunc(digest, func(value rune) bool {
		return (value < '0' || value > '9') && (value < 'a' || value > 'f')
	}) >= 0 {
		return errors.New("image reference has an invalid sha256 digest")
	}
	return nil
}

func (manifest DeploymentManifest) ChangedComponents(previous DeploymentManifest) []Component {
	changed := make([]Component, 0, 3)
	if manifest.Gateway.ID != previous.Gateway.ID {
		changed = append(changed, ComponentGateway)
	}
	if manifest.Frontend.ID != previous.Frontend.ID {
		changed = append(changed, ComponentFrontend)
	}
	if manifest.Backend.ID != previous.Backend.ID {
		changed = append(changed, ComponentBackend)
	}
	return changed
}
