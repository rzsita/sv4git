package main

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Masterminds/semver/v3"
	"github.com/bvieira/sv4git/v2/sv"
	"github.com/urfave/cli/v2"
)

// ---- mock implementations ----

type mockGit struct {
	lastComponentTagFn func(componentPath string) string
	logFn              func(lr sv.LogRange) ([]sv.GitCommitLog, error)
	tagForComponentFn  func(version semver.Version, componentPath string) (string, error)
	lastFileCommitFn   func(relPath string) string
	showFileFn         func(commit, relPath string) ([]byte, error)
	componentTagsFn    func(componentPath string) ([]sv.GitTag, error)
}

func (m mockGit) LastTag() string                            { return "" }
func (m mockGit) Log(lr sv.LogRange) ([]sv.GitCommitLog, error) { return m.logFn(lr) }
func (m mockGit) Commit(header, body, footer string) error   { return nil }
func (m mockGit) Tag(version semver.Version) (string, error) { return "", nil }
func (m mockGit) Tags() ([]sv.GitTag, error)                 { return nil, nil }
func (m mockGit) Branch() string                             { return "" }
func (m mockGit) IsDetached() (bool, error)                  { return false, nil }
func (m mockGit) LastComponentTag(componentPath string) string {
	return m.lastComponentTagFn(componentPath)
}
func (m mockGit) TagForComponent(version semver.Version, componentPath string) (string, error) {
	return m.tagForComponentFn(version, componentPath)
}
func (m mockGit) LastFileCommit(relPath string) string {
	if m.lastFileCommitFn != nil {
		return m.lastFileCommitFn(relPath)
	}
	return ""
}
func (m mockGit) ShowFile(commit, relPath string) ([]byte, error) {
	if m.showFileFn != nil {
		return m.showFileFn(commit, relPath)
	}
	return nil, nil
}
func (m mockGit) ComponentTags(componentPath string) ([]sv.GitTag, error) {
	if m.componentTagsFn != nil {
		return m.componentTagsFn(componentPath)
	}
	return nil, nil
}

type mockMonorepoProcessor struct {
	findComponentsFn func(repoRoot string, cfg sv.MonorepoConfig) ([]sv.MonorepoComponent, error)
	nextVersionFn    func(component sv.MonorepoComponent, commits []sv.GitCommitLog, semverProc sv.SemVerCommitsProcessor) (*semver.Version, bool)
	updateVersionFn  func(component sv.MonorepoComponent, version semver.Version, cfg sv.MonorepoConfig) error
}

func (m mockMonorepoProcessor) FindComponents(repoRoot string, cfg sv.MonorepoConfig) ([]sv.MonorepoComponent, error) {
	return m.findComponentsFn(repoRoot, cfg)
}
func (m mockMonorepoProcessor) NextVersion(component sv.MonorepoComponent, commits []sv.GitCommitLog, semverProc sv.SemVerCommitsProcessor) (*semver.Version, bool) {
	return m.nextVersionFn(component, commits, semverProc)
}
func (m mockMonorepoProcessor) UpdateVersion(component sv.MonorepoComponent, version semver.Version, cfg sv.MonorepoConfig) error {
	return m.updateVersionFn(component, version, cfg)
}

type mockSemVerProcessor struct {
	nextVersionFn func(version *semver.Version, commits []sv.GitCommitLog) (*semver.Version, bool)
}

func (m mockSemVerProcessor) NextVersion(version *semver.Version, commits []sv.GitCommitLog) (*semver.Version, bool) {
	if m.nextVersionFn == nil {
		return version, false
	}
	return m.nextVersionFn(version, commits)
}

type mockReleaseNoteProcessor struct{}

func (m mockReleaseNoteProcessor) Create(version *semver.Version, tag string, date time.Time, commits []sv.GitCommitLog) sv.ReleaseNote {
	return sv.ReleaseNote{Version: version, Tag: tag, Date: date}
}

type mockOutputFormatter struct {
	formatChangelogFn func(releasenotes []sv.ReleaseNote) (string, error)
}

func (m mockOutputFormatter) FormatReleaseNote(releasenote sv.ReleaseNote) (string, error) {
	return "", nil
}
func (m mockOutputFormatter) FormatChangelog(releasenotes []sv.ReleaseNote) (string, error) {
	if m.formatChangelogFn != nil {
		return m.formatChangelogFn(releasenotes)
	}
	return "# Changelog\n", nil
}

