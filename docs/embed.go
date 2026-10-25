// Package apidocs exposes the canonical API specification embedded in the
// Backend Core module.
package apidocs

import _ "embed"

//go:embed openapi.yaml
var openAPISpec []byte

// OpenAPISpec returns a copy of the canonical OpenAPI document.
func OpenAPISpec() []byte {
	return append([]byte(nil), openAPISpec...)
}
