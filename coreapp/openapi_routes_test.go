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

	apidocs "github.com/ericbaek/musecat-backend-core/docs"
	"gopkg.in/yaml.v2"
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
	directOps := map[string]bool{}
	for _, name := range []string{"registerSessionRoutes", "registerAccountRoutes"} {
		routeDecl := routeFunction(t, file, name)
		prefixes := routerGroupPrefixes(routeDecl.Body)
		for op := range directRouteOperations(routeDecl.Body, prefixes) {
			directOps[op] = true
		}
	}
	if len(directOps) == 0 {
		t.Fatal("expected non-empty direct route operations")
	}
	for operation := range directOps {
		if !operations[operation] {
			t.Errorf("OpenAPI is missing %s", operation)
		}
	}
}

func routeFunction(t *testing.T, file *ast.File, name string) *ast.FuncDecl {
	t.Helper()
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if ok && function.Name.Name == name {
			return function
		}
	}
	t.Fatalf("route declaration %s not found", name)
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
		if method != "GET" && method != "POST" && method != "PUT" && method != "PATCH" && method != "DELETE" {
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
		for _, method := range []string{"GET", "POST", "PUT", "PATCH", "DELETE"} {
			if regexp.MustCompile(`(?m)^    ` + strings.ToLower(method) + `:$`).MatchString(section) {
				operations[method+" "+path] = true
			}
		}
	}
	return operations
}

func TestOpenAPIDocumentsMessagingRoutes(t *testing.T) {
	_, thisFile, _, _ := runtime.Caller(0)
	root := filepath.Dir(filepath.Dir(thisFile))
	source, err := os.ReadFile(filepath.Join(root, "handlers", "messaging", "routes.go"))
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := parser.ParseFile(token.NewFileSet(), "routes.go", source, 0)
	if err != nil {
		t.Fatal(err)
	}
	spec, err := os.ReadFile(filepath.Join(root, "docs", "openapi.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	function := routeFunction(t, parsed, "RegisterRoutes")
	operations := documentedOperations(string(spec))
	for operation := range directRouteOperations(function.Body, routerGroupPrefixes(function.Body)) {
		if !operations[operation] {
			t.Errorf("Messaging OpenAPI missing %s", operation)
		}
	}
}

func TestOpenAPINativeLoginPathsAndSecurity(t *testing.T) {
	var parsed map[string]any
	if err := yaml.UnmarshalStrict(apidocs.OpenAPISpec(), &parsed); err != nil {
		t.Fatal(err)
	}
	type operation struct {
		Security  []map[string][]string `yaml:"security"`
		Responses map[string]any        `yaml:"responses"`
	}
	var contract struct {
		Paths map[string]struct {
			Get  *operation `yaml:"get"`
			Post *operation `yaml:"post"`
		} `yaml:"paths"`
		Components struct {
			SecuritySchemes map[string]struct {
				Type         string `yaml:"type"`
				Scheme       string `yaml:"scheme"`
				BearerFormat string `yaml:"bearerFormat"`
			} `yaml:"securitySchemes"`
			Schemas map[string]any `yaml:"schemas"`
		} `yaml:"components"`
	}
	if err := yaml.Unmarshal(apidocs.OpenAPISpec(), &contract); err != nil {
		t.Fatal(err)
	}
	if scheme := contract.Components.SecuritySchemes["nativeSession"]; scheme.Type != "http" || scheme.Scheme != "bearer" || scheme.BearerFormat != "opaque" {
		t.Fatal("native opaque security scheme is missing from components")
	}
	if scheme := contract.Components.SecuritySchemes["nativeWebClientBasic"]; scheme.Type != "http" || scheme.Scheme != "basic" {
		t.Fatal("confidential web handoff scheme missing")
	}
	if _, ok := contract.Components.Schemas["NativeCredentials"]; !ok {
		t.Fatal("native credential schema is missing from components")
	}
	for _, route := range []struct {
		path, method, security string
		statuses               []string
	}{
		{"/native/session/web/code", "post", "nativeSession", []string{"200", "400", "401", "403", "413"}},
		{"/native/session/web/exchange", "post", "nativeWebClientBasic", []string{"200", "400", "401", "403", "413"}},
		{"/native/session/code", "post", "bearerAuth", []string{"200", "401", "403", "413"}},
		{"/native/session/exchange", "post", "", []string{"200", "401", "403", "413"}},
		{"/native/session/refresh", "post", "", []string{"200", "401", "403", "413"}},
		{"/native/session/revoke", "post", "", []string{"204", "400", "413"}},
		{"/native/user/me", "get", "nativeSession", []string{"200", "401", "403"}},
	} {
		op := contract.Paths[route.path].Post
		if route.method == "get" {
			op = contract.Paths[route.path].Get
		}
		if op == nil {
			t.Fatalf("%s %s is not in the top-level paths object", route.method, route.path)
		}
		if route.security == "" {
			if len(op.Security) != 0 {
				t.Errorf("%s requires unintended bearer credentials", route.path)
			}
		} else if len(op.Security) != 1 {
			t.Errorf("%s missing its credential boundary", route.path)
		} else if _, ok := op.Security[0][route.security]; !ok {
			t.Errorf("%s has the wrong credential boundary", route.path)
		}
		for _, status := range route.statuses {
			if _, ok := op.Responses[status]; !ok {
				t.Errorf("%s missing status %s", route.path, status)
			}
		}
	}
}
