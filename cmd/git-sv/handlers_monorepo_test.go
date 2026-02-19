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
	lastComponentTagFn   func(componentPath string) string
	logFn                func(lr sv.LogRange) ([]sv.GitCommitLog, error)
	tagForComponentFn    func(version semver.Version, componentPath string) (string, error)
}

func (m mockGit) LastTag() string                                              { return "" }
func (m mockGit) Log(lr sv.LogRange) ([]sv.GitCommitLog, error)               { return m.logFn(lr) }
func (m mockGit) Commit(header, body, footer string) error                     { return nil }
func (m mockGit) Tag(version semver.Version) (string, error)                   { return "", nil }
func (m mockGit) Tags() ([]sv.GitTag, error)                                   { return nil, nil }
func (m mockGit) Branch() string                                               { return "" }
func (m mockGit) IsDetached() (bool, error)                                    { return false, nil }
func (m mockGit) LastComponentTag(componentPath string) string                 { return m.lastComponentTagFn(componentPath) }
func (m mockGit) TagForComponent(version semver.Version, componentPath string) (string, error) {
	return m.tagForComponentFn(version, componentPath)
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
		nextVersionFn: func(component sv.MonorepoComponent, commits []sv.GitCommitLog, _ sv.SemVerCommitsProcessor) (*semver.Version, bool) {
			return component.CurrentVersion, false
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
		nextVersionFn: func(_ sv.MonorepoComponent, _ []sv.GitCommitLog, _ sv.SemVerCommitsProcessor) (*semver.Version, bool) {
			return nextVer, true
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
		nextVersionFn: func(component sv.MonorepoComponent, _ []sv.GitCommitLog, _ sv.SemVerCommitsProcessor) (*semver.Version, bool) {
			return component.CurrentVersion, false // no update → should skip
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
		nextVersionFn: func(_ sv.MonorepoComponent, _ []sv.GitCommitLog, _ sv.SemVerCommitsProcessor) (*semver.Version, bool) {
			return nextVer, true
		},
	}
	semverProc := mockSemVerProcessor{}
	rnProc := mockReleaseNoteProcessor{}
	formatter := mockOutputFormatter{
		formatChangelogFn: func([]sv.ReleaseNote) (string, error) { return changelogContent, nil },
	}
	cfg := Config{}

	handler := monorepoChangelogHandler(git, semverProc, mnrp, rnProc, formatter, cfg, repoRoot)
	if err := handler(newCLICtx()); err != nil {
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

	handler := monorepoUpdateVersionHandler(git, mockSemVerProcessor{}, mnrp, Config{}, repoRoot)
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
