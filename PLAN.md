# Plan: Monorepo Versioning Support

## Goal

Add a `monorepo` config section to `.sv4git.yml` that enables per-component semantic versioning within a monorepo. Each component's current version is stored in a YAML/JSON file (discovered via a glob pattern). Version bumps are determined by commits touching that component's directory tree, using the existing `SemVerCommitsProcessor` logic.

### Config shape (user-facing)

```yaml
monorepo:
  versioning-file: templates/*/template.yml   # glob relative to repo root
  path: metadata.annotations.backstage.io/template-version  # dot-separated key path into the file
```

### New CLI commands

| Command | Alias | Description |
|---|---|---|
| `monorepo-next-version` | `mnv` | Print next version for each changed component |
| `monorepo-tag` | `mtg` | Update version files for all changed components |

---

## Design Decisions

### 1. Glob pattern support

`filepath.Glob` only supports single `*` (not `**`). The example pattern `templates/*/template.yml` uses only a single `*`, so `filepath.Glob` covers the stated requirement. Deep double-star patterns can be documented as a future improvement.

### 2. Dot-path navigation for YAML/JSON keys

The `path` value uses `.` as separator. YAML keys may themselves contain `.` (e.g., `backstage.io/template-version`). A **greedy longest-prefix** algorithm resolves ambiguity:

When navigating `annotations` with remaining segments `["backstage", "io/template-version"]`:
- Try `i=2`: key = `"backstage.io/template-version"` → found → return value ✓
- Try `i=1`: key = `"backstage"` → only if above fails

This correctly handles Backstage-style YAML keys that contain dots without any escaping.

### 3. Determining "commits since last version bump"

There are no per-component git tags. Instead, use the last commit that modified the versioning file itself as the baseline:

```
git log <last-commit-that-touched-versioning-file>..HEAD -- <component-dir>/
```

- Run `git log --format=%H -n 1 -- <versioning-file>` to find that baseline commit.
- Then `git log <hash>..HEAD -- <component-dir>/` for unreleased commits.
- If no baseline exists (first run), fall back to all commits touching the directory.

### 4. File reading/writing strategy

Parse YAML/JSON into `map[string]interface{}`, navigate with the path algorithm, mutate, then marshal back. This is simple and correct; it will lose YAML comments in the versioning file (acceptable trade-off for v1).

### 5. No changes to existing commands

The `monorepo` feature is purely additive. Existing `tag`, `next-version`, etc. are unchanged.

---

## Files Changed

### Modified files

| File | Change |
|---|---|
| `sv/config.go` | Add `MonorepoConfig` struct |
| `sv/git.go` | Add `paths []string` to `LogRange`; add `NewLogRangeWithPaths()`; extend `Log()` to append `-- <paths>`; add `LastCommitForFile()` to `Git` interface and `GitImpl` |
| `cmd/git-sv/config.go` | Add `Monorepo sv.MonorepoConfig \`yaml:"monorepo"\`` field to `Config` |
| `cmd/git-sv/handlers.go` | Add `monorepoNextVersionHandler()` and `monorepoTagHandler()` |
| `cmd/git-sv/main.go` | Instantiate `sv.NewMonorepoProcessor()`; register two new commands |

### New files

| File | Purpose |
|---|---|
| `sv/monorepo.go` | `MonorepoComponent`, `MonorepoProcessor` interface, `MonorepoProcessorImpl`, file I/O helpers, path navigator |
| `sv/monorepo_test.go` | Unit tests for path navigation, file reading/writing, component discovery |

---

## Detailed Implementation Steps

### Step 1 — `sv/config.go`: Add `MonorepoConfig`

Add at the bottom of the file (after the existing `ReleaseNotesSectionConfig` constants):

```go
// MonorepoConfig monorepo versioning preferences.
type MonorepoConfig struct {
    VersioningFile string `yaml:"versioning-file"`
    Path           string `yaml:"path"`
}
```

---

### Step 2 — `sv/git.go`: Path-filtered log and `LastCommitForFile`

**2a. Add `paths` to `LogRange` and a new constructor:**

```go
// LogRange git log range.
type LogRange struct {
    rangeType LogRangeType
    start     string
    end       string
    paths     []string // optional: filter commits by these file paths
}

// NewLogRangeWithPaths LogRange constructor with path filtering.
func NewLogRangeWithPaths(t LogRangeType, start, end string, paths []string) LogRange {
    return LogRange{rangeType: t, start: start, end: end, paths: paths}
}
```

