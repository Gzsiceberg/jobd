package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"
)

type client struct {
	base   string
	http   *http.Client
	apiKey string
	local  bool
}

func newClient(address, queue string) (*client, error) {
	u, err := url.Parse(address)
	if err != nil || u == nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.RawQuery != "" || u.Fragment != "" {
		return nil, fmt.Errorf("controller must be an HTTP(S) URL without a query or fragment")
	}
	if !regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,62}$`).MatchString(queue) {
		return nil, fmt.Errorf("invalid queue name")
	}
	return &client{base: strings.TrimRight(address, "/") + "/queues/" + queue, apiKey: strings.TrimSpace(os.Getenv("JOBD_API_KEY")), http: &http.Client{Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}

// Never retry mutations automatically: a lost submission/swap response is ambiguous.
func (c *client) request(method, path string, body, result any) error {
	var input io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return err
		}
		input = bytes.NewReader(data)
	}
	req, err := http.NewRequest(method, c.base+path, input)
	if err != nil {
		return err
	}
	if !c.local {
		if c.apiKey == "" {
			return fmt.Errorf("JOBD_API_KEY is required")
		}
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := c.http.Do(req)
	if err != nil {
		if method == http.MethodGet {
			return fmt.Errorf("%s %s: %w", method, path, err)
		}
		return fmt.Errorf("%s %s: %w (mutation outcome may be unknown; inspect the queue before retrying)", method, path, err)
	}
	defer res.Body.Close()
	limit := int64(1 << 20)
	if c.local {
		// 100 commands of 256 KiB, up to 6x JSON escaping, plus metadata.
		limit = 192 << 20
	}
	data, err := io.ReadAll(io.LimitReader(res.Body, limit+1))
	if err != nil {
		return err
	}
	if int64(len(data)) > limit {
		return fmt.Errorf("response exceeds %d MiB", limit>>20)
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		if path == "/env" || strings.HasPrefix(path, "/env/") {
			// Never echo a server/proxy response that might contain a submitted secret.
			return fmt.Errorf("%s %s: HTTP %d", method, path, res.StatusCode)
		}
		return fmt.Errorf("%s %s: %s: %s", method, path, res.Status, strings.TrimSpace(string(data)))
	}
	if result != nil {
		return json.Unmarshal(data, result)
	}
	return nil
}
