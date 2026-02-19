package sv

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Masterminds/semver/v3"
)

// setupIntegrationRepo creates a temporary git repository with a bare origin and
// a working clone. It changes the process working directory to workDir and restores
// it on test cleanup.
//
// Returns:
//   - gitCmd: runs git subcommands inside workDir, fatals on error
//   - workDir: path to the working clone
func setupIntegrationRepo(t *testing.T) (func(args ...string), string) {
	t.Helper()

	originDir := t.TempDir()
	if err := exec.Command("git", "init", "--bare", originDir).Run(); err != nil {
		t.Fatalf("git init --bare: %v", err)
	}

	workDir := t.TempDir()
	if err := exec.Command("git", "clone", originDir, workDir).Run(); err != nil {
		t.Fatalf("git clone: %v", err)
	}

	gitCmd := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = workDir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}

	gitCmd("config", "user.email", "test@test.com")
	gitCmd("config", "user.name", "Test User")
	gitCmd("config", "commit.gpgsign", "false")
	gitCmd("config", "tag.gpgsign", "false")

	// Initial commit so the repo has a HEAD.
	readme := filepath.Join(workDir, "README.md")
	if err := os.WriteFile(readme, []byte("test"), 0600); err != nil {
		t.Fatal(err)
	}
	gitCmd("add", "README.md")
	gitCmd("commit", "-m", "initial commit")
	gitCmd("push", "-u", "origin", "HEAD")

	// Change process working directory so GitImpl picks up the right repo.
	orig, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(workDir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(orig) })

	return gitCmd, workDir
}

// addCommit writes a file and creates a new commit, giving subsequent tags a
// distinct creatordate so that --sort=-creatordate is deterministic.
func addCommit(t *testing.T, gitCmd func(...string), workDir, name string) {
	t.Helper()
	f := filepath.Join(workDir, name)
	if err := os.WriteFile(f, []byte(name), 0600); err != nil {
		t.Fatal(err)
	}
	gitCmd("add", name)
	gitCmd("commit", "-m", "chore: add "+name)
}

func TestLastComponentTag_NoTag(t *testing.T) {
	_, _ = setupIntegrationRepo(t)

	g := GitImpl{}
	got := g.LastComponentTag("services/my-service")
	if got != "" {
		t.Errorf("LastComponentTag() = %q, want empty string when no tag exists", got)
	}
}

func TestLastComponentTag_ReturnsLatest(t *testing.T) {
	gitCmd, workDir := setupIntegrationRepo(t)

	// Create v1.0.0 with a fixed past date so the second tag is unambiguously newer.
	pastCmd := exec.Command("git", "tag", "-a", "services/my-service/v1.0.0", "-m", "v1.0.0")
	pastCmd.Dir = workDir
	pastCmd.Env = append(os.Environ(), "GIT_COMMITTER_DATE=2000-01-01T00:00:00+00:00")
	if out, err := pastCmd.CombinedOutput(); err != nil {
		t.Fatalf("git tag v1.0.0: %v\n%s", err, out)
	}

	addCommit(t, gitCmd, workDir, "bump1.txt")
	gitCmd("tag", "-a", "services/my-service/v1.1.0", "-m", "v1.1.0")
	// Create a tag for a different component to confirm no cross-contamination.
	gitCmd("tag", "-a", "services/other/v9.0.0", "-m", "other")

	g := GitImpl{}
	got := g.LastComponentTag("services/my-service")
	if got != "services/my-service/v1.1.0" {
		t.Errorf("LastComponentTag() = %q, want %q", got, "services/my-service/v1.1.0")
	}
}

func TestLastComponentTag_IsolatedByPath(t *testing.T) {
	gitCmd, _ := setupIntegrationRepo(t)

	gitCmd("tag", "-a", "services/other/v3.0.0", "-m", "other")

	g := GitImpl{}
	got := g.LastComponentTag("services/my-service")
	if got != "" {
		t.Errorf("LastComponentTag() = %q, want empty string (different component)", got)
	}
}

func TestTagForComponent_CreatesAndPushesTag(t *testing.T) {
	_, _ = setupIntegrationRepo(t)

	g := GitImpl{}
	ver := semver.MustParse("2.3.4")
	tagName, err := g.TagForComponent(*ver, "libs/mylib")
	if err != nil {
		t.Fatalf("TagForComponent() error = %v", err)
	}

	wantTag := "libs/mylib/v2.3.4"
	if tagName != wantTag {
		t.Errorf("TagForComponent() tagName = %q, want %q", tagName, wantTag)
	}

	// Verify the tag was created locally.
	out, err := exec.Command("git", "tag", "-l", wantTag).Output()
	if err != nil {
		t.Fatalf("git tag -l: %v", err)
	}
	if strings.TrimSpace(string(out)) != wantTag {
		t.Errorf("local tag %q not found after TagForComponent()", wantTag)
	}

	// Verify last component tag now returns it.
	if got := g.LastComponentTag("libs/mylib"); got != wantTag {
		t.Errorf("LastComponentTag() after tag = %q, want %q", got, wantTag)
	}
}

func TestTagForComponent_RoundTrip(t *testing.T) {
	// This test verifies that successive calls to TagForComponent succeed and that
	// both tags are visible locally. Ordering is exercised by TestLastComponentTag_ReturnsLatest.
	gitCmd, workDir := setupIntegrationRepo(t)

	g := GitImpl{}
	ver1 := semver.MustParse("1.0.0")
	if tag, err := g.TagForComponent(*ver1, "api/v1"); err != nil {
		t.Fatalf("TagForComponent() v1 error = %v", err)
	} else if tag != "api/v1/v1.0.0" {
		t.Errorf("TagForComponent() v1 = %q, want api/v1/v1.0.0", tag)
	}

	addCommit(t, gitCmd, workDir, "bump2.txt")

	ver2 := semver.MustParse("1.1.0")
	if tag, err := g.TagForComponent(*ver2, "api/v1"); err != nil {
		t.Fatalf("TagForComponent() v2 error = %v", err)
	} else if tag != "api/v1/v1.1.0" {
		t.Errorf("TagForComponent() v2 = %q, want api/v1/v1.1.0", tag)
	}

	// Both tags must exist locally.
	for _, wantTag := range []string{"api/v1/v1.0.0", "api/v1/v1.1.0"} {
		out, err := exec.Command("git", "tag", "-l", wantTag).Output()
		if err != nil {
			t.Fatalf("git tag -l %s: %v", wantTag, err)
		}
		if strings.TrimSpace(string(out)) != wantTag {
			t.Errorf("tag %q not found after TagForComponent()", wantTag)
		}
	}
}
