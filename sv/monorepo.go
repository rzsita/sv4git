package sv

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Masterminds/semver/v3"
	"gopkg.in/yaml.v3"
)

// MonorepoComponent is a versioned component discovered in a monorepo.
type MonorepoComponent struct {
	Name               string          // Directory name of the component
	RootPath           string          // Absolute path to the component root directory
	VersioningFilePath string          // Absolute path to the versioning file
	CurrentVersion     *semver.Version // Version read from the file
}

// MonorepoProcessor discovers components and manages their file-based versions.
type MonorepoProcessor interface {
	FindComponents(repoRoot string, cfg MonorepoConfig) ([]MonorepoComponent, error)
	NextVersion(component MonorepoComponent, commits []GitCommitLog, semverProc SemVerCommitsProcessor) (*semver.Version, bool)
	UpdateVersion(component MonorepoComponent, version semver.Version, cfg MonorepoConfig) error
}

// MonorepoProcessorImpl is the default MonorepoProcessor.
type MonorepoProcessorImpl struct{}

// NewMonorepoProcessor MonorepoProcessorImpl constructor.
func NewMonorepoProcessor() *MonorepoProcessorImpl {
	return &MonorepoProcessorImpl{}
}

// FindComponents globs for versioning files and reads each component's current version.
// The glob pattern in cfg.VersioningFile is relative to repoRoot.
func (p MonorepoProcessorImpl) FindComponents(repoRoot string, cfg MonorepoConfig) ([]MonorepoComponent, error) {
	if cfg.VersioningFile == "" {
		return nil, fmt.Errorf("monorepo.versioning-file is not configured")
	}

	pattern := filepath.Join(repoRoot, cfg.VersioningFile)
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return nil, fmt.Errorf("invalid versioning-file glob %q: %v", cfg.VersioningFile, err)
	}
	if len(matches) == 0 {
		return nil, fmt.Errorf("no files matched versioning-file pattern %q", cfg.VersioningFile)
	}

	components := make([]MonorepoComponent, 0, len(matches))
	for _, matchPath := range matches {
		version, err := readVersionFromFile(matchPath, cfg.Path)
		if err != nil {
			return nil, fmt.Errorf("reading version from %s: %v", matchPath, err)
		}
		dir := filepath.Dir(matchPath)
		components = append(components, MonorepoComponent{
			Name:               filepath.Base(dir),
			RootPath:           dir,
			VersioningFilePath: matchPath,
			CurrentVersion:     version,
		})
	}
	return components, nil
}

// NextVersion delegates to the existing SemVerCommitsProcessor.
func (p MonorepoProcessorImpl) NextVersion(component MonorepoComponent, commits []GitCommitLog, semverProc SemVerCommitsProcessor) (*semver.Version, bool) {
	return semverProc.NextVersion(component.CurrentVersion, commits)
}

// UpdateVersion writes the new version string into the component's versioning file.
func (p MonorepoProcessorImpl) UpdateVersion(component MonorepoComponent, version semver.Version, cfg MonorepoConfig) error {
	return writeVersionToFile(component.VersioningFilePath, cfg.Path, version.Original())
}

// ---- file I/O helpers ----

// ReadVersionFromBytes parses version from raw file content using the given dotPath.
// filePath is used only for format detection (YAML vs JSON) based on extension.
func ReadVersionFromBytes(filePath string, content []byte, dotPath string) (*semver.Version, error) {
	data, err := parseFileContent(filePath, content)
	if err != nil {
		return nil, err
	}
	segments, err := parsePath(dotPath)
	if err != nil {
		return nil, fmt.Errorf("invalid path %q: %v", dotPath, err)
	}
	raw, err := getByPath(data, segments)
	if err != nil {
		return nil, fmt.Errorf("path %q: %v", dotPath, err)
	}
	vstr, ok := raw.(string)
	if !ok {
		return nil, fmt.Errorf("path %q: value is not a string", dotPath)
	}
	v, err := ToVersion(vstr)
	if err != nil {
		return nil, fmt.Errorf("path %q: invalid semver %q: %v", dotPath, vstr, err)
	}
	return v, nil
}

func readVersionFromFile(filePath, dotPath string) (*semver.Version, error) {
	content, err := os.ReadFile(filePath)
	if err != nil {
		return nil, err
	}
	return ReadVersionFromBytes(filePath, content, dotPath)
}

func writeVersionToFile(filePath, dotPath, version string) error {
	content, err := os.ReadFile(filePath)
	if err != nil {
		return err
	}
	data, err := parseFileContent(filePath, content)
	if err != nil {
		return err
	}
	segments, err := parsePath(dotPath)
	if err != nil {
		return fmt.Errorf("invalid path %q: %v", dotPath, err)
	}
	if err := setByPath(data, segments, version); err != nil {
		return fmt.Errorf("path %q: %v", dotPath, err)
	}
	return marshalToFile(filePath, data)
}

