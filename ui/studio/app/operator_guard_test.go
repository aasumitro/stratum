package app

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// localOnlyServices touch this machine or only read a project's health, never a
// project's database or broker. Every other service is checked, so a new one cannot
// add a write without either the guard or a deliberate entry here.
var localOnlyServices = map[string]bool{
	"ProjectService":     true,
	"ConnectionService":  true,
	"MonitorService":     true,
	"OperatorLogService": true,
}

// readOnlyPrefixes name the methods that never change anything. A new method with
// any other prefix counts as mutating until it is added here on purpose, so an
// unguarded write cannot slip in unnoticed.
var readOnlyPrefixes = []string{"List", "Get", "Count", "Search", "Export", "Is"}

// mutatingMethods returns the exported service methods that must be guarded, and
// reports each one whose first statement is not the requireOperator check.
func mutatingMethods(t *testing.T) []string {
	t.Helper()
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("list source files: %v", err)
	}
	var names []string
	fset := token.NewFileSet()
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		parsed, err := parser.ParseFile(fset, file, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", file, err)
		}
		for _, decl := range parsed.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv == nil || !fn.Name.IsExported() {
				continue
			}
			service := receiverName(fn)
			if !strings.HasSuffix(service, "Service") || localOnlyServices[service] {
				continue
			}
			if slices.ContainsFunc(readOnlyPrefixes, func(p string) bool { return strings.HasPrefix(fn.Name.Name, p) }) {
				continue
			}
			names = append(names, fn.Name.Name)
			if !startsWithRequireOperator(fn) {
				t.Errorf("%s: %s.%s must start with requireOperator", file, service, fn.Name.Name)
			}
		}
	}
	return names
}

func receiverName(fn *ast.FuncDecl) string {
	if len(fn.Recv.List) != 1 {
		return ""
	}
	expr := fn.Recv.List[0].Type
	if star, ok := expr.(*ast.StarExpr); ok {
		expr = star.X
	}
	if ident, ok := expr.(*ast.Ident); ok {
		return ident.Name
	}
	return ""
}

func startsWithRequireOperator(fn *ast.FuncDecl) bool {
	if fn.Body == nil || len(fn.Body.List) == 0 {
		return false
	}
	guard, ok := fn.Body.List[0].(*ast.IfStmt)
	if !ok {
		return false
	}
	init, ok := guard.Init.(*ast.AssignStmt)
	if !ok || len(init.Rhs) != 1 {
		return false
	}
	call, ok := init.Rhs[0].(*ast.CallExpr)
	if !ok {
		return false
	}
	callee, ok := call.Fun.(*ast.Ident)
	return ok && callee.Name == "requireOperator"
}

func TestMutatingMethodsRequireOperator(t *testing.T) {
	if got := len(mutatingMethods(t)); got != 35 {
		t.Errorf("found %d mutating methods, want 35; a new one also needs a case in the operator smoke test", got)
	}
}
