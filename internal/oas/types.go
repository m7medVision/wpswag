package oas

// Spec represents an OpenAPI 3.0.3 specification.
type Spec struct {
	OpenAPI    string              `json:"openapi"`
	Info       Info                `json:"info"`
	Servers    []Server            `json:"servers,omitempty"`
	Paths      map[string]PathItem `json:"paths"`
	Components *Components         `json:"components,omitempty"`
}

// Components holds reusable OpenAPI components.
type Components struct {
	Schemas map[string]Schema `json:"schemas,omitempty"`
}

// Info holds API metadata.
type Info struct {
	Title       string `json:"title"`
	Description string `json:"description,omitempty"`
	Version     string `json:"version"`
}

// Server defines a server URL.
type Server struct {
	URL string `json:"url"`
}

// PathItem groups operations for a single path.
type PathItem struct {
	Get     *Operation `json:"get,omitempty"`
	Post    *Operation `json:"post,omitempty"`
	Put     *Operation `json:"put,omitempty"`
	Patch   *Operation `json:"patch,omitempty"`
	Delete  *Operation `json:"delete,omitempty"`
	Options *Operation `json:"options,omitempty"`
	Head    *Operation `json:"head,omitempty"`
}

// Operation represents a single API operation.
type Operation struct {
	OperationID string              `json:"operationId,omitempty"`
	Summary     string              `json:"summary,omitempty"`
	Tags        []string            `json:"tags,omitempty"`
	Parameters  []Parameter         `json:"parameters,omitempty"`
	RequestBody *RequestBody        `json:"requestBody,omitempty"`
	Responses   map[string]Response `json:"responses"`
}

// Parameter defines an operation parameter.
type Parameter struct {
	Name        string `json:"name"`
	In          string `json:"in"`
	Required    bool   `json:"required"`
	Description string `json:"description,omitempty"`
	Schema      Schema `json:"schema"`
}

// RequestBody defines the request body for an operation.
type RequestBody struct {
	Required bool             `json:"required"`
	Content  map[string]Media `json:"content"`
}

// Media represents a media type with its schema.
type Media struct {
	Schema Schema `json:"schema"`
}

// Response defines an operation response.
type Response struct {
	Description string           `json:"description"`
	Content     map[string]Media `json:"content,omitempty"`
}

// Schema defines a JSON Schema compatible object.
type Schema struct {
	Ref                  string            `json:"$ref,omitempty"`
	Title                string            `json:"title,omitempty"`
	Type                 any               `json:"type,omitempty"`
	Format               string            `json:"format,omitempty"`
	Enum                 []any             `json:"enum,omitempty"`
	Default              any               `json:"default,omitempty"`
	Items                *Schema           `json:"items,omitempty"`
	Properties           map[string]Schema `json:"properties,omitempty"`
	Required             []string          `json:"required,omitempty"`
	Description          string            `json:"description,omitempty"`
	ReadOnly             bool              `json:"readOnly,omitempty"`
	Nullable             bool              `json:"nullable,omitempty"`
	Minimum              any               `json:"minimum,omitempty"`
	Maximum              any               `json:"maximum,omitempty"`
	ExclusiveMinimum     bool              `json:"exclusiveMinimum,omitempty"`
	ExclusiveMaximum     bool              `json:"exclusiveMaximum,omitempty"`
	MinLength            any               `json:"minLength,omitempty"`
	Pattern              string            `json:"pattern,omitempty"`
	MinItems             any               `json:"minItems,omitempty"`
	MaxItems             any               `json:"maxItems,omitempty"`
	UniqueItems          bool              `json:"uniqueItems,omitempty"`
	OneOf                []Schema          `json:"oneOf,omitempty"`
	AdditionalProperties any               `json:"additionalProperties,omitempty"`
}