func parseFileContent(filePath string, content []byte) (map[string]interface{}, error) {
	var data map[string]interface{}
	switch strings.ToLower(filepath.Ext(filePath)) {
	case ".json":
		if err := json.Unmarshal(content, &data); err != nil {
			return nil, fmt.Errorf("parse JSON: %v", err)
		}
	default: // .yml, .yaml treated as YAML
		if err := yaml.Unmarshal(content, &data); err != nil {
			return nil, fmt.Errorf("parse YAML: %v", err)
		}
	}
	return data, nil
}

func marshalToFile(filePath string, data map[string]interface{}) error {
	var (
		out []byte
		err error
	)
	switch strings.ToLower(filepath.Ext(filePath)) {
	case ".json":
		out, err = json.MarshalIndent(data, "", "  ")
		if err != nil {
			return fmt.Errorf("marshal JSON: %v", err)
		}
		out = append(out, '\n')
	default:
		out, err = yaml.Marshal(data)
		if err != nil {
			return fmt.Errorf("marshal YAML: %v", err)
		}
	}
	return os.WriteFile(filePath, out, 0600)
}

// ---- path parsing and navigation ----

// parsePath parses a jq/yq-style path expression into key segments.
//
// Supported formats:
//
//	metadata.version                                  → ["metadata", "version"]
//	.metadata.version                                 → ["metadata", "version"]  (leading dot optional)
//	.metadata.annotations["backstage.io/my-key"]     → ["metadata", "annotations", "backstage.io/my-key"]
//	metadata["key.with.dots"].nested                  → ["metadata", "key.with.dots", "nested"]
//
// Inside bracket notation ["..."] or ['...'] the content is treated as a literal
// key name, allowing dots and other special characters.
func parsePath(path string) ([]string, error) {
	if path == "" {
		return nil, fmt.Errorf("empty path")
	}

	var segments []string
	var current strings.Builder
	i := 0

	// Strip optional leading dot (jq style).
	if path[0] == '.' {
		i = 1
	}

	for i < len(path) {
		switch path[i] {
		case '.':
			if current.Len() > 0 {
				segments = append(segments, current.String())
				current.Reset()
			}
			i++

		case '[':
			if current.Len() > 0 {
				segments = append(segments, current.String())
				current.Reset()
			}
			i++ // skip '['
			if i >= len(path) {
				return nil, fmt.Errorf("unexpected end of path after '['")
			}
			quote := path[i]
			if quote != '"' && quote != '\'' {
				return nil, fmt.Errorf("expected quote character after '[', got %q", string(quote))
			}
			i++ // skip opening quote
			for i < len(path) && path[i] != quote {
				current.WriteByte(path[i])
				i++
			}
			if i >= len(path) {
				return nil, fmt.Errorf("unclosed string in bracket notation")
			}
			i++ // skip closing quote
			if i >= len(path) || path[i] != ']' {
				return nil, fmt.Errorf("expected ']' to close bracket notation")
			}
			i++ // skip ']'
			segments = append(segments, current.String())
			current.Reset()
			// Skip optional trailing dot after ']'.
			if i < len(path) && path[i] == '.' {
				i++
			}

		default:
			current.WriteByte(path[i])
			i++
		}
	}

	if current.Len() > 0 {
		segments = append(segments, current.String())
	}
	if len(segments) == 0 {
		return nil, fmt.Errorf("path %q contains no segments", path)
	}
	return segments, nil
}

// getByPath navigates a nested map[string]interface{} using pre-parsed key segments.
func getByPath(data map[string]interface{}, segments []string) (interface{}, error) {
	if len(segments) == 0 {
		return nil, fmt.Errorf("empty path")
	}
	val, ok := data[segments[0]]
	if !ok {
		return nil, fmt.Errorf("key %q not found", segments[0])
	}
	if len(segments) == 1 {
		return val, nil
	}
	nested, ok := val.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("value at %q is not a map", segments[0])
	}
	return getByPath(nested, segments[1:])
}

// setByPath sets a value in a nested map[string]interface{} using dot-separated segments.
func setByPath(data map[string]interface{}, segments []string, value string) error {
	if len(segments) == 0 {
		return fmt.Errorf("empty path")
	}
	if len(segments) == 1 {
		if _, ok := data[segments[0]]; !ok {
			return fmt.Errorf("key %q not found", segments[0])
		}
		data[segments[0]] = value
		return nil
	}
	val, ok := data[segments[0]]
	if !ok {
		return fmt.Errorf("key %q not found", segments[0])
	}
	nested, ok := val.(map[string]interface{})
	if !ok {
		return fmt.Errorf("value at %q is not a map", segments[0])
	}
	return setByPath(nested, segments[1:], value)
}
