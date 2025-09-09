// wp2openapi: Generate an OpenAPI 3.0 spec from a WordPress REST index/namespace
// - Handles nested named regex groups in routes (balanced parsing)
// - Tolerant to args/properties/items being {} or []
// - Keeps endpoint args per method; GET-ish -> query params; write methods -> requestBody
// - Accepts -u (URL or local JSON path), -o (output), -debug for diagnostics

package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

var (
	flagURL   = flag.String("u", "", "WordPress REST URL or local JSON file (e.g. https://site/wp-json or ./wp-json.json)")
	flagOut   = flag.String("o", "", "Output OpenAPI file (defaults to stdout)")
	flagDebug = flag.Bool("debug", false, "Print debug stats to stderr")
)

// ---------------- WordPress discovery (tolerant) ----------------

type WPIndex struct {
	Name        string              `json:"name"`
	Description string              `json:"description"`
	URL         string              `json:"url"`
	Home        string              `json:"home"`
	Namespace   string              `json:"namespace"`
	Namespaces  []string            `json:"namespaces"`
	Routes      map[string]*WPRoute `json:"routes"`
}

type WPRoute struct {
	Namespace string          `json:"namespace"`
	Methods   []string        `json:"methods"`
	ArgsRaw   json.RawMessage `json:"args"`         // {} or [] or null
	Endpoints []WPEndpoint    `json:"endpoints"`
}

type WPEndpoint struct {
	Methods []string        `json:"methods"`
	ArgsRaw json.RawMessage `json:"args"`          // {} or [] or null
}

// ---------------- Minimal OpenAPI 3 types ----------------

type OA3Spec struct {
	OpenAPI string                 `json:"openapi"`
	Info    OAInfo                 `json:"info"`
	Servers []OAServer             `json:"servers,omitempty"`
	Paths   map[string]OAPathItem  `json:"paths"`
}

type OAInfo struct{
	Title       string `json:"title"`
	Description string `json:"description,omitempty"`
	Version     string `json:"version"`
}

type OAServer struct{ URL string `json:"url"` }

type OAPathItem struct {
	Get     *OAOperation `json:"get,omitempty"`
	Post    *OAOperation `json:"post,omitempty"`
	Put     *OAOperation `json:"put,omitempty"`
	Patch   *OAOperation `json:"patch,omitempty"`
	Delete  *OAOperation `json:"delete,omitempty"`
	Options *OAOperation `json:"options,omitempty"`
	Head    *OAOperation `json:"head,omitempty"`
}

type OAOperation struct {
	OperationID string                 `json:"operationId,omitempty"`
	Summary     string                 `json:"summary,omitempty"`
	Tags        []string               `json:"tags,omitempty"`
	Parameters  []OAParameter          `json:"parameters,omitempty"`
	RequestBody *OARequestBody         `json:"requestBody,omitempty"`
	Responses   map[string]OAResponse  `json:"responses"`
}

type OAParameter struct {
	Name        string   `json:"name"`
	In          string   `json:"in"`
	Required    bool     `json:"required"`
	Description string   `json:"description,omitempty"`
	Schema      OASchema `json:"schema"`
}

type OARequestBody struct {
	Required bool               `json:"required"`
	Content  map[string]OAMedia `json:"content"`
}

type OAMedia struct { Schema OASchema `json:"schema"` }

type OAResponse struct { Description string `json:"description"` }

type OASchema struct {
	Type        any                 `json:"type,omitempty"`
	Format      string              `json:"format,omitempty"`
	Enum        []any               `json:"enum,omitempty"`
	Items       *OASchema           `json:"items,omitempty"`
	Properties  map[string]OASchema `json:"properties,omitempty"`
	Required    []string            `json:"required,omitempty"`
	Description string              `json:"description,omitempty"`
	Nullable    bool                `json:"nullable,omitempty"`
	OneOf       []OASchema          `json:"oneOf,omitempty"`
	AdditionalProperties any        `json:"additionalProperties,omitempty"`
}

// ---------------- Utilities ----------------

func isHTTP(u string) bool { return strings.HasPrefix(u, "http://") || strings.HasPrefix(u, "https://") }

