// Package api holds the OpenAPI specification of the customer API, embedded
// so the gateway can serve it.
package api

import _ "embed"

// OpenAPI is the customer API specification in YAML.
//
//go:embed openapi.yaml
var OpenAPI []byte
