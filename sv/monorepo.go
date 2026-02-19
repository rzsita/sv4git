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

func readVersionFromFile(filePath, dotPath string) (*semver.Version, error) {
	content, err := os.ReadFile(filePath)
	if err != nil {
		return nil, err
	}
	data, err := parseFileContent(filePath, content)
	if err != nil {
		return nil, err
	}
	raw, err := getByPath(data, strings.Split(dotPath, "."))
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

func writeVersionToFile(filePath, dotPath, version string) error {
	content, err := os.ReadFile(filePath)
	if err != nil {
		return err
	}
	data, err := parseFileContent(filePath, content)
	if err != nil {
		return err
	}
	if err := setByPath(data, strings.Split(dotPath, "."), version); err != nil {
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
	return os.WriteFile(filePath, out, 0644)
}

// ---- dot-path navigation ----

// getByPath navigates a nested map[string]interface{} using dot-separated segments.
// It uses greedy longest-prefix matching so keys that themselves contain dots
// (e.g. "backstage.io/template-version") resolve correctly without escaping.
//
// Example: segments ["metadata", "annotations", "backstage", "io/template-version"]
// will try key "metadata.annotations.backstage.io/template-version" first, then
// progressively shorter prefixes until "metadata" matches and recursion continues.
func getByPath(data map[string]interface{}, segments []string) (interface{}, error) {
	if len(segments) == 0 {
		return nil, fmt.Errorf("empty path")
	}
	for i := len(segments); i > 0; i-- {
		key := strings.Join(segments[:i], ".")
		val, ok := data[key]
		if !ok {
			continue
		}
		if i == len(segments) {
			// All segments consumed — this is the target value.
			return val, nil
		}
		nested, ok := val.(map[string]interface{})
		if !ok {
			return nil, fmt.Errorf("value at %q is not a map", key)
		}
		return getByPath(nested, segments[i:])
	}
	return nil, fmt.Errorf("key %q not found", strings.Join(segments, "."))
}

// setByPath sets a value in a nested map[string]interface{} using the same greedy strategy.
func setByPath(data map[string]interface{}, segments []string, value string) error {
	if len(segments) == 0 {
		return fmt.Errorf("empty path")
	}
	for i := len(segments); i > 0; i-- {
		key := strings.Join(segments[:i], ".")
		val, ok := data[key]
		if !ok {
			continue
		}
		if i == len(segments) {
			data[key] = value
			return nil
		}
		nested, ok := val.(map[string]interface{})
		if !ok {
			return fmt.Errorf("value at %q is not a map", key)
		}
		return setByPath(nested, segments[i:], value)
	}
	return fmt.Errorf("key %q not found", strings.Join(segments, "."))
}
