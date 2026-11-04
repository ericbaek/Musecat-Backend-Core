package coreapp

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	apidocs "github.com/ericbaek/musecat-backend-core/docs"
	"gopkg.in/yaml.v2"
)

func TestOAuthContractsKeepCanonicalSchemasAndResolveReferences(t *testing.T) {
	parse := func(name string, data []byte) map[interface{}]interface{} {
		t.Helper()
		var document map[interface{}]interface{}
		if err := yaml.UnmarshalStrict(data, &document); err != nil {
			t.Fatalf("%s is not strict YAML: %v", name, err)
		}
		if document["openapi"] != "3.1.0" {
			t.Fatalf("%s has an unexpected OpenAPI version", name)
		}
		var walk func(interface{})
		walk = func(value interface{}) {
			switch node := value.(type) {
			case map[interface{}]interface{}:
				if ref, ok := node["$ref"].(string); ok && strings.HasPrefix(ref, "#/") {
					var target interface{} = document
					for _, part := range strings.Split(ref[2:], "/") {
						key := strings.ReplaceAll(strings.ReplaceAll(part, "~1", "/"), "~0", "~")
						mapping, ok := target.(map[interface{}]interface{})
						if !ok || mapping[key] == nil {
							t.Fatalf("%s has an unresolved reference: %s", name, ref)
						}
						target = mapping[key]
					}
				}
				for _, child := range node {
					walk(child)
				}
			case []interface{}:
				for _, child := range node {
					walk(child)
				}
			}
		}
		walk(document)
		return document
	}
	main := parse("canonical contract", apidocs.OpenAPISpec())
	developer := parse("developer contract", apidocs.DeveloperOpenAPI())
	mainSchemas := main["components"].(map[interface{}]interface{})["schemas"].(map[interface{}]interface{})
	developerSchemas := developer["components"].(map[interface{}]interface{})["schemas"].(map[interface{}]interface{})
	for name, schema := range developerSchemas {
		if !reflect.DeepEqual(schema, mainSchemas[name]) {
			t.Errorf("developer schema %s drifted from the canonical domain contract", name)
		}
	}
	for path := range developer["paths"].(map[interface{}]interface{}) {
		name := fmt.Sprint(path)
		if strings.HasPrefix(name, "/native") || strings.HasPrefix(name, "/bot/") || strings.HasPrefix(name, "/developer/apps") || strings.HasPrefix(name, "/partner/") {
			t.Errorf("non-delegated management resource in external OAuth contract: %s", name)
		}
	}
}
