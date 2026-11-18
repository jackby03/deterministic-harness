package inventory

import (
	"strings"
)

// Statuses reported by `go test -v`.
const (
	StatusPass = "pass"
	StatusFail = "fail"
	StatusSkip = "skip"
)

// ParseGoTestV maps top-level test names to their status.
//
// Subtests are rolled up into their parent with fail > skip > pass precedence,
// so a `--- SKIP:` on a subtest is never hidden by a parent `--- PASS:`.
func ParseGoTestV(text string) map[string]string {
	out := map[string]string{}
	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimSpace(raw)

		var status string
		switch {
		case strings.HasPrefix(line, "--- PASS: "):
			status = StatusPass
		case strings.HasPrefix(line, "--- FAIL: "):
			status = StatusFail
		case strings.HasPrefix(line, "--- SKIP: "):
			status = StatusSkip
		default:
			continue
		}

		name := line[strings.Index(line, ": ")+2:]
		if i := strings.Index(name, " ("); i > 0 {
			name = name[:i]
		}
		if i := strings.Index(name, "/"); i > 0 {
			name = name[:i]
		}
		if prev, ok := out[name]; ok {
			out[name] = worse(prev, status)
			continue
		}
		out[name] = status
	}
	return out
}

func worse(a, b string) string {
	rank := map[string]int{StatusPass: 0, StatusSkip: 1, StatusFail: 2}
	if rank[b] > rank[a] {
		return b
	}
	return a
}

// CheckSelectors enforces that each required selector actually ran and was not
// skipped. A skipped test reports success to the exit code, which is the exact
// hole this closes: a security invariant gated by a t.Skip is not gated.
func CheckSelectors(statuses map[string]string, required []string, forbidSkip bool) []Violation {
	var out []Violation
	if len(statuses) == 0 {
		return []Violation{{Subject: "go-test", Kind: "missing", Detail: "no test results parsed: the selector matched nothing"}}
	}
	for _, name := range required {
		status, ok := statuses[name]
		switch {
		case !ok:
			out = append(out, Violation{Subject: name, Kind: "missing", Detail: "required selector did not run"})
		case status == StatusFail:
			out = append(out, Violation{Subject: name, Kind: "failed", Detail: "required selector failed"})
		case status == StatusSkip && forbidSkip:
			out = append(out, Violation{Subject: name, Kind: "skipped", Detail: "required selector was skipped; a skip is not evidence"})
		}
	}
	sortViolations(out)
	return out
}
