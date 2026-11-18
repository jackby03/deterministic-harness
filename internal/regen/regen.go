// Package regen rewrites the generated blocks of a registry in place.
//
// It deliberately does not parse or re-serialise YAML. A round-trip through a
// YAML library would reorder keys, drop comments and reflow quoting, turning
// every regeneration into an unreviewable diff. Instead the registry marks the
// regions that are derived with `# BEGIN GENERATED:<key>` / `# END GENERATED:<key>`
// and everything outside those regions is preserved byte for byte.
//
// Consequences that matter:
//   - Generated blocks hold observed facts only. Policy keys (min_*, budget,
//     tiers, touch_sets) are never emitted, so regeneration can never lower a
//     ratchet or widen a budget.
//   - A missing marker is an error, not a silent no-op: silent no-ops are how
//     a registry stops matching the tree.
package regen

import (
	"fmt"
	"sort"
	"strings"

	"github.com/jackby03/deterministic-harness/internal/facts"
)

// Input is the observed state written by harness-facts. It is the same shape,
// by construction: sharing the type is what keeps the wire format from drifting
// away from what the renderers expect.

// Renderers maps a block key to the lines it owns, without indentation.
var Renderers = map[string]func(facts.Facts) []string{
	"seeds": func(in facts.Facts) []string { return intMapLines(in.Seeds) },
	"citations": func(in facts.Facts) []string {
		out := []string{fmt.Sprintf("resolvable: %d", in.Citations.Resolvable)}
		if len(in.Citations.Dangling) == 0 {
			return append(out, "dangling: []")
		}
		out = append(out, "dangling:")
		for _, d := range in.Citations.Dangling {
			out = append(out, "  - "+quote(d))
		}
		return out
	},
	"nolint": func(in facts.Facts) []string { return intMapLines(in.Nolint) },
	"benchmarks": func(in facts.Facts) []string {
		keys := make([]string, 0, len(in.Benchmarks))
		for k := range in.Benchmarks {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		out := make([]string, 0, len(keys))
		for _, k := range keys {
			out = append(out, fmt.Sprintf("%s: %d", quote(k), in.Benchmarks[k]))
		}
		return out
	},
}

func intMapLines(m map[string]int) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		out = append(out, fmt.Sprintf("%s: %d", quote(k), m[k]))
	}
	return out
}

// Keys are always quoted. Benchmarks and paths contain `:` and `/`, and a
// target named `on` would otherwise be read as a YAML boolean. Quoting every
// key keeps generated diffs stable instead of style-dependent.
func quote(s string) string {
	return fmt.Sprintf("%q", s)
}

// Regenerate returns src with every generated block replaced by the rendered
// facts. Everything outside the markers is preserved byte for byte.
func Regenerate(src []byte, in facts.Facts) ([]byte, error) {
	text := string(src)
	trailing := strings.HasSuffix(text, "\n")
	lines := strings.Split(strings.TrimSuffix(text, "\n"), "\n")

	var out []string
	seen := map[string]bool{}

	for i := 0; i < len(lines); i++ {
		line := lines[i]
		key, indent, ok := marker(line, "BEGIN")
		if !ok {
			out = append(out, line)
			continue
		}
		if seen[key] {
			return nil, fmt.Errorf("duplicate generated block %q at line %d", key, i+1)
		}
		seen[key] = true

		render, known := Renderers[key]
		if !known {
			return nil, fmt.Errorf("unknown generated block %q at line %d", key, i+1)
		}

		// Find the matching end marker.
		end := -1
		for j := i + 1; j < len(lines); j++ {
			if k, _, isEnd := marker(lines[j], "END"); isEnd && k == key {
				end = j
				break
			}
		}
		if end < 0 {
			return nil, fmt.Errorf("generated block %q is not closed", key)
		}

		out = append(out, line)
		if !Renderable(key, in) {
			// Measured facts that were not measured this run must not be
			// deleted: absence of a measurement is not a measurement of zero.
			out = append(out, lines[i+1:end+1]...)
			i = end
			continue
		}
		for _, l := range render(in) {
			if l == "" {
				out = append(out, l)
				continue
			}
			out = append(out, indent+l)
		}
		out = append(out, lines[end])
		i = end
	}

	if len(seen) == 0 {
		return nil, fmt.Errorf("no generated blocks found")
	}

	res := strings.Join(out, "\n")
	if trailing {
		res += "\n"
	}
	return []byte(res), nil
}

// marker recognises "# BEGIN GENERATED:<key>" and "# END GENERATED:<key>",
// returning the key and the indentation that generated lines must adopt.
func marker(line, word string) (key, indent string, ok bool) {
	trimmed := strings.TrimLeft(line, " \t")
	indent = line[:len(line)-len(trimmed)]

	prefix := "# " + word + " GENERATED:"
	if !strings.HasPrefix(trimmed, prefix) {
		return "", "", false
	}
	key = strings.TrimSpace(strings.TrimPrefix(trimmed, prefix))
	if key == "" {
		return "", "", false
	}
	return key, indent, true
}

// Renderable reports whether the run produced the facts a block needs. The
// measured blocks are the only ones that can be absent, and an absent
// measurement must never be written as a zero.
func Renderable(key string, in facts.Facts) bool {
	if key == "benchmarks" {
		return in.Benchmarks != nil
	}
	_, known := Renderers[key]
	return known
}

// SkippedBlocks lists the blocks this run cannot refresh, so the caller can say
// so out loud instead of leaving a stale block looking fresh.
func SkippedBlocks(in facts.Facts) []string {
	var out []string
	for _, key := range []string{"benchmarks"} {
		if !Renderable(key, in) {
			out = append(out, key)
		}
	}
	return out
}

// BlockKeys reports the generated blocks present in src, in file order.
func BlockKeys(src []byte) []string {
	var keys []string
	for _, line := range strings.Split(string(src), "\n") {
		if k, _, ok := marker(line, "BEGIN"); ok {
			keys = append(keys, k)
		}
	}
	return keys
}
