package convert

import "strings"

// ReplaceNamedGroups handles nested parentheses inside a (?P<name> ... ) group.
func ReplaceNamedGroups(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		if i+3 < len(s) && s[i] == '(' && s[i+1] == '?' && s[i+2] == 'P' && s[i+3] == '<' {
			j := i + 4
			for j < len(s) && s[j] != '>' {
				j++
			}
			if j >= len(s) {
				b.WriteString(s[i:])
				break
			}
			name := s[i+4 : j]
			j++
			depth := 1
			k := j
			for k < len(s) && depth > 0 {
				if s[k] == '(' {
					depth++
				} else if s[k] == ')' {
					depth--
				}
				k++
			}
			b.WriteString("{")
			b.WriteString(name)
			b.WriteString("}")
			i = k
			continue
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}

// SanitizeRoutePath cleans a WordPress route regex path into an OpenAPI path.
// Returns the sanitized path and a list of path parameter names.
func SanitizeRoutePath(route string) (string, []string) {
	p := strings.ReplaceAll(route, `\/`, "/")
	p = strings.ReplaceAll(p, `\\`, "")
	p = ReplaceNamedGroups(p)

	// Remove obvious regex tokens and character classes
	for _, j := range []string{"^", "$", "+", "?", ":", "|"} {
		p = strings.ReplaceAll(p, j, "")
	}
	for {
		start := strings.Index(p, "[")
		if start == -1 {
			break
		}
		end := strings.Index(p[start+1:], "]")
		if end == -1 {
			break
		}
		p = p[:start] + p[start+end+2:]
	}
	for strings.Contains(p, "//") {
		p = strings.ReplaceAll(p, "//", "/")
	}
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	if len(p) > 1 {
		p = strings.TrimRight(p, "/")
	}

	params := []string{}
	for _, seg := range strings.Split(p, "/") {
		if strings.HasPrefix(seg, "{") && strings.HasSuffix(seg, "}") {
			params = append(params, strings.TrimSuffix(strings.TrimPrefix(seg, "{"), "}"))
		}
	}
	return p, params
}
