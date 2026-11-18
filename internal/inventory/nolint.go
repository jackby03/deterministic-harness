package inventory

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// NolintDirective is one `//nolint` suppression found in the tree.
type NolintDirective struct {
	File      string
	Line      int
	Linters   string // "gosec", "gosec,errcheck", or "" when unscoped
	Reason    string // text after the directive's `//`
	Justified bool   // Reason is non-empty
}

// CensusNolint walks root for Go files and records every suppression.
//
// The census exists because a suppression is the cheapest way to weaken the
// gate system without touching a governance file: it looks like code, it lives
// in the diff of the very change it excuses, and no hash covers it.
func CensusNolint(root string) ([]NolintDirective, error) {
	var out []NolintDirective

	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "third_party", "node_modules", "testdata":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			rel = path
		}
		out = append(out, parseNolint(filepath.ToSlash(rel), string(body))...)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

func parseNolint(rel, body string) []NolintDirective {
	var out []NolintDirective
	for i, line := range strings.Split(body, "\n") {
		idx := strings.Index(line, "//nolint")
		if idx < 0 {
			continue
		}
		rest := line[idx+len("//nolint"):]

		d := NolintDirective{File: rel, Line: i + 1}
		if strings.HasPrefix(rest, ":") {
			rest = rest[1:]
			if cut := strings.IndexAny(rest, " \t/"); cut >= 0 {
				d.Linters = rest[:cut]
				rest = rest[cut:]
			} else {
				d.Linters = rest
				rest = ""
			}
		}
		if cut := strings.Index(rest, "//"); cut >= 0 {
			d.Reason = strings.TrimSpace(rest[cut+2:])
		}
		d.Justified = d.Reason != ""
		out = append(out, d)
	}
	return out
}

// CheckNolintBudget enforces three things at once: the total per directory may
// not exceed the declared budget, every directive must name its linters, and
// every directive must carry a reason. An unscoped `//nolint` disables every
// linter on that line, which is never a considered decision.
func CheckNolintBudget(directives []NolintDirective, budget map[string]int, allowedLinters map[string]bool) []Violation {
	var out []Violation
	byDir := map[string]int{}

	for _, d := range directives {
		dir := filepath.ToSlash(filepath.Dir(d.File))
		byDir[dir]++
		subject := fmt.Sprintf("%s:%d", d.File, d.Line)

		if !d.Justified {
			out = append(out, Violation{Subject: subject, Kind: "unjustified", Detail: "//nolint without a reason"})
		}
		if d.Linters == "" {
			out = append(out, Violation{Subject: subject, Kind: "unjustified", Detail: "unscoped //nolint disables every linter"})
			continue
		}
		for _, l := range strings.Split(d.Linters, ",") {
			l = strings.TrimSpace(l)
			if l == "" || l == "all" {
				out = append(out, Violation{Subject: subject, Kind: "unjustified", Detail: "//nolint:all is not a scoped suppression"})
				continue
			}
			if len(allowedLinters) > 0 && !allowedLinters[l] {
				out = append(out, Violation{Subject: subject, Kind: "unjustified", Detail: "linter " + l + " is not in the allowed set"})
			}
		}
	}

	for _, dir := range sortedStrings(byDir) {
		limit, ok := budget[dir]
		if !ok {
			out = append(out, Violation{Subject: dir, Kind: "over-budget", Detail: fmt.Sprintf("%d //nolint in a directory with no budget", byDir[dir])})
			continue
		}
		if byDir[dir] > limit {
			out = append(out, Violation{Subject: dir, Kind: "over-budget", Detail: fmt.Sprintf("%d //nolint exceeds budget %d", byDir[dir], limit)})
		}
	}
	sortViolations(out)
	return out
}

// CensusByDir is the stable form written into the registry by registry-gen.
func CensusByDir(directives []NolintDirective) map[string]int {
	out := map[string]int{}
	for _, d := range directives {
		out[filepath.ToSlash(filepath.Dir(d.File))]++
	}
	return out
}
