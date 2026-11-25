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
	if fn.Recv == nil || len(fn.Recv.List) != 1 {
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

// refusesAfter reports whether the statement holding the call at pos is followed by
// an if statement on err that only returns.
func refusesAfter(statements []ast.Stmt, pos token.Pos) bool {
	for i, statement := range statements[:len(statements)-1] {
		if pos < statement.Pos() || pos > statement.End() {
			continue
		}
		check, ok := statements[i+1].(*ast.IfStmt)
		if !ok || len(check.Body.List) != 1 {
			return false
		}
		condition, ok := check.Cond.(*ast.BinaryExpr)
		if !ok || condition.Op != token.NEQ {
			return false
		}
		name, ok := condition.X.(*ast.Ident)
		_, returns := check.Body.List[0].(*ast.ReturnStmt)
		return ok && name.Name == "err" && returns
	}
	return false
}

// Every broker change checks the broker login before it records or sends anything: a
// check that comes later leaves a row, or a purge, behind a refused login.
func TestBrokerChangesCheckTheBrokerLoginFirst(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("list source files: %v", err)
	}
	var methods []*ast.FuncDecl
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		parsed, err := parser.ParseFile(token.NewFileSet(), file, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", file, err)
		}
		for _, decl := range parsed.Decls {
			if fn, ok := decl.(*ast.FuncDecl); ok && receiverName(fn) == "DLQService" &&
				fn.Name.IsExported() && startsWithRequireOperator(fn) {
				methods = append(methods, fn)
			}
		}
	}
	if len(methods) != 2 {
		t.Errorf("found %d broker changes, want 2", len(methods))
	}
	for _, fn := range methods {
		first := map[string]token.Pos{}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			if selector, ok := call.Fun.(*ast.SelectorExpr); ok && first[selector.Sel.Name] == token.NoPos {
				first[selector.Sel.Name] = call.Pos()
			}
			return true
		})
		guard := first["requireOwnBrokerLogin"]
		if guard == token.NoPos || guard > first["recordAction"] || guard > first["request"] {
			t.Errorf("%s must call requireOwnBrokerLogin before it records or sends anything", fn.Name.Name)
		}
		if !refusesAfter(fn.Body.List, guard) {
			t.Errorf("%s must return when requireOwnBrokerLogin fails", fn.Name.Name)
		}
	}
}

func TestMutatingMethodsRequireOperator(t *testing.T) {
	if got := len(mutatingMethods(t)); got != 35 {
		t.Errorf("found %d mutating methods, want 35; a new one also needs a case in the operator smoke test", got)
	}
}
