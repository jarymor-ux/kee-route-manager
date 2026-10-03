package bench

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strconv"
	"strings"
	"testing"
)

const configImportPath = "github.com/jarymor-ux/kee-route-manager/internal/config"

func TestProductionDoesNotUseRootConfig(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(token.NewFileSet(), name, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		aliases := map[string]bool{}
		for _, spec := range file.Imports {
			path, err := strconv.Unquote(spec.Path.Value)
			if err != nil || path != configImportPath {
				continue
			}
			if spec.Name != nil && spec.Name.Name == "." {
				t.Fatalf("%s: dot-import of internal/config bypasses the root-config boundary", name)
			}
			alias := "config"
			if spec.Name != nil {
				alias = spec.Name.Name
			}
			if alias != "_" {
				aliases[alias] = true
			}
		}
		ast.Inspect(file, func(node ast.Node) bool {
			selector, ok := node.(*ast.SelectorExpr)
			if !ok || selector.Sel.Name != "Config" {
				return true
			}
			pkg, ok := selector.X.(*ast.Ident)
			if ok && aliases[pkg.Name] {
				t.Errorf("%s: internal/bench production code must not use root config.Config", name)
			}
			return true
		})
	}
}

func TestRootConfigBoundaryDetectsAliasedImport(t *testing.T) {
	source := `package bench
import cfg "github.com/jarymor-ux/kee-route-manager/internal/config"
var forbidden cfg.Config
`
	file, err := parser.ParseFile(token.NewFileSet(), "fixture.go", source, 0)
	if err != nil {
		t.Fatal(err)
	}
	alias := ""
	for _, spec := range file.Imports {
		path, _ := strconv.Unquote(spec.Path.Value)
		if path == configImportPath && spec.Name != nil {
			alias = spec.Name.Name
		}
	}
	found := false
	ast.Inspect(file, func(node ast.Node) bool {
		selector, ok := node.(*ast.SelectorExpr)
		if !ok || selector.Sel.Name != "Config" {
			return true
		}
		pkg, ok := selector.X.(*ast.Ident)
		found = found || ok && pkg.Name == alias
		return true
	})
	if !found {
		t.Fatal("architecture guard did not detect aliased root config.Config reference")
	}
}
