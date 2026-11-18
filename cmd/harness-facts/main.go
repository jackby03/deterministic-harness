// Command harness-facts observes the repository and writes the facts that
// registry-gen applies to the registry.
//
//	harness-facts -root . > facts.json
//	harness-facts -root <repo> -registry gates.yaml > facts.json
//
// Without -measure it only reads the tree (seeds, citations, suppressions):
// cheap, deterministic, safe on every settle. With -measure it also runs the
// benchmarks, which is only meaningful inside the pinned verify image, because
// allocations depend on the toolchain and the architecture.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/jackby03/deterministic-harness/internal/facts"
	"github.com/jackby03/deterministic-harness/internal/registry"
)

func main() {
	root := flag.String("root", ".", "repository root")
	registryPath := flag.String("registry", "", "registry file: when set, citation scope and citable docs come from the profile")
	globs := flag.String("citation-globs", "", "comma-separated globs scanned for § citations (default: profile, else **/*.md)")
	docs := flag.String("docs", "", "citation document name=path pairs (default: profile)")
	measure := flag.Bool("measure", false, "also run benchmarks to observe allocations")
	benchPkgs := flag.String("bench-pkgs", "", "comma-separated packages for -measure")
	out := flag.String("out", "", "write facts JSON here instead of stdout")
	flag.Parse()

	citationGlobs := splitList(*globs)
	docTargets := parseDocs(*docs)

	// El perfil manda: el framework no sabe que este repo tiene una "constitución".
	if *registryPath != "" {
		reg, err := registry.Load(*registryPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "harness-facts: registry: %v\n", err)
			os.Exit(2)
		}
		if len(citationGlobs) == 0 {
			if g, ok := reg.Gates["section-refs"]; ok {
				citationGlobs = g.Covers
			}
		}
		if len(docTargets) == 0 {
			docTargets = reg.Traceability.Docs
		}
	}

	opts := facts.Options{
		Root:          *root,
		CitationGlobs: citationGlobs,
		DocTargets:    docTargets,
		Bench:         *measure,
		BenchPackages: splitList(*benchPkgs),
	}

	observed, err := facts.Collect(opts)
	if err != nil {
		fmt.Fprintf(os.Stderr, "harness-facts: %v\n", err)
		os.Exit(2)
	}

	body, err := json.MarshalIndent(observed, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "harness-facts: encode: %v\n", err)
		os.Exit(2)
	}
	body = append(body, '\n')

	if *out == "" {
		if _, err := os.Stdout.Write(body); err != nil {
			fmt.Fprintf(os.Stderr, "harness-facts: write: %v\n", err)
			os.Exit(2)
		}
		return
	}
	if err := os.WriteFile(*out, body, 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "harness-facts: write: %v\n", err)
		os.Exit(2)
	}
	fmt.Printf("facts written to %s: %d fuzz targets, %d citations resolvable, %d suppressed lines, %d benchmarks\n",
		*out, len(observed.Seeds), observed.Citations.Resolvable, total(observed.Nolint), len(observed.Benchmarks))
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

func parseDocs(s string) map[string]string {
	out := map[string]string{}
	for _, pair := range splitList(s) {
		name, path, ok := strings.Cut(pair, "=")
		if ok {
			out[name] = path
			continue
		}
		out[pair] = pair
	}
	return out
}

func total(m map[string]int) int {
	n := 0
	for _, v := range m {
		n += v
	}
	return n
}
