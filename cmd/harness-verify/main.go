// Command harness-verify runs a tier of the registry and writes the report of
// what it observed.
//
//	harness-verify -root . -registry gates.yaml -tier 3 -base origin/main
//	harness-verify -root . -registry gates.yaml -only section-refs
//
// Exit code 0 means every gate of the tier ended green (passed or
// not_applicable). Anything else is a failure to close, never a warning.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/jackby03/deterministic-harness/internal/facts"
	"github.com/jackby03/deterministic-harness/internal/registry"
	"github.com/jackby03/deterministic-harness/internal/verify"
)

func main() {
	root := flag.String("root", ".", "repository root")
	registryPath := flag.String("registry", "gates.yaml", "registry file")
	tier := flag.Int("tier", 0, "tier to run (0 = compute from the diff)")
	base := flag.String("base", "", "git base for the diff, e.g. origin/main")
	backend := flag.String("backend", "auto", "auto | container | host (host overrides a container registry and is recorded in the report)")
	only := flag.String("only", "", "comma-separated gate names, for debugging one gate")
	out := flag.String("out", "", "write the report here (default: stdout)")
	noFacts := flag.Bool("no-facts", false, "skip fact collection; citations and freshness will not be judged")
	flag.Parse()

	reg, err := registry.Load(*registryPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "harness-verify: %v\n", err)
		os.Exit(2)
	}

	opts := verify.Options{
		Root:         *root,
		Registry:     reg,
		RegistryPath: *registryPath,
		Tier:         *tier,
		Base:         *base,
		Backend:      *backend,
		Only:         splitList(*only),
		Stdout:       os.Stdout,
	}

	if !*noFacts {
		opts.Facts, err = collectFacts(*root, reg)
		if err != nil {
			fmt.Fprintf(os.Stderr, "harness-verify: facts: %v\n", err)
			os.Exit(2)
		}
	}

	rep, err := verify.Run(opts)
	if err != nil {
		fmt.Fprintf(os.Stderr, "harness-verify: %v\n", err)
		os.Exit(2)
	}

	printSummary(rep)

	body, err := json.MarshalIndent(rep, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "harness-verify: encode: %v\n", err)
		os.Exit(2)
	}
	body = append(body, '\n')

	if *out == "" {
		if _, err := os.Stdout.Write(body); err != nil {
			fmt.Fprintf(os.Stderr, "harness-verify: write: %v\n", err)
			os.Exit(2)
		}
	} else {
		if err := os.MkdirAll(filepath.Dir(*out), 0o750); err != nil {
			fmt.Fprintf(os.Stderr, "harness-verify: %v\n", err)
			os.Exit(2)
		}
		if err := os.WriteFile(*out, body, 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "harness-verify: %v\n", err)
			os.Exit(2)
		}
	}

	if !rep.Green {
		os.Exit(1)
	}
}

func printSummary(rep verify.Report) {
	note := ""
	if rep.BackendOverride {
		note = " (OVERRIDE: registry asks for a container)"
	}
	fmt.Fprintf(os.Stderr, "tier %d (%s)  backend=%s%s  env=%s/%s %s\n",
		rep.Tier, rep.TierSource, rep.Backend, note, rep.Env.OS, rep.Env.Arch, rep.Env.GoVersion)
	fmt.Fprintf(os.Stderr, "%-24s %-18s %-18s %s\n", "GATE", "KIND", "STATUS", "DETAIL")
	for _, g := range rep.Gates {
		detail := g.Reason
		if len(g.Violations) > 0 {
			detail = g.Violations[0].String()
			if len(g.Violations) > 1 {
				detail += fmt.Sprintf(" (+%d)", len(g.Violations)-1)
			}
		}
		fmt.Fprintf(os.Stderr, "%-24s %-18s %-18s %s\n", g.Gate, g.Kind, g.Status, detail)
	}
	verdict := "GREEN"
	if !rep.Green {
		verdict = "RED"
	}
	fmt.Fprintf(os.Stderr, "%s (%d gate(s), touched=%d)\n", verdict, len(rep.Gates), len(rep.Touched))
}

// collectFacts uses the registry's own citation scope, so the profile stays the
// single source of truth for what gets scanned.
func collectFacts(root string, reg *registry.Registry) (facts.Facts, error) {
	globs := []string{"**/*.md"}
	if g, ok := reg.Gates["section-refs"]; ok {
		globs = g.Covers
	}
	return facts.Collect(facts.Options{
		Root:          root,
		CitationGlobs: globs,
		DocTargets:    reg.Traceability.Docs,
	})
}

func splitList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
