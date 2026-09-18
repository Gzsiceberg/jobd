package main

import (
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode"
)

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
