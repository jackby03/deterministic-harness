// Package inventory turns raw gate output into structured facts, and checks
// those facts against the minimum inventory declared in the registry.
//
// The point of this package is that every check is a pure function over text.
// That makes the checks themselves testable: registry-selftest feeds them
// synthetic violations and asserts they are rejected. A gate that cannot fail
// is not a gate, so the failure path is exercised here, in isolation from any
// toolchain.
package inventory

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// BenchResult is one parsed line of `go test -bench -benchmem` output.
type BenchResult struct {
	Package     string
	Name        string // without the -N CPU suffix
	NsPerOp     float64
	BytesPerOp  int64
	AllocsPerOp int64
}

// ParseBench parses -benchmem output. Package headers ("pkg: path") and
// "goos:"/"goarch:"/"cpu:" lines are ignored; they are context, not results.
//
// A line is a result only when it yields all three metrics, so partial or
// malformed lines are dropped rather than guessed at. Dropping is safe because
// CheckAllocs runs with failOnUnlisted: a benchmark that fails to parse then
// shows up as "unlisted" instead of silently disappearing.
func ParseBench(text string) []BenchResult {
	var out []BenchResult
	pkg := ""
	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimSpace(raw)
		switch {
		case strings.HasPrefix(line, "pkg:"):
			pkg = strings.TrimSpace(strings.TrimPrefix(line, "pkg:"))
			continue
		case strings.HasPrefix(line, "goos:"),
			strings.HasPrefix(line, "goarch:"),
			strings.HasPrefix(line, "cpu:"),
			!strings.HasPrefix(line, "Benchmark"):
			continue
		}

		fields := strings.Fields(line)
		if len(fields) < 4 {
			continue
		}

		name := fields[0]
		if i := strings.LastIndex(name, "-"); i > 0 {
			if _, err := strconv.Atoi(name[i+1:]); err == nil {
				name = name[:i]
			}
		}

		r := BenchResult{Package: pkg, Name: name}
		complete := 0
		for i := 1; i+1 < len(fields); i++ {
			value := fields[i]
			switch fields[i+1] {
			case "ns/op":
				r.NsPerOp, _ = strconv.ParseFloat(value, 64)
				complete++
			case "B/op":
				r.BytesPerOp, _ = strconv.ParseInt(value, 10, 64)
				complete++
			case "allocs/op":
				r.AllocsPerOp, _ = strconv.ParseInt(value, 10, 64)
				complete++
			}
		}
		if complete == 3 {
			out = append(out, r)
		}
	}
	return out
}

// Violation is one reason a gate must fail.
type Violation struct {
	Subject string // benchmark, selector, file or path the violation is about
	Kind    string // over-limit | unlisted | missing | skipped | dangling | over-budget | unjustified
	Detail  string
}

func (v Violation) String() string {
	return fmt.Sprintf("%s: %s (%s)", v.Subject, v.Detail, v.Kind)
}

// CheckAllocs compares measured allocations against the declared ceiling.
//
// When failOnUnlisted is set, every observed benchmark must appear in limits.
// That is what stops a new benchmark from passing with no ceiling at all, and
// it is also what makes a deleted benchmark visible: absence shows up as a
// missing subject only when the caller supplies RequiredNames.
func CheckAllocs(results []BenchResult, limits map[string]int64, failOnUnlisted bool, requiredNames []string) []Violation {
	var out []Violation
	seen := map[string]bool{}

	for _, r := range results {
		key := r.Package + ":" + r.Name
		seen[key] = true
		limit, ok := limits[key]
		if !ok {
			if failOnUnlisted {
				out = append(out, Violation{Subject: key, Kind: "unlisted", Detail: "benchmark has no declared allocs ceiling"})
			}
			continue
		}
		if r.AllocsPerOp > limit {
			out = append(out, Violation{
				Subject: key,
				Kind:    "over-limit",
				Detail:  fmt.Sprintf("%d allocs/op exceeds ceiling %d", r.AllocsPerOp, limit),
			})
		}
	}

	for _, name := range requiredNames {
		if !seen[name] {
			out = append(out, Violation{Subject: name, Kind: "missing", Detail: "required benchmark did not run"})
		}
	}

	sortViolations(out)
	return out
}

func sortViolations(v []Violation) {
	sort.Slice(v, func(i, j int) bool {
		if v[i].Subject != v[j].Subject {
			return v[i].Subject < v[j].Subject
		}
		return v[i].Kind < v[j].Kind
	})
}

// ErrNoData reports output that contained nothing parseable, which for a gate
// is indistinguishable from a broken command and must not read as success.
var ErrNoData = errors.New("no parseable results")
