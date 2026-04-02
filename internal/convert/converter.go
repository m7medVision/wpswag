package convert

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strings"
	"sync"
	"unicode"

	"github.com/m7medVision/wpswag/internal/oas"
	"github.com/m7medVision/wpswag/internal/util"
	"github.com/m7medVision/wpswag/internal/wp"
)

type fetchFunc func(method, source string) ([]byte, error)

// Converter converts a WordPress REST index into an OpenAPI 3.0.3 spec.
type Converter struct {
	idx    *wp.Index
	source string
	fetch  fetchFunc
}

type routeMeta struct {
	raw          string
	route        *wp.Route
	path         string
	pathParams   []string
	isCollection bool
}

type schemaDiscovery struct {
	path   string
	name   string
	schema oas.Schema
}

const schemaDiscoveryConcurrency = 8

// NewConverter creates a new converter from a parsed WordPress index.
func NewConverter(idx *wp.Index, source string) *Converter {
	return &Converter{idx: idx, source: source, fetch: util.FetchWithMethod}
}

// Convert performs the conversion and returns the spec, stats, and any error.
func (c *Converter) Convert() (*oas.Spec, *Stats, error) {
	if c.idx == nil || c.idx.Routes == nil {
		return nil, nil, errors.New("no routes found")
	}

	serverURL := c.resolveServerURL()
	builder := NewBuilder(c.idx.Name, c.idx.Description, serverURL)

	keys := make([]string, 0, len(c.idx.Routes))
	for k := range c.idx.Routes {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	metas := c.buildRouteMetas(keys)
	discovered := c.discoverSchemas(builder, serverURL, metas)

	for _, meta := range metas {
		builder.IncrementRoutes()
		pi := builder.GetPath(meta.path)
		tag := primaryTag(meta.path, meta.route.Namespace)
		summary := strings.TrimPrefix(meta.path, "/")
		responseSchema, hasResponseSchema := resolveResponseSchema(meta.path, discovered)

		for _, ep := range meta.route.Endpoints {
			builder.IncrementEndpoints()
			methods := ep.Methods
			if len(methods) == 0 && len(meta.route.Methods) > 0 {
				methods = meta.route.Methods
			}
			epArgs := util.ParseArgs(ep.ArgsRaw)
			routeArgs := util.ParseArgs(meta.route.ArgsRaw)
			for _, method := range methods {
				args := chooseArgsForMethod(routeArgs, epArgs, method)
				op := buildOperation(method, meta.path, []string{tag}, meta.pathParams, args, summary, responseSchema, hasResponseSchema, meta.isCollection)
				SetMethodOperation(&pi, method, op)
				builder.IncrementOps()
			}
		}

		if IsPathItemEmpty(pi) && len(meta.route.Methods) > 0 {
			routeArgs := util.ParseArgs(meta.route.ArgsRaw)
			for _, method := range meta.route.Methods {
				op := buildOperation(method, meta.path, []string{tag}, meta.pathParams, routeArgs, summary, responseSchema, hasResponseSchema, meta.isCollection)
				SetMethodOperation(&pi, method, op)
				builder.IncrementOps()
			}
		}

		builder.AddPath(meta.path, pi)
	}

	spec, stats := builder.Build()
	if len(spec.Paths) == 0 {
		return nil, stats, errors.New("no paths emitted")
	}
	return spec, stats, nil
}

func (c *Converter) buildRouteMetas(keys []string) []routeMeta {
	metas := make([]routeMeta, 0, len(keys))
	itemParents := map[string]bool{}

	for _, raw := range keys {
		r := c.idx.Routes[raw]
		if r == nil {
			continue
		}
		path, pathParams := SanitizeRoutePath(raw)
		metas = append(metas, routeMeta{raw: raw, route: r, path: path, pathParams: pathParams})
		if parent := directCollectionPath(path); parent != "" {
			itemParents[parent] = true
		}
	}

	for i := range metas {
		metas[i].isCollection = isCollectionRoute(metas[i], itemParents)
	}

	return metas
}

func (c *Converter) discoverSchemas(builder *Builder, serverURL string, metas []routeMeta) map[string]string {
	if !util.IsHTTP(c.source) {
		return map[string]string{}
	}

	candidates := make([]routeMeta, 0, len(metas))
	for _, meta := range metas {
		if !shouldDiscoverSchema(meta) {
			continue
		}
		candidates = append(candidates, meta)
	}

	results := make(chan schemaDiscovery, len(candidates))
	sem := make(chan struct{}, schemaDiscoveryConcurrency)
	var wg sync.WaitGroup

	for _, meta := range candidates {
		wg.Add(1)
		go func(meta routeMeta) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			route, err := c.fetchRouteOptions(strings.TrimRight(serverURL, "/") + meta.path)
			if err != nil || len(route.Schema) == 0 {
				return
			}

			results <- schemaDiscovery{
				path:   meta.path,
				name:   componentName(route.Schema, meta.path),
				schema: buildSchema(route.Schema),
			}
		}(meta)
	}

	wg.Wait()
	close(results)

	found := make([]schemaDiscovery, 0, len(candidates))
	for result := range results {
		found = append(found, result)
	}
	sort.Slice(found, func(i, j int) bool {
		return found[i].path < found[j].path
	})

	discovered := map[string]string{}
	for _, result := range found {
		builder.AddSchema(result.name, result.schema)
		discovered[result.path] = result.name
	}

	return discovered
}

