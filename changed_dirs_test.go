package tftargets

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"
)

// newTerraformRepo creates a repository with one commit:
//
//	root/main.tf        terraform block, calls ../modules/mod
//	modules/mod/main.tf variable and resource only
//	lone/main.tf        variable only, called by nobody
//
// It returns the repository directory and the SHA of the commit.
func newTerraformRepo(t *testing.T) (string, string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not available")
	}
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")

	dir := t.TempDir()
	runGit(t, dir, "init", "-b", "main")
	writeFile(t, dir, "root/main.tf", "terraform {}\n\nmodule \"mod\" {\n  source = \"../modules/mod\"\n}\n")
	writeFile(t, dir, "modules/mod/main.tf", "variable \"name\" {}\n\nresource \"null_resource\" \"this\" {}\n")
	writeFile(t, dir, "lone/main.tf", "variable \"name\" {}\n")
	runGit(t, dir, "add", ".")
	runGit(t, dir, "commit", "-m", "initial")
	return dir, runGit(t, dir, "rev-parse", "HEAD")
}

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	path := filepath.Join(dir, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestCollectTargetsChangedDirs(t *testing.T) {
	tests := []struct {
		name string
		// change modifies the working tree of the repository.
		change func(t *testing.T, dir string)
		// want is the result with --include-changed-dirs.
		want []string
		// wantDefault is the result without it.
		wantDefault []string
	}{
		{
			name: "module without a terraform block",
			change: func(t *testing.T, dir string) {
				writeFile(t, dir, "modules/mod/main.tf", "variable \"name\" {}\n")
			},
			want:        []string{"modules/mod", "root"},
			wantDefault: []string{"root"},
		},
		{
			name: "directory without a terraform block that nobody calls",
			change: func(t *testing.T, dir string) {
				writeFile(t, dir, "lone/main.tf", "variable \"other\" {}\n")
			},
			want:        []string{"lone"},
			wantDefault: []string{},
		},
		{
			name: "file renamed to a non-ASCII name and changed",
			change: func(t *testing.T, dir string) {
				runGit(t, dir, "mv", "modules/mod/main.tf", "modules/mod/設定.tf")
				// A small change, so that Git still detects the rename.
				writeFile(t, dir, "modules/mod/設定.tf", "variable \"name\" {}\n\nresource \"null_resource\" \"this\" {}\n\n# changed\n")
			},
			want:        []string{"modules/mod", "root"},
			wantDefault: []string{"root"},
		},
		{
			name: "file name with a space",
			change: func(t *testing.T, dir string) {
				writeFile(t, dir, "modules/mod/a b.tf", "variable \"other\" {}\n")
				runGit(t, dir, "add", ".")
			},
			want:        []string{"modules/mod", "root"},
			wantDefault: []string{"root"},
		},
		{
			name: "file moved to another directory",
			change: func(t *testing.T, dir string) {
				runGit(t, dir, "mv", "lone/main.tf", "modules/mod/lone.tf")
			},
			want:        []string{"lone", "modules/mod", "root"},
			wantDefault: []string{"root"},
		},
		{
			name: "deleted directory",
			change: func(t *testing.T, dir string) {
				runGit(t, dir, "rm", "-r", "lone")
			},
			want:        []string{},
			wantDefault: []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, include := range []bool{true, false} {
				dir, sha := newTerraformRepo(t)
				tt.change(t, dir)

				got, err := collectTargets(&GlobalOptions{
					BaseCommitSha:      sha,
					BaseDir:            dir,
					SearchPath:         ".",
					IncludeChangedDirs: include,
				})
				if err != nil {
					t.Fatalf("collectTargets: %v", err)
				}

				want := tt.wantDefault
				if include {
					want = tt.want
				}
				var wantPaths []string
				for _, w := range want {
					wantPaths = append(wantPaths, filepath.Join(dir, filepath.FromSlash(w)))
				}
				gotPaths := got.ToSlice()
				slices.Sort(gotPaths)
				slices.Sort(wantPaths)
				if !slices.Equal(gotPaths, wantPaths) {
					t.Errorf("include-changed-dirs=%v: got %v, want %v", include, gotPaths, wantPaths)
				}
			}
		})
	}
}

func TestCollectTargetsIgnoresChangedDirsOutsideSearchPath(t *testing.T) {
	dir, sha := newTerraformRepo(t)
	writeFile(t, dir, "lone/main.tf", "variable \"other\" {}\n")
	writeFile(t, dir, "modules/mod/main.tf", "variable \"other\" {}\n")

	got, err := collectTargets(&GlobalOptions{
		BaseCommitSha:      sha,
		BaseDir:            dir,
		SearchPath:         "modules",
		IncludeChangedDirs: true,
	})
	if err != nil {
		t.Fatalf("collectTargets: %v", err)
	}
	if want := []string{filepath.Join(dir, "modules", "mod")}; !slices.Equal(got.ToSlice(), want) {
		t.Errorf("got %v, want %v", got.ToSlice(), want)
	}
}

func TestGetChangedFilesFromGitDoesNotQuotePaths(t *testing.T) {
	dir, sha := newTerraformRepo(t)
	runGit(t, dir, "mv", "lone/main.tf", "lone/設定 1.tf")

	got, err := getChangedFilesFromGit(dir, "main", sha)
	if err != nil {
		t.Fatalf("getChangedFilesFromGit: %v", err)
	}
	want := []string{filepath.Join(dir, "lone", "main.tf"), filepath.Join(dir, "lone", "設定 1.tf")}
	slices.Sort(got)
	if !slices.Equal(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
}
