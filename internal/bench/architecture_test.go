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

func rootConfigUsage(filename string, source []byte) (usesRootConfig, dotImport bool, err error) {
	file, err := parser.ParseFile(token.NewFileSet(), filename, source, 0)
	if err != nil {
		return false, false, err
	}
	aliases := map[string]bool{}
	for _, spec := range file.Imports {
		path, err := strconv.Unquote(spec.Path.Value)
		if err != nil || path != configImportPath {
			continue
		}
		if spec.Name != nil && spec.Name.Name == "." {
			return false, true, nil
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
			usesRootConfig = true
		}
		return true
	})
	return usesRootConfig, false, nil
}

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
		source, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		usesRoot, dotImport, err := rootConfigUsage(name, source)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		if dotImport {
			t.Errorf("%s: dot-import of internal/config bypasses the root-config boundary", name)
		}
		if usesRoot {
			t.Errorf("%s: internal/bench production code must not use root config.Config", name)
		}
	}
}

func TestRootConfigBoundaryAnalyzer(t *testing.T) {
	tests := []struct {
		name     string
		source   string
		wantRoot bool
		wantDot  bool
	}{
		{
			name: "default import",
			source: `package bench
import "github.com/jarymor-ux/kee-route-manager/internal/config"
var forbidden config.Config
`,
			wantRoot: true,
		},
		{
			name: "aliased import",
			source: `package bench
import cfg "github.com/jarymor-ux/kee-route-manager/internal/config"
var forbidden cfg.Config
`,
			wantRoot: true,
		},
		{
			name: "allowed narrow types",
			source: `package bench
import cfg "github.com/jarymor-ux/kee-route-manager/internal/config"
var benchmark cfg.Benchmark
var health cfg.Health
var targets []cfg.Target
`,
		},
		{
			name: "dot import",
			source: `package bench
import . "github.com/jarymor-ux/kee-route-manager/internal/config"
var forbidden Config
`,
			wantDot: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gotRoot, gotDot, err := rootConfigUsage("fixture.go", []byte(tc.source))
			if err != nil {
				t.Fatal(err)
			}
			if gotRoot != tc.wantRoot || gotDot != tc.wantDot {
				t.Fatalf("root=%v dot=%v, want root=%v dot=%v", gotRoot, gotDot, tc.wantRoot, tc.wantDot)
			}
		})
	}
}
