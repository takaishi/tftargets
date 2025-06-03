package tftargets

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclparse"
	"github.com/hashicorp/terraform-config-inspect/tfconfig"
)

type App struct {
	CLI *CLI
}

func New(cli *CLI) *App {
	return &App{
		CLI: cli,
	}
}

func (app *App) Run(ctx context.Context) error {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: getLogLevel(),
	}))
	slog.SetDefault(logger)

	if err := app.listTargets(); err != nil {
		return fmt.Errorf("failed to list targets: %w", err)
	}

	return nil
}

type Set[T comparable] map[T]struct{}

func (s Set[T]) Add(v T) {
	s[v] = struct{}{}
}

func (s Set[T]) Contains(v T) bool {
	_, ok := s[v]
	return ok
}

func (s Set[T]) ToSlice() []T {
	result := make([]T, 0, len(s))
	for v := range s {
		result = append(result, v)
	}
	return result
}

func Contains(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}

func getChangedFilesFromGit(baseDir, baseBranch string) ([]string, error) {
	cmd := exec.Command("git", "fetch", "--depth=1", "origin")
	cmd.Dir = baseDir
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("git fetch failed: %w", err)
	}

	cmd = exec.Command("git", "diff", "--name-only", fmt.Sprintf("origin/%s", baseBranch))
	cmd.Dir = baseDir
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git diff failed: %w", err)
	}

	files := strings.Split(string(output), "\n")
	var result []string
	for _, file := range files {
		if file != "" {
			result = append(result, filepath.Join(baseDir, file))
		}
	}
	return result, nil
}

func getModuleCalls(dir string) (Set[string], error) {
	module, diags := tfconfig.LoadModule(dir)
	if diags.HasErrors() {
		return nil, fmt.Errorf("failed to load module: %v", diags)
	}

	calls := make(Set[string])
	for _, mc := range module.ModuleCalls {
		dependencies, err := getModuleCalls(filepath.Join(dir, mc.Source))
		if err != nil {
			return nil, err
		}
		for _, dependency := range dependencies.ToSlice() {
			calls.Add(dependency)
		}
		calls.Add(filepath.Join(dir, mc.Source))
	}
	return calls, nil
}

func findTargetCandidates(searchPath string) ([]string, error) {
	directories := make(map[string]struct{})
	parser := hclparse.NewParser()

	err := filepath.Walk(searchPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		if strings.HasSuffix(info.Name(), ".tf") {
			file, diags := parser.ParseHCLFile(path)
			if diags.HasErrors() {
				return nil // Skip files with parse errors
			}

			if hasTerraformBlock(file.Body) {
				directories[filepath.Dir(path)] = struct{}{}
			}
		}
		return nil
	})

	if err != nil {
		return nil, err
	}

	var result []string
	for dir := range directories {
		if !strings.Contains(dir, ".terragrunt-cache") {
			result = append(result, dir)
		}
	}
	return result, nil
}

func hasTerraformBlock(body hcl.Body) bool {
	content, _, _ := body.PartialContent(&hcl.BodySchema{
		Blocks: []hcl.BlockHeaderSchema{
			{
				Type:       "terraform",
				LabelNames: nil,
			},
		},
	})

	for _, block := range content.Blocks {
		if block.Type == "terraform" {
			return true
		}
	}
	return false
}

func (app *App) listTargets() error {
	baseBranch := app.CLI.BaseBranch
	baseDir := app.CLI.BaseDir
	searchPath := app.CLI.SearchPath

	slog.Debug("baseBranch", "baseBranch", baseBranch)
	slog.Debug("baseDir", "baseDir", baseDir)
	slog.Debug("searchPath", "searchPath", searchPath)

	targetCandidates, err := findTargetCandidates(filepath.Join(baseDir, searchPath))
	if err != nil {
		return err
	}

	changes, err := getChangedFilesFromGit(baseDir, baseBranch)
	if err != nil {
		return err
	}
	slog.Debug("getChangedFilesFromGit", "changes", changes)

	targets := make(Set[string])
	for _, candidate := range targetCandidates {
		calls, err := getModuleCalls(candidate)
		if err != nil {
			return err
		}
		calls.Add(candidate)

		for _, change := range changes {
			dir := filepath.Dir(change)
			if calls.Contains(dir) {
				targets.Add(candidate)
				break
			}
		}
	}

	jsonOutput, err := json.Marshal(targets.ToSlice())
	if err != nil {
		return fmt.Errorf("failed to marshal paths: %w", err)
	}
	fmt.Printf("%s", jsonOutput)

	return nil
}

func getLogLevel() slog.Level {
	level := os.Getenv("LOG_LEVEL")
	switch level {
	case "DEBUG":
		return slog.LevelDebug
	case "INFO":
		return slog.LevelInfo
	case "WARN":
		return slog.LevelWarn
	case "ERROR":
		return slog.LevelError
	default:
		return slog.LevelInfo // デフォルトはINFO
	}
}
