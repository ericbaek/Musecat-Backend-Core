package coreapp

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestOpenAPIDocumentsBotAndKeyManagementRoutes(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	root := filepath.Dir(filepath.Dir(file))
	parsed, err := parser.ParseFile(token.NewFileSet(), filepath.Join(root, "coreapp", "bot_routes.go"), nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	declaration := routeFunction(t, parsed, "registerBotRoutes")
	operations := directRouteOperations(declaration.Body, routerGroupPrefixes(declaration.Body))
	spec, err := os.ReadFile(filepath.Join(root, "docs", "openapi.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	documented := documentedOperations(string(spec))
	if !operations["POST /user/bot-keys"] || !operations["POST /bot/v1/arcade/new"] {
		t.Fatal("bot and key routes were not inspected")
	}
	for operation := range operations {
		if !documented[operation] {
			t.Errorf("OpenAPI is missing %s", operation)
		}
	}
}
