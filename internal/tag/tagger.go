package tag

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/m7medVision/wpswag/internal/oas"
)

type Result struct {
	Method string `json:"m"`
	Path   string `json:"p"`
	Tag    string `json:"t"`
}

func TagSpec(spec *oas.Spec, categories []string, dryRun bool) error {
	endpoints := extractEndpoints(spec)
	if len(endpoints) == 0 {
		return nil
	}

	prompt := BuildPrompt(endpoints, categories)

	if dryRun {
		tokens := EstimateTokens(prompt)
		fmt.Fprintf(os.Stderr, "endpoints=%d estimated_tokens=%d\n", len(endpoints), tokens)
		return nil
	}

	opencodeBin, err := findOpencode()
	if err != nil {
		return err
	}

	output, err := runOpencode(opencodeBin, prompt)
	if err != nil {
		return fmt.Errorf("opencode: %w", err)
	}

	results, err := parseResults(output)
	if err != nil {
		return fmt.Errorf("parse: %w", err)
	}

	applyTags(spec, results)
	return nil
}

func extractEndpoints(spec *oas.Spec) []Endpoint {
	var eps []Endpoint
	for path, pi := range spec.Paths {
		for method, op := range operationsMap(pi) {
			if op == nil {
				continue
			}
			eps = append(eps, Endpoint{Method: method, Path: path})
		}
	}
	return eps
}

func operationsMap(pi oas.PathItem) map[string]*oas.Operation {
	return map[string]*oas.Operation{
		"GET": pi.Get, "POST": pi.Post, "PUT": pi.Put, "PATCH": pi.Patch,
		"DELETE": pi.Delete, "OPTIONS": pi.Options, "HEAD": pi.Head,
	}
}

func findOpencode() (string, error) {
	p, err := exec.LookPath("opencode")
	if err != nil {
		return "", fmt.Errorf("opencode CLI not found on PATH. Install it or use --no-tag")
	}
	return p, nil
}

func runOpencode(bin, prompt string) (string, error) {
	cmd := exec.Command(bin, "run", "--format", "json", prompt)
	cmd.Dir, _ = os.Getwd()

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("%s: %s", err, stderr.String())
	}

	return stdout.String(), nil
}

func parseResults(raw string) ([]Result, error) {
	var results []Result
	decoder := json.NewDecoder(strings.NewReader(raw))
	for {
		var evt map[string]any
		if err := decoder.Decode(&evt); err == io.EOF {
			break
		} else if err != nil {
			continue
		}
		typ, _ := evt["type"].(string)
		if typ != "assistant" {
			continue
		}
		content, _ := evt["content"].([]any)
		for _, c := range content {
			cm, ok := c.(map[string]any)
			if !ok {
				continue
			}
			ct, _ := cm["type"].(string)
			if ct != "text" {
				continue
			}
			text, _ := cm["text"].(string)
			for _, line := range strings.Split(text, "\n") {
				line = strings.TrimSpace(line)
				if line == "" || !strings.HasPrefix(line, "{") {
					continue
				}
				var r Result
				if err := json.Unmarshal([]byte(line), &r); err == nil && r.Tag != "" {
					results = append(results, r)
				}
			}
		}
	}
	return results, nil
}

func applyTags(spec *oas.Spec, results []Result) {
	type key struct{ m, p string }
	m := map[key]string{}
	for _, r := range results {
		m[key{strings.ToUpper(r.Method), r.Path}] = r.Tag
	}

	for path, pi := range spec.Paths {
		for method, op := range operationsMap(pi) {
			if op == nil {
				continue
			}
			if tag, ok := m[key{strings.ToUpper(method), path}]; ok {
				op.Tags = []string{tag}
			}
		}
	}
}