// newCLICtx creates a minimal *cli.Context suitable for calling handlers under test.
func newCLICtx() *cli.Context {
	return cli.NewContext(cli.NewApp(), flag.NewFlagSet("test", flag.ContinueOnError), nil)
}

// makeComponent creates a MonorepoComponent with the given name and version, rooted in a
// temp directory that is registered for cleanup.
func makeComponent(t *testing.T, name, version string) sv.MonorepoComponent {
	t.Helper()
	dir := filepath.Join(t.TempDir(), name)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	v := semver.MustParse(version)
	return sv.MonorepoComponent{
		Name:               name,
		RootPath:           dir,
		VersioningFilePath: filepath.Join(dir, "package.json"),
		CurrentVersion:     v,
	}
}

// ---- monorepoNextVersionHandler tests ----

func Test_monorepoNextVersionHandler_NoUpdate(t *testing.T) {
	comp := makeComponent(t, "alpha", "1.0.0")

	git := mockGit{
		lastComponentTagFn: func(string) string { return "" },
		logFn:              func(sv.LogRange) ([]sv.GitCommitLog, error) { return nil, nil },
	}
	mnrp := mockMonorepoProcessor{
		findComponentsFn: func(string, sv.MonorepoConfig) ([]sv.MonorepoComponent, error) {
			return []sv.MonorepoComponent{comp}, nil
		},
	}
	semverProc := mockSemVerProcessor{nextVersionFn: func(v *semver.Version, _ []sv.GitCommitLog) (*semver.Version, bool) { return v, false }}
	cfg := Config{Monorepo: sv.MonorepoConfig{VersioningFile: "*/package.json", Path: "version"}}

	handler := monorepoNextVersionHandler(git, semverProc, mnrp, cfg, t.TempDir())
	if err := handler(newCLICtx()); err != nil {
		t.Errorf("monorepoNextVersionHandler() unexpected error: %v", err)
	}
}

func Test_monorepoNextVersionHandler_WithUpdate(t *testing.T) {
	comp := makeComponent(t, "alpha", "1.0.0")
	nextVer := semver.MustParse("1.1.0")

	git := mockGit{
		lastComponentTagFn: func(string) string { return "" },
		logFn:              func(sv.LogRange) ([]sv.GitCommitLog, error) { return []sv.GitCommitLog{{Hash: "abc"}}, nil },
	}
	mnrp := mockMonorepoProcessor{
		findComponentsFn: func(string, sv.MonorepoConfig) ([]sv.MonorepoComponent, error) {
			return []sv.MonorepoComponent{comp}, nil
		},
	}
	semverProc := mockSemVerProcessor{nextVersionFn: func(v *semver.Version, _ []sv.GitCommitLog) (*semver.Version, bool) { return nextVer, true }}
	cfg := Config{Monorepo: sv.MonorepoConfig{VersioningFile: "*/package.json", Path: "version"}}

	handler := monorepoNextVersionHandler(git, semverProc, mnrp, cfg, t.TempDir())
	if err := handler(newCLICtx()); err != nil {
		t.Errorf("monorepoNextVersionHandler() unexpected error: %v", err)
	}
}

func Test_monorepoNextVersionHandler_FindComponentsError(t *testing.T) {
	git := mockGit{
		lastComponentTagFn: func(string) string { return "" },
		logFn:              func(sv.LogRange) ([]sv.GitCommitLog, error) { return nil, nil },
	}
	mnrp := mockMonorepoProcessor{
		findComponentsFn: func(string, sv.MonorepoConfig) ([]sv.MonorepoComponent, error) {
			return nil, os.ErrNotExist
		},
	}
	semverProc := mockSemVerProcessor{}
	cfg := Config{}

	handler := monorepoNextVersionHandler(git, semverProc, mnrp, cfg, t.TempDir())
	if err := handler(newCLICtx()); err == nil {
		t.Error("monorepoNextVersionHandler() expected error when FindComponents fails, got nil")
	}
}

// ---- monorepoTagHandler tests ----

