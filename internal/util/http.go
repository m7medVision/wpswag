package util

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// IsHTTP returns true if the string starts with http:// or https://.
func IsHTTP(u string) bool {
	return strings.HasPrefix(u, "http://") || strings.HasPrefix(u, "https://")
}

// Fetch retrieves content from a URL or local file.
func Fetch(u string) ([]byte, error) {
	return FetchWithMethod(http.MethodGet, u)
}

// FetchWithMethod retrieves content from a URL using the supplied method.
func FetchWithMethod(method, u string) ([]byte, error) {
	if IsHTTP(u) {
		timeout := 30 * time.Second
		if method == http.MethodOptions {
			timeout = 8 * time.Second
		}
		c := &http.Client{Timeout: timeout}
		req, err := http.NewRequest(method, u, nil)
		if err != nil {
			return nil, err
		}
		r, err := c.Do(req)
		if err != nil {
			return nil, err
		}
		defer r.Body.Close()
		if r.StatusCode < 200 || r.StatusCode >= 300 {
			return nil, fmt.Errorf("HTTP %d", r.StatusCode)
		}
		return io.ReadAll(r.Body)
	}
	if method != http.MethodGet {
		return nil, fmt.Errorf("%s unsupported for local files", method)
	}
	return os.ReadFile(u)
}

// OriginFromURL extracts the scheme+host from a URL string.
func OriginFromURL(u string) string {
	sp := strings.SplitN(u, "/", 4)
	if len(sp) >= 3 {
		return sp[0] + "//" + sp[2]
	}
	return u
}
