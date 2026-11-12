package tftargets

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclparse"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"
)

// fileFunctions are the Terraform functions whose first argument is a path
// to a file (or, for fileset, a directory) that the configuration reads.
var fileFunctions = map[string]struct{}{
	"file":             {},
	"fileexists":       {},
	"filebase64":       {},
	"filebase64sha256": {},
	"filebase64sha512": {},
	"filemd5":          {},
	"filesha1":         {},
	"filesha256":       {},
	"filesha512":       {},
	"fileset":          {},
	"templatefile":     {},
}

// getFileDependencies returns the paths that the .tf files in moduleDir read
// through file functions such as file() or templatefile(), when the path
// starts from path.module or path.root. rootDir is the directory of the root
// module, which path.root refers to.
//
// A returned path is either a file, or a directory whose contents are all
// treated as dependencies (the directory given to fileset, or the static part
// of a path whose rest is not static).
func getFileDependencies(moduleDir, rootDir string) (Set[string], error) {
	deps := make(Set[string])

	entries, err := os.ReadDir(moduleDir)
	if err != nil {
		return nil, err
	}

	parser := hclparse.NewParser()
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".tf") {
			continue
		}
		path := filepath.Join(moduleDir, entry.Name())
		file, diags := parser.ParseHCLFile(path)
		if diags.HasErrors() {
			continue // Skip files with parse errors
		}
		body, ok := file.Body.(*hclsyntax.Body)
		if !ok {
			continue
		}
		hclsyntax.VisitAll(body, func(node hclsyntax.Node) hcl.Diagnostics {
			call, ok := node.(*hclsyntax.FunctionCallExpr)
			if !ok {
				return nil
			}
			name := strings.TrimPrefix(call.Name, "core::")
			if _, ok := fileFunctions[name]; !ok || len(call.Args) == 0 {
				return nil
			}
			dep, ok := resolveFilePath(call.Args[0], moduleDir, rootDir)
			if !ok {
				slog.Debug("Skipping file reference that does not start from path.module or path.root",
					"function", name,
					"file", path,
					"line", call.Range().Start.Line)
				return nil
			}
			slog.Debug("File dependency detected",
				"function", name,
				"file", path,
				"dependency", dep)
			deps.Add(dep)
			return nil
		})
	}
	return deps, nil
}

// resolveFilePath resolves a path expression that starts from path.module or
// path.root. When part of the expression cannot be resolved statically (a
// variable, a function call, ...), the directory of the static part before it
// is returned instead, so that any change below it is treated as a dependency.
func resolveFilePath(expr hclsyntax.Expression, moduleDir, rootDir string) (string, bool) {
	var parts []hclsyntax.Expression
	switch e := expr.(type) {
	case *hclsyntax.TemplateWrapExpr:
		parts = []hclsyntax.Expression{e.Wrapped}
	case *hclsyntax.TemplateExpr:
		parts = e.Parts
	case *hclsyntax.ScopeTraversalExpr:
		parts = []hclsyntax.Expression{e}
	default:
		return "", false
	}
	if len(parts) == 0 {
		return "", false
	}

	base, ok := pathReference(parts[0], moduleDir, rootDir)
	if !ok {
		return "", false
	}

	var tail strings.Builder
	for _, part := range parts[1:] {
		if lit, ok := part.(*hclsyntax.LiteralValueExpr); ok && lit.Val.Type() == cty.String && lit.Val.IsKnown() && !lit.Val.IsNull() {
			tail.WriteString(lit.Val.AsString())
			continue
		}
		// The rest of the path is not static. Depend on the directory of
		// the static prefix.
		static := tail.String()
		if i := strings.LastIndex(static, "/"); i >= 0 {
			return filepath.Join(base, static[:i]), true
		}
		return filepath.Clean(base), true
	}
	return filepath.Join(base, tail.String()), true
}

// pathReference returns the directory that path.module or path.root refers to.
func pathReference(expr hclsyntax.Expression, moduleDir, rootDir string) (string, bool) {
	traversal, ok := expr.(*hclsyntax.ScopeTraversalExpr)
	if !ok || len(traversal.Traversal) != 2 || traversal.Traversal.RootName() != "path" {
		return "", false
	}
	attr, ok := traversal.Traversal[1].(hcl.TraverseAttr)
	if !ok {
		return "", false
	}
	switch attr.Name {
	case "module":
		return moduleDir, true
	case "root":
		return rootDir, true
	default:
		return "", false
	}
}
