# tftargets

A tool that analyzes Terraform configurations and identifies directories that need to be executed based on Git changes.

## What it does

`tftargets` scans your repository for Terraform configurations and determines which directories contain changes that require Terraform execution. It:

1. Finds all directories containing `.tf` files with `terraform` blocks
2. Analyzes module dependencies within those directories
3. Compares against Git changes from a base branch
4. Outputs a JSON array of directories that need attention

This is particularly useful in CI/CD pipelines where you want to run Terraform only on changed modules rather than all modules.

## Install

### Go Install
```bash
go install github.com/takaishi/tftargets/cmd/tftargets@latest
```

### GitHub Action
```yaml
- uses: takaishi/tftargets@v1
  with:
    version: latest
```

## Usage

### Basic Usage
```bash
tftargets
```

### Options
```bash
tftargets --base-branch main --base-dir . --search-path .
```

#### Flags
- `--base-branch`: Base branch for Git comparison (default: "main")
- `--base-dir`: Base directory for the repository (default: ".")
- `--search-path`: Path to search for Terraform files (default: ".")
- `--version`: Show version information

### Environment Variables
- `LOG_LEVEL`: Set logging level (DEBUG, INFO, WARN, ERROR)

### Example Output
```json
["/path/to/terraform/module1", "/path/to/terraform/module2"]
```

## Use Cases

- **CI/CD Optimization**: Only run Terraform on changed modules
- **Selective Planning**: Generate plans only for affected infrastructure
- **Module Dependency Analysis**: Understand which modules are impacted by changes