func Test_monorepoTagHandler_SkipsNoUpdate(t *testing.T) {
	comp := makeComponent(t, "beta", "2.0.0")

	git := mockGit{
		lastComponentTagFn: func(string) string { return "" },
		logFn:              func(sv.LogRange) ([]sv.GitCommitLog, error) { return nil, nil },
	}
	mnrp := mockMonorepoProcessor{
		findComponentsFn: func(string, sv.MonorepoConfig) ([]sv.MonorepoComponent, error) {
			return []sv.MonorepoComponent{comp}, nil
		},
		nextVersionFn: func(component sv.MonorepoComponent, _ []sv.GitCommitLog, _ sv.SemVerCommitsProcessor) (*semver.Version, bool) {
			return component.CurrentVersion, false // no update
		},
	}
	semverProc := mockSemVerProcessor{}
	cfg := Config{}

	handler := monorepoTagHandler(git, semverProc, mnrp, cfg, comp.RootPath)
	if err := handler(newCLICtx()); err != nil {
		t.Errorf("monorepoTagHandler() unexpected error: %v", err)
	}
}

func Test_monorepoTagHandler_UpdatesAndTags(t *testing.T) {
	repoRoot := t.TempDir()
	comp := makeComponent(t, "gamma", "3.0.0")
	// RootPath must be inside repoRoot for filepath.Rel to work.
	comp.RootPath = filepath.Join(repoRoot, "gamma")
	if err := os.MkdirAll(comp.RootPath, 0755); err != nil {
		t.Fatal(err)
	}

	nextVer := semver.MustParse("3.1.0")
	var updatedVersion semver.Version
	var createdTag string

	git := mockGit{
		lastComponentTagFn: func(string) string { return "" },
		logFn:              func(sv.LogRange) ([]sv.GitCommitLog, error) { return []sv.GitCommitLog{{Hash: "abc"}}, nil },
		tagForComponentFn: func(version semver.Version, componentPath string) (string, error) {
			createdTag = componentPath + "/v" + version.String()
			return createdTag, nil
		},
	}
	mnrp := mockMonorepoProcessor{
		findComponentsFn: func(string, sv.MonorepoConfig) ([]sv.MonorepoComponent, error) {
			return []sv.MonorepoComponent{comp}, nil
		},
		nextVersionFn: func(_ sv.MonorepoComponent, _ []sv.GitCommitLog, _ sv.SemVerCommitsProcessor) (*semver.Version, bool) {
			return nextVer, true
		},
		updateVersionFn: func(_ sv.MonorepoComponent, version semver.Version, _ sv.MonorepoConfig) error {
			updatedVersion = version
			return nil
		},
	}
	semverProc := mockSemVerProcessor{}
	cfg := Config{}

	handler := monorepoTagHandler(git, semverProc, mnrp, cfg, repoRoot)
	if err := handler(newCLICtx()); err != nil {
		t.Fatalf("monorepoTagHandler() unexpected error: %v", err)
	}
	if updatedVersion.String() != "3.1.0" {
		t.Errorf("UpdateVersion called with %s, want 3.1.0", updatedVersion.String())
	}
	if !strings.HasSuffix(createdTag, "v3.1.0") {
		t.Errorf("TagForComponent tag = %q, want suffix v3.1.0", createdTag)
	}
}

// ---- monorepoChangelogHandler tests ----

func Test_monorepoChangelogHandler_SkipsNoUpdate(t *testing.T) {
	comp := makeComponent(t, "delta", "1.0.0")

	git := mockGit{
		lastComponentTagFn: func(string) string { return "" },
		logFn:              func(sv.LogRange) ([]sv.GitCommitLog, error) { return nil, nil },
	}
	mnrp := mockMonorepoProcessor{
		findComponentsFn: func(string, sv.MonorepoConfig) ([]sv.MonorepoComponent, error) {
			return []sv.MonorepoComponent{comp}, nil
		},
	}
	semverProc := mockSemVerProcessor{}
	rnProc := mockReleaseNoteProcessor{}
	formatter := mockOutputFormatter{}
	cfg := Config{}

	handler := monorepoChangelogHandler(git, semverProc, mnrp, rnProc, formatter, cfg, comp.RootPath)
	if err := handler(newCLICtx()); err != nil {
		t.Errorf("monorepoChangelogHandler() unexpected error: %v", err)
	}

	// CHANGELOG.md must NOT have been written when there are no changes.
	if _, err := os.Stat(filepath.Join(comp.RootPath, "CHANGELOG.md")); !os.IsNotExist(err) {
		t.Error("monorepoChangelogHandler() wrote CHANGELOG.md for a component with no changes")
	}
}

