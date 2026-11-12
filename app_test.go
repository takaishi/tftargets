package tftargets

import (
	"path/filepath"
	"slices"
	"testing"
)

func TestFindTargets(t *testing.T) {
	root := filepath.Join("testdata", "pathmodule")
	p := func(path string) string { return filepath.Join(root, filepath.FromSlash(path)) }

	candidates, err := findTargetCandidates(root)
	if err != nil {
		t.Fatalf("findTargetCandidates: %v", err)
	}

	tests := []struct {
		name    string
		changes []string
		want    []string
	}{
		{
			name:    "file read by a called module from a sibling directory",
			changes: []string{p("modules/shared/config.tpl")},
			want:    []string{p("envs/app")},
		},
		{
			name:    "file read by a called module from outside the module tree",
			changes: []string{p("docker/base/Dockerfile")},
			want:    []string{p("envs/image")},
		},
		{
			name:    "file read by a root module from outside itself",
			changes: []string{p("data/tags.json")},
			want:    []string{p("envs/worker")},
		},
		{
			name:    "file read through path.root",
			changes: []string{p("envs/shared/policy.json")},
			want:    []string{p("envs/web")},
		},
		{
			name:    "file in a directory given to fileset",
			changes: []string{p("src/lambda/handler.py")},
			want:    []string{p("envs/lambda")},
		},
		{
			name:    "file below the static part of a dynamic path",
			changes: []string{p("templates/prod/user_data.sh")},
			want:    []string{p("envs/server")},
		},
		{
			name:    "tf file in a called module",
			changes: []string{p("modules/app/main.tf")},
			want:    []string{p("envs/app")},
		},
		{
			name:    "tf file in a root module",
			changes: []string{p("envs/network/main.tf")},
			want:    []string{p("envs/network")},
		},
		{
			name:    "file next to a referenced file is not a dependency",
			changes: []string{p("data/other.json")},
			want:    []string{},
		},
		{
			name:    "file outside the static part of a dynamic path is not a dependency",
			changes: []string{p("docs/README.md")},
			want:    []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			targets, err := findTargets(candidates, tt.changes)
			if err != nil {
				t.Fatalf("findTargets: %v", err)
			}
			got := targets.ToSlice()
			slices.Sort(got)
			if !slices.Equal(got, tt.want) {
				t.Errorf("findTargets(%v) = %v, want %v", tt.changes, got, tt.want)
			}
		})
	}
}

func TestGetFileDependenciesSkipsUnresolvablePaths(t *testing.T) {
	dir := filepath.Join("testdata", "pathmodule", "envs", "network")
	deps, err := getFileDependencies(dir, dir)
	if err != nil {
		t.Fatalf("getFileDependencies: %v", err)
	}
	if len(deps) != 0 {
		t.Errorf("getFileDependencies(%q) = %v, want none", dir, deps.ToSlice())
	}
}
