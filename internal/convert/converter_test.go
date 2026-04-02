package convert

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"sensepost.com/wpswag/internal/oas"
	"sensepost.com/wpswag/internal/wp"
)

func TestConvertUsesOptionsSchemaForTypedResponsesAndTags(t *testing.T) {
	idx := &wp.Index{
		Name: "Test API",
		URL:  "https://example.com/wp-json",
		Routes: map[string]*wp.Route{
			"/wp/v2/posts": {
				Namespace: "wp/v2",
				Endpoints: []wp.Endpoint{
					{Methods: []string{http.MethodGet}, ArgsRaw: rawArgs(`{"page":{"type":"integer"}}`)},
					{Methods: []string{http.MethodPost}, ArgsRaw: rawArgs(`{"title":{"type":"string","required":true}}`)},
				},
			},
			"/wp/v2/posts/(?P<id>[\\d]+)": {
				Namespace: "wp/v2",
				Endpoints: []wp.Endpoint{
					{Methods: []string{http.MethodGet}, ArgsRaw: rawArgs(`{"id":{"type":"integer"}}`)},
					{Methods: []string{http.MethodPatch}, ArgsRaw: rawArgs(`{"id":{"type":"integer"},"title":{"type":"string"}}`)},
				},
			},
			"/wp/v2/tags": {
				Namespace: "wp/v2",
				Endpoints: []wp.Endpoint{
					{Methods: []string{http.MethodGet}, ArgsRaw: rawArgs(`{"page":{"type":"integer"}}`)},
				},
			},
			"/wp/v2/tags/(?P<id>[\\d]+)": {
				Namespace: "wp/v2",
				Endpoints: []wp.Endpoint{
					{Methods: []string{http.MethodGet}, ArgsRaw: rawArgs(`{"id":{"type":"integer"}}`)},
				},
			},
		},
	}

	conv := NewConverter(idx, "https://example.com/wp-json")
	conv.fetch = func(method, url string) ([]byte, error) {
		if method != http.MethodOptions {
			return nil, fmt.Errorf("unexpected method %s", method)
		}
		switch url {
		case "https://example.com/wp-json/wp/v2/posts":
			return []byte(`{"schema":{"title":"post","type":"object","properties":{"id":{"type":"integer","readonly":true},"title":{"type":"string"}}}}`), nil
		case "https://example.com/wp-json/wp/v2/tags":
			return []byte(`{"schema":{"title":"tag","type":"object","properties":{"id":{"type":"integer","readonly":true},"name":{"type":"string"}}}}`), nil
		default:
			return nil, fmt.Errorf("unexpected options url %s", url)
		}
	}

	spec, _, err := conv.Convert()
	if err != nil {
		t.Fatalf("Convert() error = %v", err)
	}
	if spec.Components == nil {
		t.Fatal("expected component schemas to be generated")
	}

	postSchema, ok := spec.Components.Schemas["Post"]
	if !ok {
		t.Fatalf("expected Post schema, got %#v", spec.Components.Schemas)
	}
	if !postSchema.Properties["id"].ReadOnly {
		t.Fatalf("expected Post.id to be readOnly, got %#v", postSchema.Properties["id"])
	}

	postsGet := spec.Paths["/wp/v2/posts"].Get
	if postsGet == nil {
		t.Fatal("expected GET /wp/v2/posts operation")
	}
	assertTags(t, postsGet.Tags, []string{"posts"})
	postsBody := postsGet.Responses["200"].Content["application/json"].Schema
	if got, _ := postsBody.Type.(string); got != "array" {
		t.Fatalf("expected posts collection response type array, got %#v", postsBody.Type)
	}
	if postsBody.Items == nil || postsBody.Items.Ref != "#/components/schemas/Post" {
		t.Fatalf("expected posts collection items ref, got %#v", postsBody.Items)
	}

	postsCreate := spec.Paths["/wp/v2/posts"].Post
	if postsCreate == nil {
		t.Fatal("expected POST /wp/v2/posts operation")
	}
	if got := postsCreate.Responses["200"].Content["application/json"].Schema.Ref; got != "#/components/schemas/Post" {
		t.Fatalf("expected POST /wp/v2/posts to return Post ref, got %q", got)
	}

	postGet := spec.Paths["/wp/v2/posts/{id}"].Get
	if postGet == nil {
		t.Fatal("expected GET /wp/v2/posts/{id} operation")
	}
	if got := postGet.Responses["200"].Content["application/json"].Schema.Ref; got != "#/components/schemas/Post" {
		t.Fatalf("expected GET /wp/v2/posts/{id} to return Post ref, got %q", got)
	}

	tagsGet := spec.Paths["/wp/v2/tags"].Get
	if tagsGet == nil {
		t.Fatal("expected GET /wp/v2/tags operation")
	}
	assertTags(t, tagsGet.Tags, []string{"tags"})
	if got := tagsGet.Responses["200"].Content["application/json"].Schema.Items.Ref; got != "#/components/schemas/Tag" {
		t.Fatalf("expected GET /wp/v2/tags to return Tag array, got %q", got)
	}
}

func TestBuildSchemaUsesExplicitOneOfAndNestedAdditionalProperties(t *testing.T) {
	schema := buildSchema(map[string]any{
		"description": "Limit result set to matching terms.",
		"type":        []any{"object", "array"},
		"oneOf": []any{
			map[string]any{
				"title": "Term ID List",
				"type":  "array",
				"items": map[string]any{"type": "integer"},
			},
			map[string]any{
				"title": "Term Query",
				"type":  "object",
				"properties": map[string]any{
					"terms": map[string]any{"type": "array", "items": map[string]any{"type": "integer"}},
				},
				"additionalProperties": false,
			},
		},
	})

	if len(schema.OneOf) != 2 {
		t.Fatalf("expected explicit oneOf entries, got %#v", schema.OneOf)
	}
	if got, _ := schema.OneOf[0].Type.(string); got != "array" {
		t.Fatalf("expected first oneOf schema to stay array, got %#v", schema.OneOf[0])
	}
	if got, _ := schema.OneOf[1].AdditionalProperties.(bool); got {
		t.Fatalf("expected nested additionalProperties false, got %#v", schema.OneOf[1].AdditionalProperties)
	}

	objectSchema := buildSchema(map[string]any{
		"type": "object",
		"additionalProperties": map[string]any{
			"type":    "string",
			"default": "value",
		},
	})

	ap, ok := objectSchema.AdditionalProperties.(oas.Schema)
	if !ok {
		t.Fatalf("expected additionalProperties schema, got %#v", objectSchema.AdditionalProperties)
	}
	if got, _ := ap.Type.(string); got != "string" {
		t.Fatalf("expected additionalProperties string schema, got %#v", ap)
	}
	if ap.Default != "value" {
		t.Fatalf("expected additionalProperties default to be preserved, got %#v", ap.Default)
	}
}

func rawArgs(v string) json.RawMessage {
	return json.RawMessage(v)
}

func assertTags(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("unexpected tags length: got %#v want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("unexpected tags: got %#v want %#v", got, want)
		}
	}
}
