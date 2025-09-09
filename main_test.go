package main

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// --- helpers ---
func mustLoadIndex(t *testing.T, path string) *WPIndex {
	data, err := os.ReadFile(path)
	if err != nil { t.Fatalf("read: %v", err) }
	var idx WPIndex
	dec := json.NewDecoder(strings.NewReader(string(data)))
	dec.UseNumber()
	if err := dec.Decode(&idx); err != nil { t.Fatalf("decode: %v", err) }
	if idx.Routes == nil || len(idx.Routes) == 0 { t.Fatalf("no routes in %s", path) }
	return &idx
}

func TestReplaceNamedGroupsNested(t *testing.T) {
	in := `/wp/v2/template-parts/(?P<parent>([^\\/:<>*?"|]+(?:\\/[^\\/:<>*?"|]+)?)[\\/\\w%-]+)/autosaves/(?P<id>[\\d]+)`
	out := replaceNamedGroups(in)
	if !strings.Contains(out, "{parent}") || !strings.Contains(out, "{id}") {
		t.Fatalf("named groups not replaced correctly: %s", out)
	}
	if strings.Contains(out, "(?P") || strings.Contains(out, ")") {
		t.Fatalf("leftover regex group text: %s", out)
	}
}

func TestParseArgsTolerant(t *testing.T) {
	if got := parseArgs(json.RawMessage(`[]`)); len(got) != 0 { t.Fatalf("[] should parse to empty map") }
	if got := parseArgs(json.RawMessage(`{}`)); len(got) != 0 { t.Fatalf("{} should parse to empty map (no fields)") }
}

func TestConvertSynthetic(t *testing.T) {
	idx := &WPIndex{ Routes: map[string]*WPRoute{ 
		"/wp/v2/users": {
			Namespace: "wp/v2",
			Endpoints: []WPEndpoint{
				{ Methods: []string{"GET"}, ArgsRaw: json.RawMessage(`{"search":{"type":"string"}}`) },
				{ Methods: []string{"POST"}, ArgsRaw: json.RawMessage(`{"username":{"type":"string","required":true},"password":{"type":"string"}}`) },
			},
		},
	}} 
	spec, st, err := convert(idx, "http://example.com/wp-json")
	if err != nil { t.Fatalf("convert: %v", err) }
	if st.routes == 0 || len(spec.Paths) == 0 { t.Fatalf("no output paths") }
	p := spec.Paths["/wp/v2/users"]
	if p.Get == nil || p.Post == nil { t.Fatalf("expected GET and POST") }
	if p.Get.RequestBody != nil { t.Fatalf("GET should not have requestBody") }
	if p.Post.RequestBody == nil { t.Fatalf("POST should have requestBody") }
}

func TestIntegrationIfEnvSet(t *testing.T) {
	path := os.Getenv("WP_JSON")
	if path == "" { t.Skip("set WP_JSON to run integration test against a real wp-json.json") }
	idx := mustLoadIndex(t, path)
	spec, st, err := convert(idx, path)
	if err != nil { t.Fatalf("convert: %v", err) }
	if len(spec.Paths) < 300 { t.Fatalf("too few paths: %d (routes=%d, endpoints=%d, ops=%d)", len(spec.Paths), st.routes, st.endpoints, st.ops) }
	// sanity-check a known route
	pi, ok := spec.Paths["/wp/v2/users"]
	if !ok { t.Fatalf("/wp/v2/users path missing") }
	if pi.Get == nil || pi.Post == nil { t.Fatalf("users GET/POST missing") }
	// ensure GET does not include a POST-only field like password
	for _, p := range pi.Get.Parameters {
		if p.Name == "password" { t.Fatalf("GET unexpectedly has password param") }
	}
}
