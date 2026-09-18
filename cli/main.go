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
	"strconv"
	"strings"
	"time"
	"unicode"
)

const help = `Usage: jobd [--controller URL] [--queue NAME] [--local] [action | COMMAND [ARGS...]]
Actions:
  -l               List remote and local jobs (default; --local lists local only)
  COMMAND [ARGS...] Submit a command and print its job ID
  -C               Clear finished job records (keep log files)
  -o [ID]          Print output path on executing host (last run by default)
  -r [ID]          Remove a non-running job (last added by default)
  -k [ID]          Request cancellation of a running job (last run by default)
  -u [ID]          Move a queued job first (last added by default)
  -U ID1 ID2       Swap two queued jobs
  --restart        Start or restart the detached local worker
  --stop           Stop the local worker
  -h               Show help
Use -- before a command beginning with a dash. Commands run directly, not via a shell.
Defaults: JOBD_CONTROLLER=https://jobd-controller.aflashsheng.workers.dev, JOBD_QUEUE=default.
Authentication: JOBD_API_KEY (controller requests only). Without it, local mode is automatic.
--local uses the same actions against the local worker's single queue.
Local jobs run without an idle delay when no key is set; otherwise after 30 seconds of controller idle time.
JOBD_STATE_DIR selects the local worker (default ~/.local/state/jobd-worker).
Output files remain on the executing worker, not on the CLI machine.
`

type job struct {
	ID              string   `json:"id"`
	Status          string   `json:"status"`
	Command         []string `json:"command"`
	Hostname        *string  `json:"hostname"`
	WorkerID        *string  `json:"worker_id"`
	OutputPath      *string  `json:"output_path"`
	ExitCode        *int     `json:"exit_code"`
	StartedAt       *string  `json:"started_at"`
	FinishedAt      *string  `json:"finished_at"`
	Progress        *float64 `json:"progress"`
	CancelRequested int      `json:"cancel_requested"`
}

// elapsed uses controller assignment timestamps, not process CPU time.
func elapsed(j job, now time.Time) string {
	if j.Status == "queued" || j.StartedAt == nil {
		return "-"
	}
	start, err := time.Parse(time.RFC3339Nano, *j.StartedAt)
	if err != nil {
		return "-"
	}
	end := now
	if j.Status != "running" {
		if j.FinishedAt == nil {
			return "-"
		}
		end, err = time.Parse(time.RFC3339Nano, *j.FinishedAt)
		if err != nil {
			return "-"
		}
	}
	duration := end.Sub(start).Truncate(time.Second)
	if duration < 0 {
		duration = 0 // Allow for clock skew between the CLI and controller.
	}
	return duration.String()
}

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
		return fmt.Errorf("%s %s: %s: %s", method, path, res.Status, strings.TrimSpace(string(data)))
	}
	if result != nil {
		return json.Unmarshal(data, result)
	}
	return nil
}

func env(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok {
		return v
	}
	return fallback
}
func value(p *string) string {
	if p == nil {
		return "-"
	}
	return *p
}

var shellBareWord = regexp.MustCompile(`^[a-zA-Z0-9_@%+=:,./-]+$`)

func shellQuote(arg string, command bool) string {
	// Control/format characters use Bash/Zsh ANSI-C quoting to keep the table
	// on one line without changing the argument when pasted into a shell.
	if strings.ContainsFunc(arg, func(r rune) bool { return unicode.IsControl(r) || unicode.Is(unicode.Cf, r) }) {
		var quoted strings.Builder
		quoted.WriteString("$'")
		for _, r := range arg {
			switch r {
			case '\n':
				quoted.WriteString(`\n`)
			case '\r':
				quoted.WriteString(`\r`)
			case '\t':
				quoted.WriteString(`\t`)
			case '\\':
				quoted.WriteString(`\\`)
			case '\'':
				quoted.WriteString(`\'`)
			default:
				if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
					if r < 128 {
						fmt.Fprintf(&quoted, `\x%02x`, r)
					} else {
						fmt.Fprintf(&quoted, `\U%08x`, r)
					}
				} else {
					quoted.WriteRune(r)
				}
			}
		}
		quoted.WriteByte('\'')
		return quoted.String()
	}
	// At command position, assignments and shell keywords must be quoted too.
	reserved := strings.Contains(" if then else elif fi case esac for select while until do done in function time coproc repeat end foreach nocorrect noglob ", " "+arg+" ")
	if shellBareWord.MatchString(arg) && !(command && (strings.Contains(arg, "=") || reserved)) {
		return arg
	}
	return "'" + strings.ReplaceAll(arg, "'", "'\\''") + "'"
}

func displayCommand(argv []string) string {
	parts := make([]string, len(argv))
	for i, arg := range argv {
		parts[i] = shellQuote(arg, i == 0)
	}
	return strings.Join(parts, " ")
}

