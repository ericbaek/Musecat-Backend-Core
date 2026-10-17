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

//go:embed developer-openapi.yaml
var developerSpec []byte

//go:embed developer-api.md
var developerGuide []byte

//go:embed developer.html
var developerHTML []byte

func DeveloperOpenAPI() []byte { return append([]byte(nil), developerSpec...) }
func DeveloperGuide() []byte   { return append([]byte(nil), developerGuide...) }
func DeveloperHTML() []byte    { return append([]byte(nil), developerHTML...) }

//go:embed partner-integration-spec.md
var partnerIntegrationSpec []byte

func PartnerIntegrationSpec() []byte { return append([]byte(nil), partnerIntegrationSpec...) }
