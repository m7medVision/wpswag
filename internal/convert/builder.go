package convert

import (
	"strings"

	"sensepost.com/wpswag/internal/oas"
)

// Builder incrementally builds an OpenAPI 3.0.3 spec from WordPress routes.
type Builder struct {
	spec  *oas.Spec
	stats Stats
}

// Stats holds conversion statistics.
type Stats struct {
	Routes    int
	Endpoints int
	Ops       int
}

// NewBuilder creates a new spec builder with the given metadata.
func NewBuilder(title, description, serverURL string) *Builder {
	if title == "" {
		title = "WordPress REST"
	}
	if serverURL == "" {
		serverURL = "https://example.com/wp-json"
	}
	return &Builder{
		spec: &oas.Spec{
			OpenAPI: "3.0.3",
			Info: oas.Info{
				Title:       title,
				Description: description,
				Version:     "1.0.0",
			},
			Servers: []oas.Server{{URL: serverURL}},
			Paths:   map[string]oas.PathItem{},
		},
	}
}

// AddPath sets a path item on the spec.
func (b *Builder) AddPath(path string, item oas.PathItem) {
	b.spec.Paths[path] = item
}

// GetPath returns the current path item for a given path.
func (b *Builder) GetPath(path string) oas.PathItem {
	return b.spec.Paths[path]
}

// AddSchema stores a reusable schema component on the spec.
func (b *Builder) AddSchema(name string, schema oas.Schema) {
	if b.spec.Components == nil {
		b.spec.Components = &oas.Components{Schemas: map[string]oas.Schema{}}
	}
	if b.spec.Components.Schemas == nil {
		b.spec.Components.Schemas = map[string]oas.Schema{}
	}
	if _, exists := b.spec.Components.Schemas[name]; exists {
		return
	}
	b.spec.Components.Schemas[name] = schema
}

// IncrementRoutes increments the route counter.
func (b *Builder) IncrementRoutes() {
	b.stats.Routes++
}

// IncrementEndpoints increments the endpoint counter.
func (b *Builder) IncrementEndpoints() {
	b.stats.Endpoints++
}

// IncrementOps increments the operations counter.
func (b *Builder) IncrementOps() {
	b.stats.Ops++
}

// SetMethodOperation sets an operation on a path item by HTTP method.
func SetMethodOperation(pi *oas.PathItem, method string, op *oas.Operation) {
	switch strings.ToUpper(method) {
	case "GET":
		pi.Get = op
	case "POST":
		pi.Post = op
	case "PUT":
		pi.Put = op
	case "PATCH":
		pi.Patch = op
	case "DELETE":
		pi.Delete = op
	case "OPTIONS":
		pi.Options = op
	case "HEAD":
		pi.Head = op
	}
}

// IsPathItemEmpty returns true if the path item has no operations.
func IsPathItemEmpty(pi oas.PathItem) bool {
	return pi.Get == nil && pi.Post == nil && pi.Put == nil &&
		pi.Patch == nil && pi.Delete == nil && pi.Options == nil && pi.Head == nil
}

// Build returns the final spec and stats.
func (b *Builder) Build() (*oas.Spec, *Stats) {
	return b.spec, &b.stats
}