func (c *Converter) fetchRouteOptions(url string) (*wp.Route, error) {
	data, err := c.fetch(http.MethodOptions, url)
	if err != nil {
		return nil, err
	}
	data = util.CleanJSON(data)

	var route wp.Route
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	if err := dec.Decode(&route); err != nil {
		return nil, err
	}
	return &route, nil
}

func (c *Converter) resolveServerURL() string {
	base := c.idx.URL
	if base == "" {
		base = c.idx.Home
	}
	if util.IsHTTP(c.source) {
		o := util.OriginFromURL(c.source)
		base = strings.TrimRight(o, "/") + "/wp-json"
	}
	if base == "" {
		base = "https://example.com/wp-json"
	}
	return base
}

// chooseArgsForMethod picks args by method: endpoint args override;
// GET-like methods can fall back to route args.
func chooseArgsForMethod(routeArgs, epArgs map[string]any, method string) map[string]any {
	if len(epArgs) > 0 {
		return epArgs
	}
	switch strings.ToUpper(method) {
	case "GET", "HEAD", "DELETE":
		return routeArgs
	default:
		return map[string]any{}
	}
}

// normalizeType maps WordPress types to OpenAPI types.
func normalizeType(t string) string {
	switch t {
	case "integer", "number", "boolean", "string", "array", "object":
		return t
	case "file":
		return "string"
	default:
		return "string"
	}
}

// opID generates an operation ID from method and path.
func opID(method, path string) string {
	clean := strings.Trim(path, "/")
	clean = strings.ReplaceAll(clean, "/", "_")
	clean = strings.ReplaceAll(clean, "{", "")
	clean = strings.ReplaceAll(clean, "}", "")
	clean = strings.ReplaceAll(clean, "-", "_")
	if clean == "" {
		clean = "root"
	}
	return strings.ToLower(method + "_" + clean)
}

