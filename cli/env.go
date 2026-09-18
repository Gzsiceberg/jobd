package main

import (
	"bufio"
	"fmt"
	"io"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"golang.org/x/term"
)

// Bare names consume one line each; Enter terminates the value rather than
// becoming part of it. Keep prompts separate from command output.
func readEnvAssignments(args []string, input io.Reader, prompts io.Writer) ([]string, error) {
	validName := regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,127}$`)
	for i, arg := range args {
		name, _, _ := strings.Cut(arg, "=")
		if !validName.MatchString(name) {
			return nil, fmt.Errorf("assignment %d must have a valid variable name", i+1)
		}
	}
	terminalFD := -1
	if file, ok := input.(interface{ Fd() uintptr }); ok && term.IsTerminal(int(file.Fd())) {
		terminalFD = int(file.Fd())
	}
	scanner := bufio.NewScanner(input)
	// Allow 4096 value bytes plus CRLF and space to detect oversized input.
	scanner.Buffer(make([]byte, 4099), 4099)
	result := append([]string(nil), args...)
	for i, arg := range args {
		if strings.Contains(arg, "=") {
			continue
		}
		if terminalFD >= 0 {
			value, err := readConfirmedEnvValue(arg, prompts, func() (string, error) {
				value, err := term.ReadPassword(terminalFD)
				return string(value), err
			}, func() (string, error) {
				if !scanner.Scan() {
					return "", io.EOF
				}
				return scanner.Text(), nil
			})
			if err != nil {
				return nil, err
			}
			result[i] = arg + "=" + value
			continue
		}
		if _, err := fmt.Fprintf(prompts, "%s (press Enter): ", arg); err != nil {
			return nil, err
		}
		if !scanner.Scan() {
			return nil, fmt.Errorf("unable to read value for %s: expected a line of at most 4096 UTF-8 bytes", arg)
		}
		result[i] = arg + "=" + scanner.Text()
	}
	return result, nil
}

// Only long secrets expose a small, escaped prefix and suffix. Quoting prevents
// control characters in pasted values from being interpreted by the terminal.
func envValuePreview(value string) string {
	runes := []rune(value)
	preview := fmt.Sprintf("Received %d characters", len(runes))
	if len(runes) > 12 {
		preview += ": " + strconv.Quote(string(runes[:4])+"…"+string(runes[len(runes)-4:]))
	}
	return preview
}

func readConfirmedEnvValue(name string, prompts io.Writer, readSecret, readAnswer func() (string, error)) (string, error) {
	for {
		if _, err := fmt.Fprintf(prompts, "%s (hidden; press Enter): ", name); err != nil {
			return "", err
		}
		value, err := readSecret()
		if _, writeErr := fmt.Fprintln(prompts); writeErr != nil {
			return "", writeErr
		}
		if err != nil {
			return "", fmt.Errorf("unable to read hidden value for %s", name)
		}
		if len(value) > 4096 || !utf8.ValidString(value) || strings.ContainsRune(value, 0) {
			return "", fmt.Errorf("value must be UTF-8 without NUL, at most 4096 bytes")
		}
		if _, err := fmt.Fprintln(prompts, envValuePreview(value)); err != nil {
			return "", err
		}
		for {
			if _, err := fmt.Fprint(prompts, "Save? [Y/n] "); err != nil {
				return "", err
			}
			answer, err := readAnswer()
			if err != nil {
				return "", fmt.Errorf("confirmation cancelled for %s; no secrets saved", name)
			}
			switch strings.ToLower(strings.TrimSpace(answer)) {
			case "", "y", "yes":
				return value, nil
			case "n", "no":
				break
			default:
				continue
			}
			break
		}
	}
}

// Never include secret values in upload output or errors.
func runEnv(c *client, action string, args []string, out io.Writer) error {
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
	validName := regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,127}$`)
	if action == "set" {
		if len(args) == 0 {
			return fmt.Errorf("set requires one or more KEY=VALUE assignments")
		}
		type assignment struct{ name, value string }
		assignments := make([]assignment, 0, len(args))
		for i, arg := range args {
			name, value, ok := strings.Cut(arg, "=")
			if !ok || !validName.MatchString(name) {
				return fmt.Errorf("assignment %d must be KEY=VALUE with a valid variable name", i+1)
			}
			if len(value) > 4096 || !utf8.ValidString(value) || strings.ContainsRune(value, 0) {
				return fmt.Errorf("assignment %d value must be UTF-8 without NUL, at most 4096 bytes", i+1)
			}
			assignments = append(assignments, assignment{name, value})
		}
		for _, item := range assignments {
			if err := c.request("PUT", "/env/"+url.PathEscape(item.name), map[string]string{"value": item.value}, nil); err != nil {
				return fmt.Errorf("set %s failed (earlier assignments may already be saved): %w", item.name, err)
			}
		}
		return nil
	}
	if len(args) != 1 || !validName.MatchString(args[0]) {
		return fmt.Errorf("%s requires one valid variable name", action)
	}
	path := "/env/" + url.PathEscape(args[0])
	if action == "delete" {
		return c.request("DELETE", path, nil, nil)
	}
	return fmt.Errorf("unknown environment action")
}
