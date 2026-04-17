package tag

import (
	"encoding/json"
	"fmt"
	"strings"
)

type Endpoint struct {
	Method string `json:"m"`
	Path   string `json:"p"`
}

var DefaultCategories = []string{
	"Posts",
	"Pages",
	"Media",
	"Comments",
	"Taxonomy",
	"Users",
	"Auth",
	"Settings",
	"Plugins",
	"Themes",
	"Site",
	"Admin",
	"Widgets",
	"Block Editor",
	"Other",
}

func BuildPrompt(endpoints []Endpoint, categories []string) string {
	epJSONL := endpointsToJSONL(endpoints)
	catStr := strings.Join(categories, "|")
	return fmt.Sprintf(
		"Tag each endpoint into one category. Categories: %s\nReply JSONL: {\"m\":\"METHOD\",\"p\":\"path\",\"t\":\"Tag\"}\n%s",
		catStr, epJSONL,
	)
}

func EstimateTokens(prompt string) int {
	return len(prompt) / 4
}

func endpointsToJSONL(eps []Endpoint) string {
	var b strings.Builder
	for _, ep := range eps {
		line, _ := json.Marshal(ep)
		b.Write(line)
		b.WriteByte('\n')
	}
	return b.String()
}
