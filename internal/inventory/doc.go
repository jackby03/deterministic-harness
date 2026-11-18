package inventory

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

var (
	headingRe  = regexp.MustCompile(`(?m)^#{2,3}\s+([0-9]+(?:\.[0-9]+)?)\.?\s`)
	citationRe = regexp.MustCompile(`(Constitution|AGENTS\.md)\s*§\s*([0-9]+(?:\.[0-9]+)?)`)
)

// Citation is one `§X.Y` reference. Target is the name as written in the
// document; resolving a name to a file is the profile's job, because the
// framework has no idea which documents a project treats as normative.
type Citation struct {
	Target  string
	Section string
	Line    int
}

// Headings collects the numbered headings a citation may resolve to.
// Only `##`/`###` headings carrying an explicit number are addressable;
// ordered list items are not, because their numbering is implicit and unstable.
func Headings(md string) map[string]bool {
	out := map[string]bool{}
	for _, m := range headingRe.FindAllStringSubmatch(md, -1) {
		out[m[1]] = true
	}
	return out
}

// Citations extracts every `§X.Y` reference and names its target as written.
func Citations(text string) []Citation {
	var out []Citation
	for i, line := range strings.Split(text, "\n") {
		for _, m := range citationRe.FindAllStringSubmatch(line, -1) {
			out = append(out, Citation{Target: m[1], Section: m[2], Line: i + 1})
		}
	}
	return out
}

// CheckCitations reports references whose target has no such heading.
// headings is keyed by file name; a citation to a document absent from the map
// is dangling, because an unresolvable target is never evidence.
func CheckCitations(sources map[string]string, citationsByFile map[string][]Citation, headings map[string]map[string]bool) []Violation {
	var out []Violation

	for _, file := range sortedCitations(citationsByFile) {
		for _, c := range citationsByFile[file] {
			hs, ok := headings[c.Target]
			if !ok {
				out = append(out, Violation{
					Subject: fmt.Sprintf("%s:%d", file, c.Line),
					Kind:    "dangling",
					Detail:  fmt.Sprintf("§%s points to %s, which was not scanned", c.Section, c.Target),
				})
				continue
			}
			if !hs[c.Section] {
				out = append(out, Violation{
					Subject: fmt.Sprintf("%s:%d", file, c.Line),
					Kind:    "dangling",
					Detail:  fmt.Sprintf("§%s has no numbered heading in %s", c.Section, c.Target),
				})
			}
		}
	}
	sortViolations(out)
	return out
}

func sortedCitations(m map[string][]Citation) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func sortedStrings[T any](m map[string]T) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
