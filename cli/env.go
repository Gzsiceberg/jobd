package main

import (
	"fmt"
	"io"
	"net/url"
	"regexp"
	"strings"
	"unicode/utf8"
)

// Values come only from stdin, never argv (shell history and process listings).
func runEnv(c *client, action string, args []string, input io.Reader, out io.Writer) error {
	if c.local || !strings.HasPrefix(c.base, "https://") {
		return fmt.Errorf("queue environment requires a remote HTTPS controller")
	}
	if c.apiKey == "" {
		return fmt.Errorf("JOBD_API_KEY is required")
	}
	if action == "list" {
		if len(args) != 0 {
			return fmt.Errorf("env list takes no arguments")
		}
		var result struct {
			Names []string `json:"names"`
		}
		if err := c.request("GET", "/env", nil, &result); err != nil {
			return err
		}
		for _, name := range result.Names {
			if _, err := fmt.Fprintln(out, name); err != nil {
				return err
			}
		}
		return nil
	}
	if len(args) != 1 || !regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,127}$`).MatchString(args[0]) {
		return fmt.Errorf("%s requires one valid variable name", action)
	}
	path := "/env/" + url.PathEscape(args[0])
	if action == "delete" {
		return c.request("DELETE", path, nil, nil)
	}
	if action != "set" {
		return fmt.Errorf("unknown environment action")
	}
	value, err := io.ReadAll(io.LimitReader(input, 4097))
	if err != nil {
		return fmt.Errorf("unable to read environment value from stdin")
	}
	if len(value) > 4096 || !utf8.Valid(value) || strings.ContainsRune(string(value), 0) {
		return fmt.Errorf("value must be UTF-8 without NUL, at most 4096 bytes")
	}
	return c.request("PUT", path, map[string]string{"value": string(value)}, nil)
}
