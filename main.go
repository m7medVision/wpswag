package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"
)

/*
MVP: WordPress REST → OpenAPI 3.0 (JSON)
- Input: /wp-json/ or /?rest_route=/ (or site root, which we normalize).
- Output: OpenAPI 3.0 JSON with paths/operations and basic parameters derived from the index.
*/

type wpIndex struct {
	Name       string                 `json:"name"`
	Namespaces []string               `json:"namespaces"`
	Routes     map[string]wpRouteMeta `json:"routes"`
}

type wpRouteMeta struct {
	Namespace string                 `json:"namespace"`
	Methods   []string               `json:"methods"`
	Endpoints []wpEndpoint           `json:"endpoints"`
	Args      map[string]wpArgSchema `json:"args"`
	// Some installs include "schema" in the root index, but we ignore for MVP.
}

type wpEndpoint struct {
	Methods []string               `json:"methods"`
	Args    json.RawMessage 	`json:"args"` // can be {} or [] or null
}

type wpArgSchema struct {
	Required    bool            `json:"required"`
	Default     any             `json:"default"`
	Type        string          `json:"type"`
	Enum        []any           `json:"enum"`
	Items       any             `json:"items"`
	Format      string          `json:"format"`
	Description string          `json:"description"`
}

// Accept args as object OR empty array/null
func decodeArgs(raw json.RawMessage) map[string]wpArgSchema {
	if len(raw) == 0 {
		return map[string]wpArgSchema{}
	}
	// Try as object first
	var obj map[string]wpArgSchema
	if err := json.Unmarshal(raw, &obj); err == nil && obj != nil {
		return obj
	}
	// If it's an array (often []), treat as empty
	var arr []any
	if err := json.Unmarshal(raw, &arr); err == nil {
		return map[string]wpArgSchema{}
	}
	// Any other shape => ignore gracefully
	return map[string]wpArgSchema{}
}

// ------ OpenAPI types (minimal subset we need) ------

type openAPI struct {
	OpenAPI string                 `json:"openapi"`
	Info    openAPIInfo            `json:"info"`
	Servers []openAPIServer        `json:"servers,omitempty"`
	Paths   map[string]openAPIPath `json:"paths"`
	// You can extend with Components later (securitySchemes, schemas, etc.)
}

type openAPIInfo struct {
	Title   string `json:"title"`
	Version string `json:"version"`
}

type openAPIServer struct {
	URL string `json:"url"`
}

type openAPIPath map[string]openAPIOperation // get/post/put/patch/delete/head/options

type openAPIOperation struct {
	Summary     string                `json:"summary,omitempty"`
	Description string                `json:"description,omitempty"`
	Parameters  []openAPIParameter    `json:"parameters,omitempty"`
	RequestBody *openAPIRequestBody   `json:"requestBody,omitempty"`
	Responses   map[string]openAPIRes `json:"responses"`
	Tags        []string              `json:"tags,omitempty"`
}

type openAPIParameter struct {
	Name        string              `json:"name"`
	In          string              `json:"in"` // "query" or "path"
	Required    bool                `json:"required"`
	Description string              `json:"description,omitempty"`
	Schema      map[string]any      `json:"schema,omitempty"`
}

type openAPIRequestBody struct {
	Required bool                           `json:"required"`
	Content  map[string]openAPIMediaType    `json:"content"`
}

type openAPIMediaType struct {
	Schema map[string]any `json:"schema,omitempty"`
}

type openAPIRes struct {
	Description string                         `json:"description"`
	Content     map[string]openAPIMediaType    `json:"content,omitempty"`
}

// ------ Main ------

func main() {
	var (
		inURL   string
		outFile string
		title   string
		version string
	)
	flag.StringVar(&inURL, "u", "", "Entry point URL (/wp-json/ or /?rest_route=/) or site root (required)")
	flag.StringVar(&outFile, "o", "", "Output file for OpenAPI JSON (default: stdout)")
	flag.StringVar(&title, "title", "", "API title (default: WP site name or host)")
	flag.StringVar(&version, "version", "1.0.0", "API version string")
	flag.Parse()

	if inURL == "" {
		fail("missing -u URL (entry point or site root)")
	}

	entryURL, baseServerURL, err := normalizeEntryURL(inURL)
	if err != nil {
		fail("normalize url: %v", err)
	}

	idx, rawName, err := fetchWPIndex(entryURL)
	if err != nil {
		fail("fetch index: %v", err)
	}

	apiTitle := title
	if apiTitle == "" {
		apiTitle = firstNonEmpty(idx.Name, rawName, mustHost(baseServerURL))
	}

	oas := buildOpenAPI(idx, apiTitle, version, baseServerURL)

	var out io.Writer = os.Stdout
	if outFile != "" {
		f, err := os.Create(outFile)
		if err != nil {
			fail("create output: %v", err)
		}
		defer f.Close()
		out = f
	}
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	if err := enc.Encode(oas); err != nil {
		fail("encode openapi json: %v", err)
	}
}

// ------ Fetch & Normalize ------

