package wp

import "encoding/json"

// Index represents a WordPress REST API index or namespace response.
type Index struct {
	Name        string            `json:"name"`
	Description string            `json:"description"`
	URL         string            `json:"url"`
	Home        string            `json:"home"`
	Namespace   string            `json:"namespace"`
	Namespaces  []string          `json:"namespaces"`
	Routes      map[string]*Route `json:"routes"`
	Links       map[string][]Link `json:"_links,omitempty"`
}

// Route represents a single WordPress REST route.
type Route struct {
	Namespace string            `json:"namespace"`
	Methods   []string          `json:"methods"`
	ArgsRaw   json.RawMessage   `json:"args"` // {} or [] or null
	Endpoints []Endpoint        `json:"endpoints"`
	Schema    map[string]any    `json:"schema,omitempty"`
	Links     map[string][]Link `json:"_links,omitempty"`
}

// Endpoint represents one endpoint within a WordPress route.
type Endpoint struct {
	Methods    []string        `json:"methods"`
	ArgsRaw    json.RawMessage `json:"args"` // {} or [] or null
	AllowBatch map[string]bool `json:"allow_batch,omitempty"`
}

// Link represents a WordPress REST link object.
type Link struct {
	Href       string `json:"href,omitempty"`
	Type       string `json:"type,omitempty"`
	Name       string `json:"name,omitempty"`
	Embeddable bool   `json:"embeddable,omitempty"`
	Templated  bool   `json:"templated,omitempty"`
}