func run(args []string, out, diagnostic io.Writer) error {
	address, queue := env("JOBD_CONTROLLER", "https://jobd-controller.aflashsheng.workers.dev"), env("JOBD_QUEUE", "default")
	local := false
	stateDir := env("JOBD_STATE_DIR", "~/.local/state/jobd-worker")
	for len(args) > 0 {
		if args[0] == "--local" {
			local = true
			args = args[1:]
			continue
		}
		name, v, hasValue := strings.Cut(args[0], "=")
		if name != "--controller" && name != "--queue" {
			break
		}
		args = args[1:]
		if !hasValue {
			if len(args) == 0 {
				return fmt.Errorf("%s needs a value", name)
			}
			v = args[0]
			args = args[1:]
		}
		if name == "--controller" {
			address = v
		} else {
			queue = v
		}
	}
	action := "-l"
	if len(args) > 0 {
		action = args[0]
		args = args[1:]
	}
	if action == "-h" || action == "--help" {
		_, err := io.WriteString(out, help)
		return err
	}
	if action == "--restart" || action == "--stop" {
		if local {
			return fmt.Errorf("%s is not a queue action", action)
		}
		if len(args) != 0 {
			return fmt.Errorf("%s takes no arguments", action)
		}
		return manageWorker(action, address, queue, out, diagnostic)
	}
	if action == "-l" && len(args) != 0 {
		return fmt.Errorf("-l takes no arguments")
	}
	if !local && action == "-l" && strings.TrimSpace(os.Getenv("JOBD_API_KEY")) != "" {
		return listCombined(address, queue, stateDir, out, diagnostic)
	}
	if strings.TrimSpace(os.Getenv("JOBD_API_KEY")) == "" {
		local = true
		if action == "-l" {
			fmt.Fprintln(diagnostic, "Warning: JOBD_API_KEY is not set; using local mode. Set JOBD_API_KEY and run jobd --restart to enable controller jobs.")
		}
	}
	var c *client
	var err error
	if local {
		if err := manageWorker("--start", address, queue, io.Discard, diagnostic); err != nil {
			return err
		}
		c, err = newLocalClient(stateDir)
	} else {
		c, err = newClient(address, queue)
	}
	if err != nil {
		return err
	}
	return runJobs(c, action, args, out, diagnostic)
}

// Both queues share validation, action defaults, pagination and table rendering.
func runJobs(c *client, action string, args []string, out, diagnostic io.Writer) error {
	switch action {
	case "-l", "-C":
		if len(args) != 0 {
			return fmt.Errorf("%s takes no arguments", action)
		}
	case "-o", "-r", "-u", "-k":
		if len(args) > 1 {
			return fmt.Errorf("%s takes at most one job ID", action)
		}
	case "-U":
		if len(args) != 2 {
			return fmt.Errorf("use -U ID1 ID2")
		}
	default:
		command := append([]string{action}, args...)
		if action == "--" {
			command = args
		} else if strings.HasPrefix(action, "-") {
			return fmt.Errorf("unknown action %s; use -h for help", action)
		}
		if len(command) == 0 || strings.TrimSpace(command[0]) == "" {
			return fmt.Errorf("command must not be empty")
		}
		var result job
		if err := c.request("POST", "/jobs", map[string]any{"command": command}, &result); err != nil {
			return err
		}
		_, err := fmt.Fprintln(out, result.ID)
		return err
	}
	if action == "-l" {
		jobs, err := fetchJobs(c)
		if err != nil {
			return err
		}
		return printJobs(out, jobs)
	}
	if action == "-C" {
		return c.request("POST", "/jobs/clear", struct{}{}, nil)
	}
	if action == "-U" {
		return c.request("POST", "/jobs/swap", map[string]string{"first": args[0], "second": args[1]}, nil)
	}
	var selected job
	if len(args) == 0 {
		kind := "added"
		if action == "-o" || action == "-k" {
			kind = "run"
		}
		if err := c.request("GET", "/jobs/latest?kind="+kind, nil, &selected); err != nil {
			return err
		}
	} else {
		selected.ID = args[0]
	}
	path := "/jobs/" + url.PathEscape(selected.ID)
	switch action {
	case "-k":
		if err := c.request("POST", path+"/cancel", struct{}{}, nil); err != nil {
			return err
		}
		_, err := fmt.Fprintln(out, "Cancellation requested for", selected.ID)
		return err
	case "-r":
		return c.request("DELETE", path, nil, nil)
	case "-u":
		return c.request("POST", path+"/urgent", struct{}{}, nil)
	case "-o":
		if len(args) > 0 {
			if err := c.request("GET", path, nil, &selected); err != nil {
				return err
			}
		}
		if selected.OutputPath == nil || *selected.OutputPath == "" {
			return fmt.Errorf("job %s has no reported output file yet", selected.ID)
		}
		fmt.Fprintf(diagnostic, "Output on worker %s (host %s); file is not downloaded.\n", value(selected.WorkerID), strconv.Quote(value(selected.Hostname)))
		_, err := fmt.Fprintln(out, *selected.OutputPath)
		return err
	}
	return nil
}
func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "jobd:", err)
		os.Exit(1)
	}
}