`NewLogRange` is unchanged — backward compatible.

**2b. Extend `Log()` to append the path separator:**

At the end of `Log()`, just before `cmd := exec.Command("git", params...)`, add:

```go
if len(lr.paths) > 0 {
    params = append(params, "--")
    params = append(params, lr.paths...)
}
```

**2c. Add `LastCommitForFile` to the `Git` interface:**

```go
type Git interface {
    LastTag() string
    Log(lr LogRange) ([]GitCommitLog, error)
    Commit(header, body, footer string) error
    Tag(version semver.Version) (string, error)
    Tags() ([]GitTag, error)
    Branch() string
    IsDetached() (bool, error)
    LastCommitForFile(filePath string) (string, error)  // NEW
}
```

**2d. Implement `LastCommitForFile` on `GitImpl`:**

```go
// LastCommitForFile returns the hash of the most recent commit touching filePath.
// Returns empty string (not an error) if the file has never been committed.
func (GitImpl) LastCommitForFile(filePath string) (string, error) {
    cmd := exec.Command("git", "log", "--format=%H", "-n", "1", "--", filePath)
    out, err := cmd.CombinedOutput()
    if err != nil {
        return "", combinedOutputErr(err, out)
    }
    return strings.TrimSpace(string(out)), nil
}
```

---

### Step 3 — `cmd/git-sv/config.go`: Add `Monorepo` to `Config`

```go
type Config struct {
    Version       string                 `yaml:"version"`
    Versioning    sv.VersioningConfig    `yaml:"versioning"`
    Tag           sv.TagConfig           `yaml:"tag"`
    ReleaseNotes  sv.ReleaseNotesConfig  `yaml:"release-notes"`
    Branches      sv.BranchesConfig      `yaml:"branches"`
    CommitMessage sv.CommitMessageConfig `yaml:"commit-message"`
    Monorepo      sv.MonorepoConfig      `yaml:"monorepo"`   // NEW
}
```

No change to `defaultConfig()` — zero value of `MonorepoConfig` means disabled (empty `VersioningFile`).

---

### Step 4 — `sv/monorepo.go`: New file

```go
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
    CurrentVersion     *semver.Version // Version read from the file; nil if unreadable
}

// MonorepoProcessor discovers components and manages their versions.
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

// UpdateVersion writes the new version into the component's versioning file.
func (p MonorepoProcessorImpl) UpdateVersion(component MonorepoComponent, version semver.Version, cfg MonorepoConfig) error {
    return writeVersionToFile(component.VersioningFilePath, cfg.Path, version.Original())
}

// ---- internal helpers ----

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
    var out []byte
    var err error
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

// getByPath navigates a nested map[string]interface{} using dot-separated segments.
// Uses greedy longest-prefix matching so keys containing dots (e.g. "backstage.io/key")
// resolve correctly without any escaping.
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
            return val, nil
        }
        nested, ok := val.(map[string]interface{})
        if !ok {
            return nil, fmt.Errorf("value at %q is not a map", key)
        }
        return getByPath(nested, segments[i:])
    }
    return nil, fmt.Errorf("key not found")
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
    return fmt.Errorf("key not found")
}
```

---

### Step 5 — `cmd/git-sv/handlers.go`: Two new handlers

Both handlers share the same core logic: for each component, find the baseline commit (last time its versioning file was touched), fetch commits since then that affected its directory, and compute the next version using the existing `SemVerCommitsProcessor`.