// normalizeEntryURL takes either a site root or an entry point and returns:
// - entryURL: absolute URL to the JSON index
// - baseServerURL: server URL for OAS (scheme+host [+port] + `/wp-json`)
func normalizeEntryURL(in string) (entryURL, baseServerURL string, err error) {
	u, err := url.Parse(in)
	if err != nil {
		return "", "", err
	}
	if u.Scheme == "" {
		u.Scheme = "https"
	}
	// Detect if caller already gave us an index endpoint
	lc := strings.ToLower(u.String())
	if strings.Contains(lc, "rest_route=/") {
		// Keep as-is; server base should be scheme://host
		base := &url.URL{Scheme: u.Scheme, Host: u.Host}
		return u.String(), strings.TrimRight(base.String(), "/"), nil
	}
	if strings.HasSuffix(lc, "/wp-json") || strings.HasSuffix(lc, "/wp-json/") {
		// Standard index
		base := &url.URL{Scheme: u.Scheme, Host: u.Host}
		return ensureTrailingSlash(u.String()), strings.TrimRight(base.String(), "/") + "/wp-json", nil
	}
	// Otherwise treat as site root; try /wp-json/ first, then /?rest_route=/
	root := &url.URL{Scheme: u.Scheme, Host: u.Host, Path: "/wp-json/"}
	if ok, _ := urlReturnsJSON(root.String()); ok {
		return root.String(), strings.TrimRight((&url.URL{Scheme: u.Scheme, Host: u.Host}).String(), "/") + "/wp-json", nil
	}
	alt := &url.URL{Scheme: u.Scheme, Host: u.Host, RawQuery: "rest_route=/"}
	if ok, _ := urlReturnsJSON(alt.String()); ok {
		return alt.String(), strings.TrimRight((&url.URL{Scheme: u.Scheme, Host: u.Host}).String(), "/"), nil
	}
	return "", "", errors.New("could not locate a REST index at /wp-json/ or /?rest_route=/")
}

func urlReturnsJSON(u string) (bool, error) {
	c := &http.Client{Timeout: 12 * time.Second}
	req, _ := http.NewRequest(http.MethodGet, u, nil)
	req.Header.Set("Accept", "application/json")
	res, err := c.Do(req)
	if err != nil {
		return false, err
	}
	defer res.Body.Close()
	ct := strings.ToLower(res.Header.Get("Content-Type"))
	return res.StatusCode < 400 && strings.Contains(ct, "json"), nil
}

func fetchWPIndex(entry string) (wpIndex, string, error) {
	var idx wpIndex
	c := &http.Client{Timeout: 20 * time.Second}
	req, _ := http.NewRequest(http.MethodGet, entry, nil)
	req.Header.Set("Accept", "application/json")
	res, err := c.Do(req)
	if err != nil {
		return idx, "", err
	}
	defer res.Body.Close()
	if res.StatusCode >= 400 {
		return idx, "", fmt.Errorf("status %d", res.StatusCode)
	}
	b, _ := io.ReadAll(res.Body)
	if err := json.Unmarshal(b, &idx); err != nil {
		// try best-effort to extract "name" if present at top-level
		var tmp map[string]any
		if json.Unmarshal(b, &tmp) == nil {
			if v, ok := tmp["name"].(string); ok {
				return idx, v, err
			}
		}
		return idx, "", err
	}
	return idx, idx.Name, nil
}

// ------ Build OpenAPI from index ------

var (
	// Replace WP route regex groups (?P<name>pattern) with {name}
	reGroup = regexp.MustCompile(`\(\?P<([a-zA-Z0-9_]+)>([^)]+)\)`)
	// Extract param names from template
	reParam = regexp.MustCompile(`\{([a-zA-Z0-9_]+)\}`)
)

