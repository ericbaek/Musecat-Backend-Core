package coreapp

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

func TestOpenAPIDocumentsDirectCoreRoutes(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate test source")
	}
	root := filepath.Dir(filepath.Dir(thisFile))
	sourcePath := filepath.Join(root, "coreapp", "coreapp.go")
	source, err := os.ReadFile(sourcePath)
	if err != nil {
		t.Fatalf("read router source: %v", err)
	}
	file, err := parser.ParseFile(token.NewFileSet(), sourcePath, source, 0)
	if err != nil {
		t.Fatalf("parse router source: %v", err)
	}
	spec, err := os.ReadFile(filepath.Join(root, "docs", "openapi.yaml"))
	if err != nil {
		t.Fatalf("read OpenAPI spec: %v", err)
	}

	operations := documentedOperations(string(spec))
	configure := configureFunction(t, file)
	prefixes := routerGroupPrefixes(configure.Body)
	for operation := range directRouteOperations(configure.Body, prefixes) {
		if !operations[operation] {
			t.Errorf("OpenAPI is missing %s", operation)
		}
	}
}

func configureFunction(t *testing.T, file *ast.File) *ast.FuncDecl {
	t.Helper()
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if ok && function.Name.Name == "Configure" {
			return function
		}
	}
	t.Fatal("Configure function not found")
	return nil
}

func routerGroupPrefixes(file ast.Node) map[string]string {
	prefixes := map[string]string{}
	ast.Inspect(file, func(node ast.Node) bool {
		assignment, ok := node.(*ast.AssignStmt)
		if !ok || len(assignment.Lhs) != 1 || len(assignment.Rhs) != 1 {
			return true
		}
		name, ok := assignment.Lhs[0].(*ast.Ident)
		if !ok {
			return true
		}
		call, ok := groupCall(assignment.Rhs[0])
		if !ok {
			return true
		}
		if path, ok := stringArgument(call, 0); ok {
			prefixes[name.Name] = path
		}
		return true
	})
	return prefixes
}

func groupCall(expression ast.Expr) (*ast.CallExpr, bool) {
	call, ok := expression.(*ast.CallExpr)
	if !ok {
		return nil, false
	}
	if methodName(call) == "Group" {
		return call, true
	}
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return nil, false
	}
	return groupCall(selector.X)
}

func directRouteOperations(file ast.Node, prefixes map[string]string) map[string]bool {
	operations := map[string]bool{}
	ast.Inspect(file, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		method := methodName(call)
		if method != "GET" && method != "POST" && method != "PUT" && method != "DELETE" {
			return true
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		path, ok := stringArgument(call, 0)
		if !ok {
			return true
		}
		if path == "/nearby" && len(call.Args) > 1 && isNil(call.Args[1]) {
			// This placeholder has no handler and is deliberately not public API.
			return true
		}
		prefix := ""
		switch receiver := selector.X.(type) {
		case *ast.Ident:
			prefix = prefixes[receiver.Name]
		case *ast.SelectorExpr:
			// Direct calls use se.Router.METHOD(...).
			if receiver.Sel.Name != "Router" {
				return true
			}
		default:
			return true
		}
		operations[method+" "+prefix+path] = true
		return true
	})
	return operations
}

func isNil(expression ast.Expr) bool {
	identifier, ok := expression.(*ast.Ident)
	return ok && identifier.Name == "nil"
}

func methodName(call *ast.CallExpr) string {
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return ""
	}
	return selector.Sel.Name
}

func stringArgument(call *ast.CallExpr, index int) (string, bool) {
	if len(call.Args) <= index {
		return "", false
	}
	literal, ok := call.Args[index].(*ast.BasicLit)
	if !ok || literal.Kind != token.STRING {
		return "", false
	}
	return strings.Trim(literal.Value, `"`), true
}

func documentedOperations(spec string) map[string]bool {
	paths := regexp.MustCompile(`(?m)^  (/[^:\n]+):$`).FindAllStringSubmatchIndex(spec, -1)
	operations := map[string]bool{}
	for index, match := range paths {
		end := len(spec)
		if index+1 < len(paths) {
			end = paths[index+1][0]
		}
		path := spec[match[2]:match[3]]
		section := spec[match[0]:end]
		for _, method := range []string{"GET", "POST", "PUT", "DELETE"} {
			if regexp.MustCompile(`(?m)^    ` + strings.ToLower(method) + `:$`).MatchString(section) {
				operations[method+" "+path] = true
			}
		}
	}
	return operations
}