func Test_monorepoChangelogHandler_WritesChangelog(t *testing.T) {
	repoRoot := t.TempDir()
	comp := makeComponent(t, "epsilon", "1.0.0")
	comp.RootPath = filepath.Join(repoRoot, "epsilon")
	if err := os.MkdirAll(comp.RootPath, 0755); err != nil {
		t.Fatal(err)
	}

	nextVer := semver.MustParse("1.1.0")
	const changelogContent = "# Changelog\n## v1.1.0\n"

	git := mockGit{
		lastComponentTagFn: func(string) string { return "" },
		logFn:              func(sv.LogRange) ([]sv.GitCommitLog, error) { return []sv.GitCommitLog{{Hash: "abc", Date: "2024-01-01"}}, nil },
	}
	mnrp := mockMonorepoProcessor{
		findComponentsFn: func(string, sv.MonorepoConfig) ([]sv.MonorepoComponent, error) {
			return []sv.MonorepoComponent{comp}, nil
		},
	}
	semverProc := mockSemVerProcessor{
		nextVersionFn: func(_ *semver.Version, _ []sv.GitCommitLog) (*semver.Version, bool) {
			return nextVer, true
		},
	}
	rnProc := mockReleaseNoteProcessor{}
	formatter := mockOutputFormatter{
		formatChangelogFn: func([]sv.ReleaseNote) (string, error) { return changelogContent, nil },
	}
	cfg := Config{}

	// Use --add-next-version to get the unreleased-commits behaviour (no component tags exist).
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	fs.Bool("add-next-version", true, "")
	fs.Bool("all", false, "")
	fs.Bool("semantic-version-only", false, "")
	fs.Int("size", 10, "")
	ctx := cli.NewContext(cli.NewApp(), fs, nil)

	handler := monorepoChangelogHandler(git, semverProc, mnrp, rnProc, formatter, cfg, repoRoot)
	if err := handler(ctx); err != nil {
		t.Fatalf("monorepoChangelogHandler() unexpected error: %v", err)
	}

	changelogPath := filepath.Join(comp.RootPath, "CHANGELOG.md")
	got, err := os.ReadFile(changelogPath)
	if err != nil {
		t.Fatalf("CHANGELOG.md not written: %v", err)
	}
	if string(got) != changelogContent {
		t.Errorf("CHANGELOG.md content = %q, want %q", string(got), changelogContent)
	}
}

func Test_monorepoChangelogHandler_FindComponentsError(t *testing.T) {
	git := mockGit{
		lastComponentTagFn: func(string) string { return "" },
		logFn:              func(sv.LogRange) ([]sv.GitCommitLog, error) { return nil, nil },
	}
	mnrp := mockMonorepoProcessor{
		findComponentsFn: func(string, sv.MonorepoConfig) ([]sv.MonorepoComponent, error) {
			return nil, os.ErrPermission
		},
	}
	handler := monorepoChangelogHandler(git, mockSemVerProcessor{}, mnrp, mockReleaseNoteProcessor{}, mockOutputFormatter{}, Config{}, t.TempDir())
	if err := handler(newCLICtx()); err == nil {
		t.Error("monorepoChangelogHandler() expected error when FindComponents fails, got nil")
	}
}

