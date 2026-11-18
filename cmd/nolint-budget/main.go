// Command nolint-budget censuses //nolint suppressions and enforces the declared
// per-directory budget.
//
// A suppression is the cheapest way to weaken the gate system without touching
// a governance file: it lives in the diff of the change it excuses, has no
// reason requirement, and is covered by no hash. So the census is a gate, and
// the budget is data written by registry-gen, not by hand.
//
// Usage:
//
//	nolint-budget -root .                      # report only (no budget declared)
//	nolint-budget -root . -budget budget.json  # enforce
//	nolint-budget -root . -write               # emit the census as the budget
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/jackby03/deterministic-harness/internal/inventory"
)

func main() {
	root := flag.String("root", ".", "repository root to census")
	budgetPath := flag.String("budget", "", "JSON file with the declared per-directory budget")
	write := flag.Bool("write", false, "print the observed census as JSON and exit 0")
	allowed := flag.String("allowed", "gosec,errcheck", "comma-separated linters a //nolint may name")
	flag.Parse()

	directives, err := inventory.CensusNolint(*root)
	if err != nil {
		fail("census: %v", err)
	}

	byDir := inventory.CensusByDir(directives)

	if *write {
		encode(byDir)
		return
	}

	budget := byDir
	if *budgetPath != "" {
		budget, err = readBudget(*budgetPath)
		if err != nil {
			fail("budget: %v", err)
		}
	}

	allowedSet := map[string]bool{}
	for _, l := range splitComma(*allowed) {
		allowedSet[l] = true
	}

	fmt.Printf("//nolint census: %d directives in %d directories\n\n", len(directives), len(byDir))
	fmt.Printf("%-45s %6s %8s\n", "DIRECTORY", "COUNT", "BUDGET")
	for _, dir := range sortedKeys(byDir) {
		limit, ok := budget[dir]
		switch {
		case !ok:
			fmt.Printf("%-45s %6d %8s\n", dir, byDir[dir], "none")
		case byDir[dir] > limit:
			fmt.Printf("%-45s %6d %8d  OVER\n", dir, byDir[dir], limit)
		default:
			fmt.Printf("%-45s %6d %8d\n", dir, byDir[dir], limit)
		}
	}

	violations := inventory.CheckNolintBudget(directives, budget, allowedSet)
	if len(violations) == 0 {
		return
	}

	fmt.Printf("\n%d violation(s):\n", len(violations))
	for _, v := range violations {
		fmt.Printf("  %s: %s [%s]\n", v.Subject, v.Detail, v.Kind)
	}
	os.Exit(1)
}

func readBudget(path string) (map[string]int, error) {
	body, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return nil, err
	}
	var out map[string]int
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return out, nil
}

func encode(m map[string]int) {
	body, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		fail("encode: %v", err)
	}
	fmt.Println(string(body))
}

func sortedKeys(m map[string]int) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func splitComma(s string) []string {
	var out []string
	for _, p := range filepath.SplitList(s) {
		if p != "" {
			out = append(out, p)
		}
	}
	if len(out) == 0 && s != "" {
		return []string{s}
	}
	return out
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(2)
}
