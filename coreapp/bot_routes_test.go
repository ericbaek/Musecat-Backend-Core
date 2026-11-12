package coreapp

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"gopkg.in/yaml.v2"
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

func TestBotOpenAPIRoutesRemainInPublicPaths(t *testing.T) {
	raw, err := os.ReadFile("../docs/openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	spec := string(raw)
	extension := strings.Index(spec, "\nx-musecat-joint-connection:")
	for _, path := range []string{"/bot/v1/arcade/new", "/bot/v1/arcade/game", "/user/bot-keys", "/user/bot-keys/{id}"} {
		at := strings.Index(spec, "\n  "+path+":")
		if at < 0 || (extension >= 0 && at > extension) {
			t.Fatalf("%s is outside public OpenAPI paths", path)
		}
	}
}

func TestBotOpenAPICredentialsMatchTheRuntimeBoundary(t *testing.T) {
	raw, err := os.ReadFile("../docs/openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var spec map[string]any
	if err := yaml.UnmarshalStrict(raw, &spec); err != nil {
		t.Fatal(err)
	}
	paths := spec["paths"].(map[interface{}]interface{})
	for key, value := range paths {
		path := key.(string)
		if !strings.HasPrefix(path, "/bot/v1/") {
			continue
		}
		for method, rawOperation := range value.(map[interface{}]interface{}) {
			operation := rawOperation.(map[interface{}]interface{})
			security, ok := operation["security"].([]interface{})
			if !ok || len(security) != 1 {
				t.Fatalf("%s %s must require only a bot key", method, path)
			}
			alternative := security[0].(map[interface{}]interface{})
			if _, ok := alternative["botKeyAuth"]; !ok || len(alternative) != 1 {
				t.Fatalf("%s %s advertises a non-bot credential", method, path)
			}
		}
	}
}