func Test_monorepoChangelogHandler_WithTagHistory(t *testing.T) {
	repoRoot := t.TempDir()
	comp := makeComponent(t, "sigma", "1.2.0")
	comp.RootPath = filepath.Join(repoRoot, "sigma")
	if err := os.MkdirAll(comp.RootPath, 0755); err != nil {
		t.Fatal(err)
	}

	// Two component tags, oldest first (handler sorts descending).
	tag1 := sv.GitTag{Name: "sigma/v1.0.0", Date: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)}
	tag2 := sv.GitTag{Name: "sigma/v1.1.0", Date: time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC)}

	git := mockGit{
		lastComponentTagFn: func(string) string { return "" },
		logFn:              func(sv.LogRange) ([]sv.GitCommitLog, error) { return []sv.GitCommitLog{{Hash: "abc"}}, nil },
		componentTagsFn:    func(string) ([]sv.GitTag, error) { return []sv.GitTag{tag1, tag2}, nil },
	}
	mnrp := mockMonorepoProcessor{
		findComponentsFn: func(string, sv.MonorepoConfig) ([]sv.MonorepoComponent, error) {
			return []sv.MonorepoComponent{comp}, nil
		},
	}

	var capturedNotes []sv.ReleaseNote
	formatter := mockOutputFormatter{
		formatChangelogFn: func(notes []sv.ReleaseNote) (string, error) {
			capturedNotes = notes
			return "# Changelog\n", nil
		},
	}

	// --all so both tags are included regardless of --size.
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	fs.Bool("add-next-version", false, "")
	fs.Bool("all", true, "")
	fs.Bool("semantic-version-only", false, "")
	fs.Int("size", 10, "")
	ctx := cli.NewContext(cli.NewApp(), fs, nil)

	handler := monorepoChangelogHandler(git, mockSemVerProcessor{}, mnrp, mockReleaseNoteProcessor{}, formatter, Config{}, repoRoot)
	if err := handler(ctx); err != nil {
		t.Fatalf("monorepoChangelogHandler() unexpected error: %v", err)
	}

	// Two tags → two release note entries (sorted newest-first: v1.1.0, v1.0.0).
	if len(capturedNotes) != 2 {
		t.Errorf("FormatChangelog received %d release note entries, want 2", len(capturedNotes))
	}
	if capturedNotes[0].Tag != "sigma/v1.1.0" {
		t.Errorf("first entry tag = %q, want sigma/v1.1.0", capturedNotes[0].Tag)
	}
	if capturedNotes[1].Tag != "sigma/v1.0.0" {
		t.Errorf("second entry tag = %q, want sigma/v1.0.0", capturedNotes[1].Tag)
	}

	// CHANGELOG.md must have been written.
	if _, err := os.Stat(filepath.Join(comp.RootPath, "CHANGELOG.md")); err != nil {
		t.Errorf("CHANGELOG.md not written: %v", err)
	}
}

// ---- monorepoUpdateVersionHandler tests ----

func Test_monorepoUpdateVersionHandler_SkipsNoUpdate(t *testing.T) {
	comp := makeComponent(t, "zeta", "1.0.0")

	updateCalled := false
	git := mockGit{
		lastComponentTagFn: func(string) string { return "" },
		logFn:              func(sv.LogRange) ([]sv.GitCommitLog, error) { return nil, nil },
	}
	mnrp := mockMonorepoProcessor{
		findComponentsFn: func(string, sv.MonorepoConfig) ([]sv.MonorepoComponent, error) {
			return []sv.MonorepoComponent{comp}, nil
		},
		nextVersionFn: func(component sv.MonorepoComponent, _ []sv.GitCommitLog, _ sv.SemVerCommitsProcessor) (*semver.Version, bool) {
			return component.CurrentVersion, false
		},
		updateVersionFn: func(_ sv.MonorepoComponent, _ semver.Version, _ sv.MonorepoConfig) error {
			updateCalled = true
			return nil
		},
	}

	handler := monorepoUpdateVersionHandler(git, mockSemVerProcessor{}, mnrp, Config{}, comp.RootPath)
	if err := handler(newCLICtx()); err != nil {
		t.Fatalf("monorepoUpdateVersionHandler() unexpected error: %v", err)
	}
	if updateCalled {
		t.Error("monorepoUpdateVersionHandler() called UpdateVersion for a component with no changes")
	}
}

func Test_monorepoUpdateVersionHandler_WritesVersion(t *testing.T) {
	repoRoot := t.TempDir()
	comp := makeComponent(t, "eta", "2.0.0")
	comp.RootPath = filepath.Join(repoRoot, "eta")
	if err := os.MkdirAll(comp.RootPath, 0755); err != nil {
		t.Fatal(err)
	}

	nextVer := semver.MustParse("2.1.0")
	var updatedVersion semver.Version
	tagCalled := false

	git := mockGit{
		lastComponentTagFn: func(string) string { return "" },
		logFn:              func(sv.LogRange) ([]sv.GitCommitLog, error) { return []sv.GitCommitLog{{Hash: "abc"}}, nil },
		tagForComponentFn: func(version semver.Version, _ string) (string, error) {
			tagCalled = true
			return "", nil
		},
	}
	mnrp := mockMonorepoProcessor{
		findComponentsFn: func(string, sv.MonorepoConfig) ([]sv.MonorepoComponent, error) {
			return []sv.MonorepoComponent{comp}, nil
		},
		nextVersionFn: func(_ sv.MonorepoComponent, _ []sv.GitCommitLog, _ sv.SemVerCommitsProcessor) (*semver.Version, bool) {
			return nextVer, true
		},
		updateVersionFn: func(_ sv.MonorepoComponent, version semver.Version, _ sv.MonorepoConfig) error {
			updatedVersion = version
			return nil
		},
	}

	semverProc := mockSemVerProcessor{
		nextVersionFn: func(_ *semver.Version, _ []sv.GitCommitLog) (*semver.Version, bool) {
			return nextVer, true
		},
	}
	handler := monorepoUpdateVersionHandler(git, semverProc, mnrp, Config{}, repoRoot)
	if err := handler(newCLICtx()); err != nil {
		t.Fatalf("monorepoUpdateVersionHandler() unexpected error: %v", err)
	}
	if updatedVersion.String() != "2.1.0" {
		t.Errorf("UpdateVersion called with %s, want 2.1.0", updatedVersion.String())
	}
	if tagCalled {
		t.Error("monorepoUpdateVersionHandler() must not call TagForComponent")
	}
}

