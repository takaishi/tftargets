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
	// A base commit that is already in the local repository needs no fetch.
	// Skipping it avoids requiring credentials and keeps a full clone from
	// becoming shallow.
	if baseCommitSha != "" && commitExists(baseDir, baseCommitSha) {
		slog.Debug("base commit exists locally; skipping git fetch", "baseCommitSha", baseCommitSha)
	} else {
		cmd := exec.Command("git", "fetch", "--depth=1", "origin")
		cmd.Dir = baseDir
		if err := cmd.Run(); err != nil {
			return nil, fmt.Errorf("git fetch failed: %w", err)
		}
	}

	var diffTarget string
	if baseCommitSha != "" {
		diffTarget = baseCommitSha
	} else {
		diffTarget = fmt.Sprintf("origin/%s", baseBranch)
	}

	// -z prints paths verbatim, separated by NUL. Without it, Git quotes paths
	// that contain non-ASCII or control characters (core.quotePath).
	// --no-renames lists both the old and the new path of a renamed file, so
	// a file moved out of a directory still counts as a change there.
	cmd := exec.Command("git", "diff", "--name-only", "-z", "--no-renames", diffTarget)
	cmd.Dir = baseDir
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git diff failed: %w", err)
	}

	files := strings.Split(string(output), "\x00")
	var result []string
	for _, file := range files {
		if file != "" {
			result = append(result, filepath.Join(baseDir, file))
		}
	}
	return result, nil
}

// commitExists reports whether rev resolves to a commit in the repository at dir.
func commitExists(dir, rev string) bool {
	cmd := exec.Command("git", "cat-file", "-e", rev+"^{commit}")
	cmd.Dir = dir
	return cmd.Run() == nil
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
		if !inTerragruntCache(dir) {
			result = append(result, dir)
		}
	}
	return result, nil
}

// inTerragruntCache reports whether path is in a Terragrunt cache, that is,
// whether one of its elements is exactly ".terragrunt-cache". A directory
// whose name merely contains that string, such as "foo.terragrunt-cache",
// is not a cache.
func inTerragruntCache(path string) bool {
	for _, elem := range strings.Split(filepath.Clean(path), string(filepath.Separator)) {
		if elem == ".terragrunt-cache" {
			return true
		}
	}
	return false
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

// findTargets returns the candidates that are affected by the changed files.
// A candidate is affected when a changed file is in the candidate directory,
// in a local module it calls (transitively), or is a file that one of those
// modules reads through path.module or path.root.
func findTargets(targetCandidates []string, changes []string) (Set[string], error) {
	// First, collect all module directories and file dependencies for each candidate
	candidateModules := make(map[string]Set[string])
	for _, candidate := range targetCandidates {
		calls, err := getModuleCalls(candidate)
		if err != nil {
			return nil, err
		}
		calls.Add(candidate)

		dependencies := make(Set[string])
		for module := range calls {
			dependencies.Add(module)
			files, err := getFileDependencies(module, candidate)
			if err != nil {
				return nil, err
			}
			for file := range files {
				dependencies.Add(file)
			}
		}
		candidateModules[candidate] = dependencies
	}
	slog.Debug("candidateModules", "candidateModules", candidateModules)

	// Then check if any changed files are within module directories or are file dependencies
	targets := make(Set[string])
	for _, change := range changes {
		for candidate, modules := range candidateModules {
			for module := range modules {
				// Check if the changed file is this path or within this directory
				if strings.HasPrefix(change, module+string(filepath.Separator)) || change == module {
					targets.Add(candidate)
					break
				}
			}
		}
	}
	slog.Debug("targets", "targets", targets)
	return targets, nil
}

// findChangedDirs returns the directories under searchRoot that contain a
// changed .tf file and still exist, whether or not they have a terraform block.
func findChangedDirs(searchRoot string, changes []string) Set[string] {
	dirs := make(Set[string])
	for _, change := range changes {
		if !strings.HasSuffix(change, ".tf") {
			continue
		}
		dir := filepath.Dir(change)
		if inTerragruntCache(dir) {
			continue
		}
		rel, err := filepath.Rel(searchRoot, dir)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			continue
		}
		if info, err := os.Stat(dir); err != nil || !info.IsDir() {
			continue
		}
		dirs.Add(dir)
	}
	return dirs
}

func collectTargets(opts *GlobalOptions) (Set[string], error) {
	slog.Debug("options", "baseBranch", opts.BaseBranch, "baseCommitSha", opts.BaseCommitSha,
		"baseDir", opts.BaseDir, "searchPath", opts.SearchPath, "includeChangedDirs", opts.IncludeChangedDirs)

	searchRoot := filepath.Join(opts.BaseDir, opts.SearchPath)
	targetCandidates, err := findTargetCandidates(searchRoot)
	if err != nil {
		return nil, err
	}

	changes, err := getChangedFilesFromGit(opts.BaseDir, opts.BaseBranch, opts.BaseCommitSha)
	if err != nil {
		return nil, err
	}
	slog.Debug("getChangedFilesFromGit", "changes", changes)

	targets, err := findTargets(targetCandidates, changes)
	if err != nil {
		return nil, err
	}

	if opts.IncludeChangedDirs {
		for dir := range findChangedDirs(searchRoot, changes) {
			targets.Add(dir)
		}
	}
	return targets, nil
}

func (app *App) listTargets() error {
	targets, err := collectTargets(app.CLI.GlobalOptions)
	if err != nil {
		return err
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