// buildSchema builds an OpenAPI schema from a WordPress schema-like map.
func buildSchema(arg map[string]any) oas.Schema {
	s := oas.Schema{}
	hasExplicitOneOf := false

	if title, _ := arg["title"].(string); title != "" {
		s.Title = title
	}
	if desc, _ := arg["description"].(string); desc != "" {
		s.Description = desc
	}
	if fmtStr, _ := arg["format"].(string); fmtStr != "" {
		s.Format = fmtStr
	}
	if def, ok := arg["default"]; ok {
		s.Default = def
	}
	if ev, ok := arg["enum"].([]any); ok && len(ev) > 0 {
		s.Enum = ev
	}
	if ro, ok := readBool(arg, "readOnly", "readonly"); ok {
		s.ReadOnly = ro
	}
	if mv, ok := arg["minimum"]; ok {
		s.Minimum = mv
	}
	if mv, ok := arg["maximum"]; ok {
		s.Maximum = mv
	}
	if v, ok := arg["exclusiveMinimum"].(bool); ok {
		s.ExclusiveMinimum = v
	}
	if v, ok := arg["exclusiveMaximum"].(bool); ok {
		s.ExclusiveMaximum = v
	}
	if v, ok := arg["minLength"]; ok {
		s.MinLength = v
	}
	if v, ok := arg["minItems"]; ok {
		s.MinItems = v
	}
	if v, ok := arg["maxItems"]; ok {
		s.MaxItems = v
	}
	if v, ok := arg["pattern"].(string); ok {
		s.Pattern = v
	}
	if v, ok := arg["uniqueItems"].(bool); ok {
		s.UniqueItems = v
	}

	if ov, ok := arg["oneOf"].([]any); ok && len(ov) > 0 {
		for _, entry := range ov {
			if child, ok := entry.(map[string]any); ok {
				s.OneOf = append(s.OneOf, buildSchema(child))
			}
		}
		hasExplicitOneOf = len(s.OneOf) > 0
	}

	if tv, ok := arg["type"]; ok {
		switch t := tv.(type) {
		case string:
			s.Type = normalizeType(t)
		case []any:
			var types []string
			nullable := false
			for _, entry := range t {
				ss, ok := entry.(string)
				if !ok {
					continue
				}
				if ss == "null" {
					nullable = true
					continue
				}
				types = append(types, normalizeType(ss))
			}
			if len(types) == 1 {
				s.Type = types[0]
			} else if len(types) > 1 && !hasExplicitOneOf {
				for _, tt := range types {
					s.OneOf = append(s.OneOf, oas.Schema{Type: tt})
				}
			}
			s.Nullable = nullable
		}
	}

	if it, ok := arg["items"].(map[string]any); ok {
		child := buildSchema(it)
		s.Items = &child
	}

	if pv, ok := arg["properties"].(map[string]any); ok {
		if s.Type == nil {
			s.Type = "object"
		}
		s.Properties = map[string]oas.Schema{}
		for key, value := range pv {
			if child, ok := value.(map[string]any); ok {
				s.Properties[key] = buildSchema(child)
			}
		}
	}

	if rv, ok := arg["required"].([]any); ok {
		for _, entry := range rv {
			if rs, ok := entry.(string); ok {
				s.Required = append(s.Required, rs)
			}
		}
	}

	if ap, ok := arg["additionalProperties"]; ok {
		s.AdditionalProperties = buildAdditionalProperties(ap)
	}

	return s
}

// buildOperation builds an OpenAPI operation from WordPress route data.
func buildOperation(method, path string, tags []string, pathParams []string, args map[string]any, summary string, responseSchema oas.Schema, hasResponseSchema, isCollection bool) *oas.Operation {
	m := strings.ToUpper(method)
	op := &oas.Operation{
		OperationID: opID(m, path),
		Summary:     summary,
		Tags:        tags,
		Responses:   buildResponses(m, responseSchema, hasResponseSchema, isCollection),
	}

	for _, nm := range pathParams {
		op.Parameters = append(op.Parameters, oas.Parameter{
			Name: nm, In: "path", Required: true,
			Schema: oas.Schema{Type: "string"},
		})
	}

	if m == http.MethodPost || m == http.MethodPut || m == http.MethodPatch {
		if len(args) > 0 {
			props := map[string]oas.Schema{}
			req := []string{}
			for name, raw := range args {
				am, _ := raw.(map[string]any)
				if am == nil {
					continue
				}
				if isPathParamName(name, pathParams) {
					continue
				}
				props[name] = buildSchema(am)
				if rb, ok := am["required"].(bool); ok && rb {
					req = append(req, name)
				}
			}
			body := oas.Schema{Type: "object", Properties: props}
			if len(req) > 0 {
				body.Required = req
			}
			op.RequestBody = &oas.RequestBody{
				Required: len(req) > 0,
				Content: map[string]oas.Media{
					"application/json":                  {Schema: body},
					"application/x-www-form-urlencoded": {Schema: body},
				},
			}
		}
	} else {
		for name, raw := range args {
			am, _ := raw.(map[string]any)
			if am == nil || isPathParamName(name, pathParams) {
				continue
			}
			req := false
			if rb, ok := am["required"].(bool); ok {
				req = rb
			}
			desc, _ := am["description"].(string)
			op.Parameters = append(op.Parameters, oas.Parameter{
				Name: name, In: "query", Required: req,
				Description: desc, Schema: buildSchema(am),
			})
		}
	}
	return op
}

func buildResponses(method string, responseSchema oas.Schema, hasResponseSchema, isCollection bool) map[string]oas.Response {
	response := oas.Response{Description: "OK"}
	if !hasResponseSchema {
		return map[string]oas.Response{"200": response}
	}

	switch method {
	case http.MethodGet:
		if isCollection {
			response.Content = map[string]oas.Media{
				"application/json": {
					Schema: oas.Schema{Type: "array", Items: &responseSchema},
				},
			}
		} else {
			response.Content = map[string]oas.Media{
				"application/json": {Schema: responseSchema},
			}
		}
	case http.MethodPost, http.MethodPut, http.MethodPatch:
		response.Content = map[string]oas.Media{
			"application/json": {Schema: responseSchema},
		}
	}

	return map[string]oas.Response{"200": response}
}