// buildOpenAPI constructs a minimal, valid OAS 3.0 document.
func buildOpenAPI(idx wpIndex, title, version, serverBase string) openAPI {
	paths := map[string]openAPIPath{}

	// Deterministic order for stable outputs
	routeKeys := make([]string, 0, len(idx.Routes))
	for k := range idx.Routes {
		routeKeys = append(routeKeys, k)
	}
	sort.Strings(routeKeys)

	for _, route := range routeKeys {
		meta := idx.Routes[route]

		// Convert WP regex path → OAS templated path
		oasPath, paramPatterns := wpRouteToOASPath(route)

		// Gather methods: prefer union of endpoints[].methods
		methods := set[string]{}
		for _, ep := range meta.Endpoints {
			for _, m := range ep.Methods {
				methods.add(strings.ToUpper(m))
			}
		}
		if len(methods) == 0 {
			for _, m := range meta.Methods {
				methods.add(strings.ToUpper(m))
			}
		}
		if len(methods) == 0 {
			// Default to GET if nothing advertised
			methods.add("GET")
		}

		// Gather args: union of route-level args and endpoint args
		args := map[string]wpArgSchema{}
		for k, v := range meta.Args {
			args[k] = v
		}
		for _, ep := range meta.Endpoints {
			for k, v := range decodeArgs(ep.Args) {
				args[k] = v
			}
		}

		// Build a parameter list per operation. Path params are always required.
		pathParamSet := map[string]struct{}{}
		for _, name := range reParam.FindAllStringSubmatch(oasPath, -1) {
			pathParamSet[name[1]] = struct{}{}
		}

		if _, ok := paths[oasPath]; !ok {
			paths[oasPath] = openAPIPath{}
		}

		for _, m := range methods.sorted() {
			op := openAPIOperation{
				Summary:     fmt.Sprintf("%s %s", m, oasPath),
				Description: fmt.Sprintf("Auto-generated from WordPress REST index for %q.", route),
				Responses: map[string]openAPIRes{
					"200": {Description: "OK", Content: map[string]openAPIMediaType{
						"application/json": {Schema: map[string]any{"type": "object"}},
					}},
				},
			}

			// Parameters
			params := make([]openAPIParameter, 0, len(args))
			// First, path parameters (from template)
			for pname := range pathParamSet {
				param := openAPIParameter{
					Name:     pname,
					In:       "path",
					Required: true,
					Schema:   map[string]any{"type": "string"},
				}
				if pat, ok := paramPatterns[pname]; ok && pat != "" {
					param.Schema["pattern"] = pat
				}
				if a, ok := args[pname]; ok && a.Description != "" {
					param.Description = a.Description
				}
				params = append(params, param)
			}
			// Then, query parameters (everything else we know about)
			for name, a := range args {
				if _, isPath := pathParamSet[name]; isPath {
					continue
				}
				param := openAPIParameter{
					Name:        name,
					In:          "query",
					Required:    a.Required,
					Description: a.Description,
					Schema:      wpArgToSchema(a),
				}
				params = append(params, param)
			}
			if len(params) > 0 {
				op.Parameters = params
			}

			// Generic requestBody for non-GET/HEAD
			if m != "GET" && m != "HEAD" {
				op.RequestBody = &openAPIRequestBody{
					Required: false,
					Content: map[string]openAPIMediaType{
						"application/json": {Schema: map[string]any{"type": "object"}},
					},
				}
			}

			// Insert operation under method key
			switch m {
			case "GET":
				paths[oasPath]["get"] = op
			case "POST":
				paths[oasPath]["post"] = op
			case "PUT":
				paths[oasPath]["put"] = op
			case "PATCH":
				paths[oasPath]["patch"] = op
			case "DELETE":
				paths[oasPath]["delete"] = op
			case "HEAD":
				paths[oasPath]["head"] = op
			case "OPTIONS":
				paths[oasPath]["options"] = op
			default:
				// ignore uncommon verbs for now
			}
		}
	}

	api := openAPI{
		OpenAPI: "3.0.3",
		Info: openAPIInfo{
			Title:   title,
			Version: version,
		},
		Servers: []openAPIServer{{URL: strings.TrimRight(serverBase, "/")}},
		Paths:   paths,
	}
	return api
}

// Convert WP route key like "/wp/v2/posts/(?P<id>\\d+)" to "/wp/v2/posts/{id}"
// Return the OAS path and a map of param -> regex pattern.
func wpRouteToOASPath(route string) (string, map[string]string) {
	paramPatterns := map[string]string{}
	out := reGroup.ReplaceAllStringFunc(route, func(m string) string {
		sub := reGroup.FindStringSubmatch(m)
		if len(sub) == 3 {
			name := sub[1]
			pat := sub[2]
			paramPatterns[name] = pat
			return "{" + name + "}"
		}
		return m
	})
	// WP routes are already rooted. Ensure no trailing slash normalization change.
	return out, paramPatterns
}

func wpArgToSchema(a wpArgSchema) map[string]any {
	s := map[string]any{}
	typ := strings.ToLower(a.Type)
	if typ == "" {
		// WP often omits type; default to string to keep OAS validators happy.
		typ = "string"
	}
	switch typ {
	case "array":
		// MVP: leave items as string if unknown
		s["type"] = "array"
		s["items"] = map[string]any{"type": "string"}
	case "object":
		s["type"] = "object"
	default:
		s["type"] = typ
	}
	if a.Format != "" {
		s["format"] = a.Format
	}
	if len(a.Enum) > 0 {
		// JSON-ify enums as-is
		s["enum"] = a.Enum
	}
	if a.Default != nil {
		s["default"] = a.Default
	}
	return s
}

// ------ Helpers ------

type set[T comparable] map[T]struct{}

func (s set[T]) add(v T) { s[v] = struct{}{} }
func (s set[T]) sorted() []T {
	out := make([]T, 0, len(s))
	for v := range s {
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool {
		return fmt.Sprint(out[i]) < fmt.Sprint(out[j])
	})
	return out
}

func fail(fmtStr string, a ...any) {
	fmt.Fprintf(os.Stderr, "error: "+fmtStr+"\n", a...)
	os.Exit(1)
}

func ensureTrailingSlash(s string) string {
	if strings.HasSuffix(s, "/") {
		return s
	}
	return s + "/"
}

func firstNonEmpty(s ...string) string {
	for _, v := range s {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func mustHost(base string) string {
	u, _ := url.Parse(base)
	return u.Hostname()
}
