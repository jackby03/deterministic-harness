// Package facts observes a repository and produces the measured inputs that
// registry-gen writes back into the registry.
//
// Facts are deterministic: no timestamps, no map iteration order, no network.
// That determinism is what makes `registry-gen -check` meaningful, since the
// whole point is to fail when the committed registry no longer matches the tree.
package facts

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/jackby03/deterministic-harness/internal/inventory"
)

// Facts is the observed state of one repository.
type Facts struct {
	Seeds      map[string]int   `json:"seeds"`
	Citations  Citations        `json:"citations"`
	Nolint     map[string]int   `json:"nolint"`
	Benchmarks map[string]int64 `json:"benchmarks"`
}

// Citations separates what resolved from what did not. Only the resolvable
// count is a ratchet candidate; dangling references are always a failure.
type Citations struct {
	Resolvable int      `json:"resolvable"`
	Dangling   []string `json:"dangling"`
}

// Options selects what to observe. Paths are repository-relative.
type Options struct {
	Root          string
	CitationGlobs []string
	// DocTargets maps the name a document is cited by to its path, e.g.
	// {"<DocName>": "<path>"}. Both come from the profile: the framework does not
	// know what the normative documents of a project are called.
	DocTargets map[string]string
	// Bench, when true, runs the benchmarks to observe allocations. Off by
	// default: the source-derived facts are cheap and safe to collect on every
	// run, measurement is not.
	Bench         bool
	BenchPackages []string
}

// Collect walks the repository once. Skipped trees match the census rules:
// vendored code, generated trees and fixtures are not the project's facts.
func Collect(opts Options) (Facts, error) {
	root := opts.Root
	if root == "" {
		root = "."
	}

	f := Facts{
		Seeds:  map[string]int{},
		Nolint: map[string]int{},
	}

	if err := collectSeeds(root, &f); err != nil {
		return f, err
	}

	cites, err := collectCitations(root, opts)
	if err != nil {
		return f, err
	}
	f.Citations = cites

	directives, err := inventory.CensusNolint(root)
	if err != nil {
		return f, err
	}
	f.Nolint = inventory.CensusByDir(directives)

	if opts.Bench {
		if err := collectBench(root, opts.BenchPackages, &f); err != nil {
			return f, err
		}
	}

	return f, nil
}

func collectSeeds(root string, f *Facts) error {
	return filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if skipDir(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(p, "_test.go") {
			return nil
		}
		body, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		counts, err := inventory.CountFuzzSeeds(body, p)
		if err != nil {
			return fmt.Errorf("%s: %w", p, err)
		}
		dir := filepath.Dir(p)
		for target, n := range counts {
			corpus := filepath.Join(dir, "testdata", "fuzz", target)
			f.Seeds[target] = n + inventory.CountCorpusFiles(corpus)
		}
		return nil
	})
}

func collectCitations(root string, opts Options) (Citations, error) {
	headings := map[string]map[string]bool{}
	for name, rel := range opts.DocTargets {
		body, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil {
			return Citations{}, fmt.Errorf("headings for %s: %w", name, err)
		}
		headings[name] = inventory.Headings(string(body))
	}

	byFile := map[string][]inventory.Citation{}
	sources := map[string]string{}

	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if skipDir(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		rel, relErr := filepath.Rel(root, p)
		if relErr != nil {
			return relErr
		}
		rel = filepath.ToSlash(rel)
		if !Matches(opts.CitationGlobs, rel) {
			return nil
		}
		body, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		if !strings.Contains(string(body), "§") {
			return nil
		}
		sources[rel] = string(body)
		byFile[rel] = inventory.Citations(string(body))
		return nil
	})
	if err != nil {
		return Citations{}, err
	}

	out := Citations{Dangling: []string{}}
	for _, cites := range byFile {
		out.Resolvable += len(cites)
	}
	for _, v := range inventory.CheckCitations(sources, byFile, headings) {
		out.Dangling = append(out.Dangling, v.Subject)
		out.Resolvable--
	}
	sort.Strings(out.Dangling)
	return out, nil
}

func collectBench(root string, pkgs []string, f *Facts) error {
	if len(pkgs) == 0 {
		return nil
	}
	// Nil means "not measured"; an empty map means "measured, nothing found".
	// Only this function may turn nil into a real map.
	f.Benchmarks = map[string]int64{}
	args := append([]string{"test", "-run=^$", "-bench=.", "-benchmem"}, pkgs...)
	cmd := exec.Command("go", args...)
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")

	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("benchmarks: %w\n%s", err, out)
	}
	for _, r := range inventory.ParseBench(string(out)) {
		f.Benchmarks[r.Package+":"+r.Name] = r.AllocsPerOp
	}
	return nil
}

func skipDir(name string) bool {
	switch name {
	case ".git", "third_party", "node_modules", "testdata", ".artifacts", "dist", "bin":
		return true
	}
	return false
}