// Test_monorepoUpdateVersionHandler_IdempotentAfterBump verifies that a second
// invocation of monorepo-bump is a no-op when the version file already reflects
// the computed next version (i.e. the file was bumped but not yet tagged).
//
// The test simulates the scenario where:
//   - No component tag exists (monorepo-bump never creates tags).
//   - The versioning file was committed at some commit with version "1.0.0".
//   - ShowFile returns that committed content (version 1.0.0).
//   - Commits since that file-commit exist, producing nextVer = 1.1.0.
//   - The on-disk CurrentVersion is already 1.1.0 (set by a previous run).
//
// Expected: nextVer (1.1.0) == currentVersion (1.1.0) → UpdateVersion is NOT called.
func Test_monorepoUpdateVersionHandler_IdempotentAfterBump(t *testing.T) {
	repoRoot := t.TempDir()
	// Simulate component whose file was already bumped to 1.1.0 on disk.
	comp := makeComponent(t, "idem", "1.1.0")
	comp.RootPath = filepath.Join(repoRoot, "idem")
	if err := os.MkdirAll(comp.RootPath, 0755); err != nil {
		t.Fatal(err)
	}
	comp.VersioningFilePath = filepath.Join(comp.RootPath, "package.json")

	nextVer := semver.MustParse("1.1.0")
	updateCalled := false

	git := mockGit{
		lastComponentTagFn: func(string) string { return "" }, // no component tag
		lastFileCommitFn:   func(string) string { return "abc123" },
		showFileFn: func(commit, relPath string) ([]byte, error) {
			// Return file content with the pre-bump version (1.0.0).
			return []byte(`{"version":"1.0.0"}`), nil
		},
		logFn: func(sv.LogRange) ([]sv.GitCommitLog, error) {
			return []sv.GitCommitLog{{Hash: "def456"}}, nil
		},
	}
	mnrp := mockMonorepoProcessor{
		findComponentsFn: func(string, sv.MonorepoConfig) ([]sv.MonorepoComponent, error) {
			return []sv.MonorepoComponent{comp}, nil
		},
		updateVersionFn: func(_ sv.MonorepoComponent, _ semver.Version, _ sv.MonorepoConfig) error {
			updateCalled = true
			return nil
		},
	}
	semverProc := mockSemVerProcessor{
		nextVersionFn: func(_ *semver.Version, _ []sv.GitCommitLog) (*semver.Version, bool) {
			return nextVer, true // bump produces 1.1.0
		},
	}
	cfg := Config{Monorepo: sv.MonorepoConfig{Path: "version"}}

	handler := monorepoUpdateVersionHandler(git, semverProc, mnrp, cfg, repoRoot)
	if err := handler(newCLICtx()); err != nil {
		t.Fatalf("monorepoUpdateVersionHandler() unexpected error: %v", err)
	}
	if updateCalled {
		t.Error("monorepoUpdateVersionHandler() called UpdateVersion but version file already had the correct version (idempotency violation)")
	}
}

func Test_monorepoUpdateVersionHandler_FindComponentsError(t *testing.T) {
	git := mockGit{
		lastComponentTagFn: func(string) string { return "" },
		logFn:              func(sv.LogRange) ([]sv.GitCommitLog, error) { return nil, nil },
	}
	mnrp := mockMonorepoProcessor{
		findComponentsFn: func(string, sv.MonorepoConfig) ([]sv.MonorepoComponent, error) {
			return nil, os.ErrPermission
		},
	}
	handler := monorepoUpdateVersionHandler(git, mockSemVerProcessor{}, mnrp, Config{}, t.TempDir())
	if err := handler(newCLICtx()); err == nil {
		t.Error("monorepoUpdateVersionHandler() expected error when FindComponents fails, got nil")
	}
}
