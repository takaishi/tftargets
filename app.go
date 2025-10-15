package tftargets

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclparse"
	"github.com/hashicorp/terraform-config-inspect/tfconfig"
)

// ModuleSourceType represents the type of module source
type ModuleSourceType string

const (
	ModuleSourceTypeLocal     ModuleSourceType = "local"
	ModuleSourceTypeRegistry  ModuleSourceType = "registry"
	ModuleSourceTypeGit       ModuleSourceType = "git"
	ModuleSourceTypeGitHub    ModuleSourceType = "github"
	ModuleSourceTypeHTTP      ModuleSourceType = "http"
	ModuleSourceTypeS3        ModuleSourceType = "s3"
	ModuleSourceTypeGCS       ModuleSourceType = "gcs"
	ModuleSourceTypeMercurial ModuleSourceType = "mercurial"
	ModuleSourceTypeUnknown   ModuleSourceType = "unknown"
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

// DetectModuleSourceType detects the type of module source based on the source string
func DetectModuleSourceType(source string) ModuleSourceType {
	// Local path (starts with ./ or ../)
	if strings.HasPrefix(source, "./") || strings.HasPrefix(source, "../") {
		return ModuleSourceTypeLocal
	}

	// Git with explicit protocol
	if strings.HasPrefix(source, "git::") {
		return ModuleSourceTypeGit
	}

	// Mercurial with explicit protocol
	if strings.HasPrefix(source, "hg::") {
		return ModuleSourceTypeMercurial
	}

	// S3 bucket
	if strings.HasPrefix(source, "s3::") {
		return ModuleSourceTypeS3
	}

	// GCS bucket
	if strings.HasPrefix(source, "gcs::") {
		return ModuleSourceTypeGCS
	}

	// HTTP/HTTPS URL
	if strings.HasPrefix(source, "http://") || strings.HasPrefix(source, "https://") {
		return ModuleSourceTypeHTTP
	}

	// GitHub repository (github.com/...)
	if strings.HasPrefix(source, "github.com/") {
		return ModuleSourceTypeGitHub
	}

	// Git repository (git@...)
	if strings.HasPrefix(source, "git@") {
		return ModuleSourceTypeGit
	}

	// Terraform Registry (namespace/name/provider format)
	// Pattern: alphanumeric characters, hyphens, underscores, and forward slashes
	// Also supports subdirectories with // (e.g., terraform-aws-modules/iam/aws//modules/iam-account)
	registryPattern := regexp.MustCompile(`^[a-zA-Z0-9_-]+/[a-zA-Z0-9_-]+/[a-zA-Z0-9_-]+(//.*)?$`)
	if registryPattern.MatchString(source) {
		return ModuleSourceTypeRegistry
	}

	// If none of the above patterns match, it's unknown
	return ModuleSourceTypeUnknown
}

func getChangedFilesFromGit(baseDir, baseBranch, baseCommitSha string) ([]string, error) {
	cmd := exec.Command("git", "fetch", "--depth=1", "origin")
	cmd.Dir = baseDir
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("git fetch failed: %w", err)
	}

	var diffTarget string
	if baseCommitSha != "" {
		diffTarget = baseCommitSha
	} else {
		diffTarget = fmt.Sprintf("origin/%s", baseBranch)
	}

	cmd = exec.Command("git", "diff", "--name-only", diffTarget)
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
		sourceType := DetectModuleSourceType(mc.Source)
		slog.Debug("Module source detected",
			"module", mc.Name,
			"source", mc.Source,
			"type", sourceType)

		// Only process local modules recursively
		if sourceType == ModuleSourceTypeLocal {
			dependencies, err := getModuleCalls(filepath.Join(dir, mc.Source))
			if err != nil {
				return nil, err
			}
			for _, dependency := range dependencies.ToSlice() {
				calls.Add(dependency)
			}
			calls.Add(filepath.Join(dir, mc.Source))
		} else {
			// For non-local modules, just add the current directory
			calls.Add(dir)
		}
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
	baseCommitSha := app.CLI.BaseCommitSha
	baseDir := app.CLI.BaseDir
	searchPath := app.CLI.SearchPath

	slog.Debug("baseBranch", "baseBranch", baseBranch)
	slog.Debug("baseCommitSha", "baseCommitSha", baseCommitSha)
	slog.Debug("baseDir", "baseDir", baseDir)
	slog.Debug("searchPath", "searchPath", searchPath)

	targetCandidates, err := findTargetCandidates(filepath.Join(baseDir, searchPath))
	if err != nil {
		return err
	}

	changes, err := getChangedFilesFromGit(baseDir, baseBranch, baseCommitSha)
	if err != nil {
		return err
	}
	slog.Debug("getChangedFilesFromGit", "changes", changes)

	// First, collect all module directories for each candidate
	candidateModules := make(map[string]Set[string])
	for _, candidate := range targetCandidates {
		calls, err := getModuleCalls(candidate)
		if err != nil {
			return err
		}
		calls.Add(candidate)
		candidateModules[candidate] = calls
	}
	slog.Debug("candidateModules", "candidateModules", candidateModules)

	// Then check if any changed files are within module directories
	targets := make(Set[string])
	for _, change := range changes {
		for candidate, modules := range candidateModules {
			for module := range modules {
				// Check if the changed file is within this module directory or its subdirectories
				if strings.HasPrefix(change, module+string(filepath.Separator)) || change == module {
					targets.Add(candidate)
					break
				}
			}
		}
	}
	slog.Debug("targets", "targets", targets)

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