func buildAdditionalProperties(v any) any {
	switch value := v.(type) {
	case bool:
		return value
	case map[string]any:
		return buildSchema(value)
	default:
		return value
	}
}

func readBool(arg map[string]any, keys ...string) (bool, bool) {
	for _, key := range keys {
		if v, ok := arg[key].(bool); ok {
			return v, true
		}
	}
	return false, false
}

func shouldDiscoverSchema(meta routeMeta) bool {
	if meta.route == nil || meta.route.Namespace != "wp/v2" || len(meta.pathParams) > 0 {
		return false
	}
	return primaryTag(meta.path, meta.route.Namespace) != meta.route.Namespace
}

func resolveResponseSchema(path string, discovered map[string]string) (oas.Schema, bool) {
	if name, ok := discovered[path]; ok {
		return oas.Schema{Ref: "#/components/schemas/" + name}, true
	}
	if parent := directCollectionPath(path); parent != "" {
		if name, ok := discovered[parent]; ok {
			return oas.Schema{Ref: "#/components/schemas/" + name}, true
		}
	}
	return oas.Schema{}, false
}

func isCollectionRoute(meta routeMeta, itemParents map[string]bool) bool {
	if len(meta.pathParams) > 0 {
		return false
	}
	if itemParents[meta.path] {
		return true
	}
	return routeHasCollectionArgs(meta.route)
}

func routeHasCollectionArgs(route *wp.Route) bool {
	if route == nil {
		return false
	}

	argsSets := []map[string]any{util.ParseArgs(route.ArgsRaw)}
	for _, ep := range route.Endpoints {
		if len(ep.Methods) == 0 || containsMethod(ep.Methods, http.MethodGet) {
			argsSets = append(argsSets, util.ParseArgs(ep.ArgsRaw))
		}
	}

	for _, args := range argsSets {
		if len(args) == 0 {
			continue
		}
		for _, key := range []string{"page", "per_page", "offset", "search", "orderby", "order", "include", "exclude"} {
			if _, ok := args[key]; ok {
				return true
			}
		}
	}

	return false
}

func containsMethod(methods []string, target string) bool {
	for _, method := range methods {
		if strings.EqualFold(method, target) {
			return true
		}
	}
	return false
}

func directCollectionPath(path string) string {
	parts := splitPath(path)
	if len(parts) == 0 || !isPathParam(parts[len(parts)-1]) {
		return ""
	}
	return "/" + strings.Join(parts[:len(parts)-1], "/")
}

func primaryTag(path, namespace string) string {
	parts := splitPath(path)
	nsParts := splitPath(namespace)
	if len(parts) >= len(nsParts) {
		matchesNamespace := true
		for i := range nsParts {
			if parts[i] != nsParts[i] {
				matchesNamespace = false
				break
			}
		}
		if matchesNamespace && len(parts) > len(nsParts) {
			return parts[len(nsParts)]
		}
	}
	if namespace != "" {
		return namespace
	}
	if len(parts) > 0 {
		return parts[0]
	}
	return "default"
}

func splitPath(v string) []string {
	parts := strings.Split(strings.Trim(v, "/"), "/")
	if len(parts) == 1 && parts[0] == "" {
		return nil
	}
	return parts
}

func isPathParam(segment string) bool {
	return strings.HasPrefix(segment, "{") && strings.HasSuffix(segment, "}")
}

func isPathParamName(name string, pathParams []string) bool {
	for _, param := range pathParams {
		if param == name {
			return true
		}
	}
	return false
}

func componentName(schema map[string]any, path string) string {
	if title, _ := schema["title"].(string); title != "" {
		return exportName(title)
	}
	return exportName(strings.Trim(path, "/"))
}

func exportName(v string) string {
	var b strings.Builder
	capitalize := true
	for _, r := range v {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			if capitalize {
				b.WriteRune(unicode.ToUpper(r))
				capitalize = false
				continue
			}
			b.WriteRune(r)
			continue
		}
		capitalize = true
	}
	name := b.String()
	if name == "" {
		return "Schema"
	}
	if len(name) > 0 && unicode.IsDigit([]rune(name)[0]) {
		return "Schema" + name
	}
	return name
}
