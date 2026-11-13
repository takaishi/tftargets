package tftargets

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// runGit runs git in dir with a fixed identity and returns trimmed stdout.
func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	args = append([]string{
		"-c", "user.name=tftargets-test",
		"-c", "user.email=tftargets-test@example.com",
		"-c", "commit.gpgsign=false",
	}, args...)
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		stderr := ""
		if ee, ok := err.(*exec.ExitError); ok {
			stderr = string(ee.Stderr)
		}
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, stderr)
	}
	return strings.TrimSpace(string(out))
}

func writeAndCommit(t *testing.T, dir, name, content string) string {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "add", name)
	runGit(t, dir, "commit", "-m", "add "+name)
	return runGit(t, dir, "rev-parse", "HEAD")
}

type gitFixture struct {
	root     string
	origin   string // bare repository used as origin
	seed     string // clone used to push new commits to origin
	work     string // full clone that tftargets runs in
	firstSha string
}

// newGitFixture creates an origin with two commits on main (a.txt, then b.txt)
// and a full clone of it.
func newGitFixture(t *testing.T) gitFixture {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not available")
	}
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")

	root := t.TempDir()
	f := gitFixture{
		root:   root,
		origin: filepath.Join(root, "origin.git"),
		seed:   filepath.Join(root, "seed"),
		work:   filepath.Join(root, "work"),
	}
	runGit(t, root, "init", "--bare", "-b", "main", f.origin)
	runGit(t, root, "init", "-b", "main", f.seed)
	f.firstSha = writeAndCommit(t, f.seed, "a.txt", "a")
	writeAndCommit(t, f.seed, "b.txt", "b")
	runGit(t, f.seed, "remote", "add", "origin", f.origin)
	runGit(t, f.seed, "push", "origin", "main")
	runGit(t, root, "clone", f.origin, f.work)
	return f
}

func (f gitFixture) paths(names ...string) []string {
	var ps []string
	for _, n := range names {
		ps = append(ps, filepath.Join(f.work, n))
	}
	return ps
}

func TestGetChangedFilesFromGitSkipsFetchWhenBaseCommitIsLocal(t *testing.T) {
	f := newGitFixture(t)
	// A fetch would fail against this origin.
	runGit(t, f.work, "remote", "set-url", "origin", filepath.Join(f.root, "missing.git"))

	got, err := getChangedFilesFromGit(f.work, "main", f.firstSha)
	if err != nil {
		t.Fatalf("getChangedFilesFromGit: %v", err)
	}
	if want := f.paths("b.txt"); !slices.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestGetChangedFilesFromGitFetchesWhenBaseCommitIsMissing(t *testing.T) {
	f := newGitFixture(t)
	runGit(t, f.seed, "checkout", "-b", "feature")
	sha := writeAndCommit(t, f.seed, "c.txt", "c")
	runGit(t, f.seed, "push", "origin", "feature")

	if commitExists(f.work, sha) {
		t.Fatalf("commit %s should not exist in the work clone yet", sha)
	}

	got, err := getChangedFilesFromGit(f.work, "main", sha)
	if err != nil {
		t.Fatalf("getChangedFilesFromGit: %v", err)
	}
	// c.txt exists in the base commit but not in the working tree.
	if want := f.paths("c.txt"); !slices.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestGetChangedFilesFromGitFetchesWithBaseBranch(t *testing.T) {
	f := newGitFixture(t)
	runGit(t, f.work, "remote", "set-url", "origin", filepath.Join(f.root, "missing.git"))

	_, err := getChangedFilesFromGit(f.work, "main", "")
	if err == nil || !strings.Contains(err.Error(), "git fetch failed") {
		t.Fatalf("expected a git fetch error, got %v", err)
	}
}

func TestGetChangedFilesFromGitKeepsFullCloneUnshallow(t *testing.T) {
	f := newGitFixture(t)
	// Give origin a commit the work clone lacks, so a depth=1 fetch would
	// make the work clone shallow.
	writeAndCommit(t, f.seed, "d.txt", "d")
	runGit(t, f.seed, "push", "origin", "main")

	if _, err := getChangedFilesFromGit(f.work, "main", f.firstSha); err != nil {
		t.Fatalf("getChangedFilesFromGit: %v", err)
	}
	if got := runGit(t, f.work, "rev-parse", "--is-shallow-repository"); got != "false" {
		t.Errorf("is-shallow-repository = %s, want false", got)
	}
}
