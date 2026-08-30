package theme

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"math"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	MaxArchiveBytes     = 10 << 20
	maxFiles            = 64
	maxExpandedBytes    = 32 << 20
	maxFileBytes        = 8 << 20
	maxManifestBytes    = 256 << 10
	maxImagePixels      = 20_000_000
	maxCompressionRatio = 100
)

var (
	namePattern       = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)
	paletteKeyPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,32}$`)
	versionPattern    = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)
	hexColorPattern   = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)
)

type Palette struct {
	Background  string `json:"background"`
	Surface     string `json:"surface"`
	Text        string `json:"text"`
	Muted       string `json:"muted"`
	Primary     string `json:"primary"`
	PrimaryText string `json:"primary_text"`
	Border      string `json:"border"`
}

type Config struct {
	ThemeColor    string `json:"theme_color"`
	BackgroundURL string `json:"background_url"`
	FontScale     string `json:"font_scale"`
	Radius        string `json:"radius"`
}

type Manifest struct {
	FormatVersion int                `json:"format_version"`
	Name          string             `json:"name"`
	Description   string             `json:"description"`
	Version       string             `json:"version"`
	Images        []string           `json:"images"`
	Backgrounds   []string           `json:"backgrounds"`
	Palettes      map[string]Palette `json:"palettes"`
	DefaultConfig Config             `json:"default_config"`
}

type Asset struct {
	Path   string
	MIME   string
	SHA256 string
	Width  int
	Height int
	Data   []byte
}

type Package struct {
	Manifest     Manifest
	ManifestJSON []byte
	SHA256       string
	Assets       []Asset
}

func ParseArchive(archive []byte) (Package, error) {
	if len(archive) == 0 || len(archive) > MaxArchiveBytes {
		return Package{}, errors.New("theme archive size is invalid")
	}
	reader, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil {
		return Package{}, errors.New("theme archive is not a valid ZIP file")
	}
	if len(reader.File) == 0 || len(reader.File) > maxFiles {
		return Package{}, errors.New("theme archive contains an invalid number of entries")
	}

	files := make(map[string][]byte, len(reader.File))
	seen := make(map[string]string, len(reader.File))
	var expanded uint64
	for _, archived := range reader.File {
		directory := archived.FileInfo().IsDir()
		rawName := archived.Name
		if directory {
			rawName = strings.TrimSuffix(rawName, "/")
		}
		name, err := validateArchivePath(rawName)
		if err != nil {
			return Package{}, err
		}
		folded := strings.ToLower(name)
		if directory {
			folded += "/"
		}
		if previous, exists := seen[folded]; exists {
			return Package{}, fmt.Errorf("theme archive contains ambiguous paths %q and %q", previous, name)
		}
		seen[folded] = name
		mode := archived.Mode()
		if directory {
			continue
		}
		if !mode.IsRegular() {
			return Package{}, fmt.Errorf("theme archive entry %q is not a regular file", name)
		}
		if archived.UncompressedSize64 > maxFileBytes {
			return Package{}, fmt.Errorf("theme archive entry %q is too large", name)
		}
		if archived.UncompressedSize64 > 0 && (archived.CompressedSize64 == 0 || archived.UncompressedSize64 > archived.CompressedSize64*maxCompressionRatio) {
			return Package{}, fmt.Errorf("theme archive entry %q exceeds the compression ratio limit", name)
		}
		expanded += archived.UncompressedSize64
		if expanded > maxExpandedBytes {
			return Package{}, errors.New("theme archive expands beyond the allowed size")
		}
		stream, err := archived.Open()
		if err != nil {
			return Package{}, fmt.Errorf("open theme archive entry %q: %w", name, err)
		}
		body, readErr := io.ReadAll(io.LimitReader(stream, maxFileBytes+1))
		closeErr := stream.Close()
		if readErr != nil || closeErr != nil {
			return Package{}, fmt.Errorf("read theme archive entry %q", name)
		}
		if len(body) > maxFileBytes || uint64(len(body)) != archived.UncompressedSize64 {
			return Package{}, fmt.Errorf("theme archive entry %q has an invalid declared size", name)
		}
		files[name] = body
	}

	manifestBody, exists := files["manifest.json"]
	if !exists || len(manifestBody) == 0 || len(manifestBody) > maxManifestBytes || !utf8.Valid(manifestBody) {
		return Package{}, errors.New("theme archive must contain a valid manifest.json")
	}
	manifest, err := decodeManifest(manifestBody)
	if err != nil {
		return Package{}, err
	}
	if err := ValidateManifest(manifest); err != nil {
		return Package{}, err
	}

	referenced := make(map[string]struct{}, len(manifest.Images)+len(manifest.Backgrounds))
	for _, reference := range append(append([]string(nil), manifest.Images...), manifest.Backgrounds...) {
		clean, err := validateArchivePath(reference)
		if err != nil || clean == "manifest.json" {
			return Package{}, fmt.Errorf("theme manifest contains invalid asset path %q", reference)
		}
		if _, duplicate := referenced[clean]; duplicate {
			return Package{}, fmt.Errorf("theme manifest contains duplicate asset path %q", clean)
		}
		referenced[clean] = struct{}{}
		if _, present := files[clean]; !present {
			return Package{}, fmt.Errorf("theme manifest references missing asset %q", clean)
		}
	}
	for file := range files {
		if file == "manifest.json" {
			continue
		}
		if _, referencedFile := referenced[file]; !referencedFile {
			return Package{}, fmt.Errorf("theme archive contains unreferenced file %q", file)
		}
	}

	assets := make([]Asset, 0, len(referenced))
	for file := range referenced {
		asset, err := inspectImage(file, files[file])
		if err != nil {
			return Package{}, err
		}
		assets = append(assets, asset)
	}
	sort.Slice(assets, func(i, j int) bool { return assets[i].Path < assets[j].Path })
	canonical, err := json.Marshal(manifest)
	if err != nil {
		return Package{}, fmt.Errorf("encode normalized theme manifest: %w", err)
	}
	digest := sha256.Sum256(archive)
	return Package{Manifest: manifest, ManifestJSON: canonical, SHA256: hex.EncodeToString(digest[:]), Assets: assets}, nil
}

func ValidateManifest(manifest Manifest) error {
	if manifest.FormatVersion != 1 || !namePattern.MatchString(manifest.Name) || strings.EqualFold(manifest.Name, "Xboard") {
		return errors.New("theme manifest has an invalid format version or name")
	}
	if !validDescription(manifest.Description) {
		return errors.New("theme manifest has an invalid description or version")
	}
	if _, err := parseVersion(manifest.Version); err != nil {
		return errors.New("theme manifest has an invalid description or version")
	}
	if len(manifest.Palettes) == 0 || len(manifest.Palettes) > 16 || len(manifest.Images) > 16 || len(manifest.Backgrounds) > 16 {
		return errors.New("theme manifest contains an invalid catalog size")
	}
	for key, palette := range manifest.Palettes {
		if !paletteKeyPattern.MatchString(key) || !validPalette(palette) {
			return fmt.Errorf("theme manifest contains invalid palette %q", key)
		}
	}
	if err := ValidateConfig(manifest, manifest.DefaultConfig); err != nil {
		return fmt.Errorf("theme manifest default config: %w", err)
	}
	return nil
}

func ValidateConfig(manifest Manifest, config Config) error {
	if _, exists := manifest.Palettes[config.ThemeColor]; !exists {
		return errors.New("theme color is not defined by the manifest")
	}
	if config.BackgroundURL != "" && !contains(manifest.Backgrounds, config.BackgroundURL) {
		return errors.New("theme background is not defined by the manifest")
	}
	if config.FontScale != "small" && config.FontScale != "normal" && config.FontScale != "large" {
		return errors.New("theme font scale is invalid")
	}
	if config.Radius != "compact" && config.Radius != "rounded" && config.Radius != "pill" {
		return errors.New("theme radius is invalid")
	}
	return nil
}

func CompareVersions(left, right string) (int, error) {
	leftParts, err := parseVersion(left)
	if err != nil {
		return 0, err
	}
	rightParts, err := parseVersion(right)
	if err != nil {
		return 0, err
	}
	for index := range leftParts {
		if leftParts[index] < rightParts[index] {
			return -1, nil
		}
		if leftParts[index] > rightParts[index] {
			return 1, nil
		}
	}
	return 0, nil
}

func decodeManifest(body []byte) (Manifest, error) {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	var manifest Manifest
	if err := decoder.Decode(&manifest); err != nil {
		return Manifest{}, errors.New("theme manifest JSON is invalid")
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return Manifest{}, errors.New("theme manifest JSON has trailing content")
	}
	return manifest, nil
}

func validateArchivePath(value string) (string, error) {
	if value == "" || !utf8.ValidString(value) || strings.ContainsAny(value, "\\\x00\r\n") || strings.HasPrefix(value, "/") || path.Clean(value) != value {
		return "", fmt.Errorf("theme archive contains invalid path %q", value)
	}
	for _, segment := range strings.Split(value, "/") {
		if segment == "" || segment == "." || segment == ".." || len([]byte(segment)) > 128 {
			return "", fmt.Errorf("theme archive contains invalid path %q", value)
		}
		for _, character := range segment {
			if unicode.IsControl(character) {
				return "", fmt.Errorf("theme archive contains invalid path %q", value)
			}
		}
	}
	if len([]byte(value)) > 512 {
		return "", fmt.Errorf("theme archive contains invalid path %q", value)
	}
	return value, nil
}

func inspectImage(name string, body []byte) (Asset, error) {
	configuration, format, err := image.DecodeConfig(bytes.NewReader(body))
	if err != nil || configuration.Width < 1 || configuration.Height < 1 || configuration.Width > maxImagePixels/configuration.Height {
		return Asset{}, fmt.Errorf("theme asset %q is not a valid bounded image", name)
	}
	var mime string
	extension := strings.ToLower(path.Ext(name))
	switch format {
	case "png":
		mime = "image/png"
		if extension != ".png" {
			return Asset{}, fmt.Errorf("theme asset %q extension does not match its image format", name)
		}
	case "jpeg":
		mime = "image/jpeg"
		if extension != ".jpg" && extension != ".jpeg" {
			return Asset{}, fmt.Errorf("theme asset %q extension does not match its image format", name)
		}
	case "gif":
		mime = "image/gif"
		if extension != ".gif" {
			return Asset{}, fmt.Errorf("theme asset %q extension does not match its image format", name)
		}
	default:
		return Asset{}, fmt.Errorf("theme asset %q uses an unsupported image format", name)
	}
	digest := sha256.Sum256(body)
	return Asset{Path: name, MIME: mime, SHA256: hex.EncodeToString(digest[:]), Width: configuration.Width, Height: configuration.Height, Data: append([]byte(nil), body...)}, nil
}

func validDescription(value string) bool {
	if !utf8.ValidString(value) || len([]byte(value)) > 512 {
		return false
	}
	for _, character := range value {
		if unicode.IsControl(character) && character != '\t' {
			return false
		}
	}
	return true
}

func validPalette(palette Palette) bool {
	colors := []string{palette.Background, palette.Surface, palette.Text, palette.Muted, palette.Primary, palette.PrimaryText, palette.Border}
	for _, value := range colors {
		if !hexColorPattern.MatchString(value) {
			return false
		}
	}
	return contrastRatio(palette.Text, palette.Background) >= 4.5 && contrastRatio(palette.PrimaryText, palette.Primary) >= 4.5
}

func contrastRatio(left, right string) float64 {
	leftLuminance := luminance(left)
	rightLuminance := luminance(right)
	if leftLuminance < rightLuminance {
		leftLuminance, rightLuminance = rightLuminance, leftLuminance
	}
	return (leftLuminance + 0.05) / (rightLuminance + 0.05)
}

func luminance(value string) float64 {
	components := make([]float64, 3)
	for index := range components {
		raw, _ := strconv.ParseUint(value[1+index*2:3+index*2], 16, 8)
		component := float64(raw) / 255
		if component <= 0.04045 {
			components[index] = component / 12.92
		} else {
			components[index] = math.Pow((component+0.055)/1.055, 2.4)
		}
	}
	return 0.2126*components[0] + 0.7152*components[1] + 0.0722*components[2]
}

func parseVersion(value string) ([3]uint64, error) {
	var parsed [3]uint64
	if !versionPattern.MatchString(value) {
		return parsed, errors.New("theme version is invalid")
	}
	for index, part := range strings.Split(value, ".") {
		component, err := strconv.ParseUint(part, 10, 31)
		if err != nil {
			return parsed, errors.New("theme version is invalid")
		}
		parsed[index] = component
	}
	return parsed, nil
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