```go
func monorepoNextVersionHandler(
    git sv.Git,
    semverProcessor sv.SemVerCommitsProcessor,
    monorepoProcessor sv.MonorepoProcessor,
    cfg Config,
    repoPath string,
) func(c *cli.Context) error {
    return func(c *cli.Context) error {
        components, err := monorepoProcessor.FindComponents(repoPath, cfg.Monorepo)
        if err != nil {
            return fmt.Errorf("error finding monorepo components: %v", err)
        }

        for _, component := range components {
            commits, err := componentCommits(git, repoPath, component)
            if err != nil {
                return fmt.Errorf("error getting commits for %s: %v", component.Name, err)
            }

            nextVer, updated := monorepoProcessor.NextVersion(component, commits, semverProcessor)
            if !updated {
                nextVer = component.CurrentVersion
            }
            fmt.Printf("%s: %s\n", component.Name, nextVer.String())
        }
        return nil
    }
}

func monorepoTagHandler(
    git sv.Git,
    semverProcessor sv.SemVerCommitsProcessor,
    monorepoProcessor sv.MonorepoProcessor,
    cfg Config,
    repoPath string,
) func(c *cli.Context) error {
    return func(c *cli.Context) error {
        components, err := monorepoProcessor.FindComponents(repoPath, cfg.Monorepo)
        if err != nil {
            return fmt.Errorf("error finding monorepo components: %v", err)
        }

        for _, component := range components {
            commits, err := componentCommits(git, repoPath, component)
            if err != nil {
                return fmt.Errorf("error getting commits for %s: %v", component.Name, err)
            }

            nextVer, updated := monorepoProcessor.NextVersion(component, commits, semverProcessor)
            if !updated {
                fmt.Printf("%s: no version change (current: %s)\n", component.Name, component.CurrentVersion.String())
                continue
            }

            if err := monorepoProcessor.UpdateVersion(component, *nextVer, cfg.Monorepo); err != nil {
                return fmt.Errorf("error updating version for %s: %v", component.Name, err)
            }
            fmt.Printf("%s: %s\n", component.Name, nextVer.String())
        }
        return nil
    }
}

// componentCommits returns commits that touched a component's directory since the
// last commit that modified its versioning file (the "version baseline").
// Falls back to all directory commits when the versioning file has no history.
func componentCommits(git sv.Git, repoPath string, component sv.MonorepoComponent) ([]sv.GitCommitLog, error) {
    relDir, err := filepath.Rel(repoPath, component.RootPath)
    if err != nil {
        return nil, err
    }
    relFile, err := filepath.Rel(repoPath, component.VersioningFilePath)
    if err != nil {
        return nil, err
    }

    baselineHash, err := git.LastCommitForFile(relFile)
    if err != nil {
        return nil, fmt.Errorf("error finding baseline commit: %v", err)
    }

    var lr sv.LogRange
    if baselineHash != "" {
        lr = sv.NewLogRangeWithPaths(sv.HashRange, baselineHash, "", []string{relDir})
    } else {
        lr = sv.NewLogRangeWithPaths(sv.TagRange, "", "", []string{relDir})
    }
    return git.Log(lr)
}
```

Note: `filepath` import is already in `handlers.go`; add it if not present.

---

### Step 6 — `cmd/git-sv/main.go`: Instantiate and register

After the existing processor instantiations in `main()`:

```go
monorepoProcessor := sv.NewMonorepoProcessor()
```

In `app.Commands`:

```go
{
    Name:    "monorepo-next-version",
    Aliases: []string{"mnv"},
    Usage:   "generate next version for each component in a monorepo",
    Action:  monorepoNextVersionHandler(git, semverProcessor, monorepoProcessor, cfg, repoPath),
},
{
    Name:    "monorepo-tag",
    Aliases: []string{"mtg"},
    Usage:   "update version files for all changed components in a monorepo",
    Action:  monorepoTagHandler(git, semverProcessor, monorepoProcessor, cfg, repoPath),
},
```

---

### Step 7 — `sv/monorepo_test.go`: Unit tests

Cover with table-driven tests:

1. **`getByPath`** — simple key, nested key, key with dot (greedy match), key with slash, missing key, empty path
2. **`setByPath`** — same cases + verify mutation
3. **`readVersionFromFile`** — valid YAML, valid JSON, missing path key, invalid semver value
4. **`writeVersionToFile`** — YAML round-trip, JSON round-trip
5. **`FindComponents`** — uses temp dir with fixture files; verifies name, path, and version extraction

---

## Commit Strategy

| Commit | Message |
|---|---|
| 1 | `feat(sv): add MonorepoConfig to sv/config.go` |
| 2 | `feat(sv): add path-filtered git log and LastCommitForFile` |
| 3 | `feat(sv): add MonorepoProcessor with file-based versioning` |
| 4 | `feat(sv): add monorepo unit tests` |
| 5 | `feat(cmd): wire monorepo commands (mnv, mtg)` |

---

## Limitations & Notes

- **`filepath.Glob` only supports single `*`**, not `**`. Pattern like `components/**/service.json` would not work. Document this clearly; implement `filepath.WalkDir`-based matching as a future improvement.
- **YAML comments are not preserved** when the versioning file is updated. The file is parsed into `map[string]interface{}` and re-marshalled. If comment preservation is required, switch to `yaml.Node`-based manipulation (significantly more complex).
- **`monorepo-tag` does not create a git commit.** It updates the files on disk. The caller (CI script) should commit and push those changes. This mirrors how tools like `release-please` work — version file updates are separate commits.
- **No new external dependencies** are required.