func fetch(u string) ([]byte, error) {
	if isHTTP(u) {
		c := &http.Client{Timeout: 30 * time.Second}
		r, err := c.Get(u)
		if err != nil { return nil, err }
		defer r.Body.Close()
		if r.StatusCode < 200 || r.StatusCode >= 300 { return nil, fmt.Errorf("HTTP %d", r.StatusCode) }
		return io.ReadAll(r.Body)
	}
	return os.ReadFile(u)
}

// replaceNamedGroups handles nested parentheses inside a (?P<name> ... ) group
func replaceNamedGroups(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		if i+3 < len(s) && s[i] == '(' && s[i+1] == '?' && s[i+2] == 'P' && s[i+3] == '<' {
			j := i+4
			for j < len(s) && s[j] != '>' { j++ }
			if j >= len(s) { b.WriteString(s[i:]); break }
			name := s[i+4:j]
			j++
			depth := 1
			k := j
			for k < len(s) && depth > 0 {
				if s[k] == '(' { depth++ } else if s[k] == ')' { depth-- }
				k++
			}
			b.WriteString("{"); b.WriteString(name); b.WriteString("}")
			i = k
			continue
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}

func sanitizeRoutePath(route string) (string, []string) {
	p := strings.ReplaceAll(route, `\/`, "/")
	p = strings.ReplaceAll(p, `\\`, "")
	p = replaceNamedGroups(p)
	// Remove obvious regex tokens and character classes
	for _, j := range []string{"^", "$", "+", "?", ":", "|"} { p = strings.ReplaceAll(p, j, "") }
	for {
		start := strings.Index(p, "[")
		if start == -1 { break }
		end := strings.Index(p[start+1:], "]")
		if end == -1 { break }
		p = p[:start] + p[start+end+2:]
	}
	for strings.Contains(p, "//") { p = strings.ReplaceAll(p, "//", "/") }
	if !strings.HasPrefix(p, "/") { p = "/" + p }
	if len(p) > 1 { p = strings.TrimRight(p, "/") }
	params := []string{}
	for _, seg := range strings.Split(p, "/") {
		if strings.HasPrefix(seg, "{") && strings.HasSuffix(seg, "}") {
			params = append(params, strings.TrimSuffix(strings.TrimPrefix(seg, "{"), "}"))
		}
	}
	return p, params
}

// parse args: accept {} or [] or null
func parseArgs(raw json.RawMessage) map[string]any {
	if len(raw) == 0 || string(raw) == "null" { return map[string]any{} }
	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err == nil { return obj }
	var arr []any
	if err := json.Unmarshal(raw, &arr); err == nil { return map[string]any{} }
	return map[string]any{}
}

// choose args by method: endpoint args override; GET-like can fall back to route args
func chooseArgsForMethod(routeArgs, epArgs map[string]any, method string) map[string]any {
	if len(epArgs) > 0 { return epArgs }
	switch strings.ToUpper(method) {
	case "GET", "HEAD", "DELETE":
		return routeArgs
	default:
		return map[string]any{}
	}
}

// Build OASchema from a generic WP arg map
func buildSchema(arg map[string]any) OASchema {
	s := OASchema{}
	// type can be string or []string (incl. "null")
	if tv, ok := arg["type"]; ok {
		switch t := tv.(type) {
		case string:
			s.Type = normalizeType(t)
		case []any:
			var types []string; nullable := false
			for _, e := range t {
				if ss, ok := e.(string); ok {
					if ss == "null" { nullable = true; continue }
					types = append(types, normalizeType(ss))
				}
			}
			if len(types) == 1 { s.Type = types[0] } else if len(types) > 1 {
				for _, tt := range types { s.OneOf = append(s.OneOf, OASchema{Type: tt}) }
			}
			s.Nullable = nullable
		}
	}
	if desc, _ := arg["description"].(string); desc != "" { s.Description = desc }
	if fmtStr, _ := arg["format"].(string); fmtStr != "" { s.Format = fmtStr }
	if ev, ok := arg["enum"].([]any); ok && len(ev) > 0 { s.Enum = ev }
	// items
	if it, ok := arg["items"].(map[string]any); ok { child := buildSchema(it); s.Items = &child }
	// properties
	if pv, ok := arg["properties"].(map[string]any); ok && len(pv) > 0 {
		s.Type = "object"
		s.Properties = map[string]OASchema{}
		for k, v := range pv { if vm, ok := v.(map[string]any); ok { s.Properties[k] = buildSchema(vm) } }
		// required list
		if rv, ok := arg["required"].([]any); ok {
			for _, r := range rv { if rs, ok := r.(string); ok { s.Required = append(s.Required, rs) } }
		}
	}
	if ap, ok := arg["additionalProperties"]; ok { s.AdditionalProperties = ap }
	return s
}

func normalizeType(t string) string {
	switch t {
	case "integer", "number", "boolean", "string", "array", "object":
		return t
	case "file":
		return "string" // OAS3 uses string+binary in multipart; we keep simple here
	default:
		return "string"
	}
}

func opID(method, path string) string {
	clean := strings.Trim(path, "/")
	clean = strings.ReplaceAll(clean, "/", "_")
	clean = strings.ReplaceAll(clean, "{", ""); clean = strings.ReplaceAll(clean, "}", "")
	clean = strings.ReplaceAll(clean, "-", "_")
	if clean == "" { clean = "root" }
	return strings.ToLower(method + "_" + clean)
}

func buildOperation(method, path string, tags []string, pathParams []string, args map[string]any, summary string) *OAOperation {
	m := strings.ToUpper(method)
	op := &OAOperation{ OperationID: opID(m, path), Summary: summary, Tags: tags, Responses: map[string]OAResponse{"200": {Description: "OK"}} }
	// Path params
	for _, nm := range pathParams {
		op.Parameters = append(op.Parameters, OAParameter{ Name: nm, In: "path", Required: true, Schema: OASchema{ Type: "string" } })
	}
	if m == "POST" || m == "PUT" || m == "PATCH" {
		if len(args) > 0 {
			props := map[string]OASchema{}; req := []string{}
			for name, raw := range args {
				am, _ := raw.(map[string]any); if am == nil { continue }
				// avoid echoing path params in body
				isPath := false; for _, pn := range pathParams { if name == pn { isPath = true; break } }
				if isPath { continue }
				props[name] = buildSchema(am)
				if rb, ok := am["required"].(bool); ok && rb { req = append(req, name) }
			}
			body := OASchema{ Type: "object", Properties: props }
			if len(req) > 0 { body.Required = req }
			op.RequestBody = &OARequestBody{ Required: len(req) > 0, Content: map[string]OAMedia{"application/json": { Schema: body }, "application/x-www-form-urlencoded": { Schema: body }}}
		}
	} else {
		// GET/DELETE/HEAD/OPTIONS -> query
		for name, raw := range args {
			am, _ := raw.(map[string]any); if am == nil { continue }
			// skip path params
			skip := false; for _, pn := range pathParams { if name == pn { skip = true; break } }
			if skip { continue }
			req := false; if rb, ok := am["required"].(bool); ok { req = rb }
			desc, _ := am["description"].(string)
			op.Parameters = append(op.Parameters, OAParameter{ Name: name, In: "query", Required: req, Description: desc, Schema: buildSchema(am) })
		}
	}
	return op
}

// ---------------- Conversion ----------------

type stats struct{ routes, endpoints, ops int }

func convert(idx *WPIndex, source string) (*OA3Spec, *stats, error) {
	if idx == nil || idx.Routes == nil { return nil, nil, errors.New("no routes found") }

	base := idx.URL
	if base == "" { base = idx.Home }
	if isHTTP(source) {
	    // If the source points to a namespace (/wp-json/wp/v2) or root,
	    // normalize the server URL to origin + /wp-json
	    o := originFromURL(source)
	    // Only append /wp-json if not already present
	    if !strings.Contains(source, "/wp-json") {
		base = strings.TrimRight(o, "/") + "/wp-json"
	    } else {
		// strip to .../wp-json if deeper
		i := strings.Index(source, "/wp-json")
		base = strings.TrimRight(o, "/") + "/wp-json"
		if i >= 0 {
		    base = strings.TrimRight(o, "/") + "/wp-json"
		}
	    }
	}
	if base == "" {
	    base = "https://example.com/wp-json"
	}

	title := idx.Name; if title == "" { title = "WordPress REST" }
	spec := &OA3Spec{ OpenAPI: "3.0.3", Info: OAInfo{ Title: title, Description: idx.Description, Version: "1.0.0" }, Servers: []OAServer{{URL: base}}, Paths: map[string]OAPathItem{} }
	st := &stats{}

	// stable key order
	keys := make([]string, 0, len(idx.Routes))
	for k := range idx.Routes { keys = append(keys, k) }
	sort.Strings(keys)
	st.routes = len(keys)

	for _, raw := range keys {
		r := idx.Routes[raw]
		path, pathParams := sanitizeRoutePath(raw)
		pi := spec.Paths[path]
		for _, ep := range r.Endpoints {
			st.endpoints++
			methods := ep.Methods
			if len(methods) == 0 && len(r.Methods) > 0 { methods = r.Methods }
			epArgs := parseArgs(ep.ArgsRaw)
			routeArgs := parseArgs(r.ArgsRaw)
			for _, m := range methods {
				args := chooseArgsForMethod(routeArgs, epArgs, m)
				op := buildOperation(m, path, []string{r.Namespace}, pathParams, args, r.Namespace)
				switch strings.ToUpper(m) {
				case "GET": pi.Get = op
				case "POST": pi.Post = op
				case "PUT": pi.Put = op
				case "PATCH": pi.Patch = op
				case "DELETE": pi.Delete = op
				case "OPTIONS": pi.Options = op
				case "HEAD": pi.Head = op
				}
				st.ops++
			}
		}
		// Fallback if no endpoints emitted
		if pi == (OAPathItem{}) && len(r.Methods) > 0 {
			routeArgs := parseArgs(r.ArgsRaw)
			for _, m := range r.Methods {
				op := buildOperation(m, path, []string{r.Namespace}, pathParams, routeArgs, r.Namespace)
				switch strings.ToUpper(m) {
				case "GET": pi.Get = op
				case "POST": pi.Post = op
				case "PUT": pi.Put = op
				case "PATCH": pi.Patch = op
				case "DELETE": pi.Delete = op
				case "OPTIONS": pi.Options = op
				case "HEAD": pi.Head = op
				}
				st.ops++
			}
		}
		spec.Paths[path] = pi
	}
	if len(spec.Paths) == 0 { return nil, st, errors.New("no paths emitted") }
	return spec, st, nil
}

func originFromURL(u string) string { sp := strings.SplitN(u, "/", 4); if len(sp) >= 3 { return sp[0] + "//" + sp[2] }; return u }

// cleanJSON removes UTF BOMs and any leading junk before '{' or '['.
func cleanJSON(b []byte) []byte {
    // UTF-8 BOM
    if len(b) >= 3 && b[0] == 0xEF && b[1] == 0xBB && b[2] == 0xBF {
        b = b[3:]
    }
    // Trim common whitespace
    b = bytes.TrimLeft(b, "\r\n\t ")

    // In case something injected text before the JSON, jump to first '{' or '['
    if i := bytes.IndexAny(b, "{["); i > 0 {
        b = b[i:]
    }
    return b
}

// ---------------- Main ----------------

func main() {
	flag.Parse()
	if *flagURL == "" { fmt.Fprintln(os.Stderr, "Usage: wp2openapi -u <wp-json URL or local file> [-o openapi.json] [--debug]"); os.Exit(2) }
	data, err := fetch(*flagURL)
	if err != nil { fmt.Fprintf(os.Stderr, "fetch error: %v\n", err); os.Exit(1) }
	data = cleanJSON(data)

	var idx WPIndex
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	if err := dec.Decode(&idx); err != nil { fmt.Fprintf(os.Stderr, "decode error: %v\n", err); os.Exit(1) }

	spec, st, err := convert(&idx, *flagURL)
	if err != nil { fmt.Fprintf(os.Stderr, "convert error: %v\n", err) }
	if *flagDebug {
		fmt.Fprintf(os.Stderr, "routes=%d endpoints=%d ops=%d paths_out=%d\n", st.routes, st.endpoints, st.ops, len(spec.Paths))
	}
	out, err := json.MarshalIndent(spec, "", "  ")
	if err != nil { fmt.Fprintf(os.Stderr, "marshal error: %v\n", err); os.Exit(1) }
	if *flagOut == "" { os.Stdout.Write(out); return }
	if err := os.WriteFile(*flagOut, out, 0644); err != nil { fmt.Fprintf(os.Stderr, "write error: %v\n", err); os.Exit(1) }
	if *flagDebug { fmt.Fprintf(os.Stderr, "wrote %s (%s bytes)\n", filepath.Base(*flagOut), strconv.Itoa(len(out))) }
}
