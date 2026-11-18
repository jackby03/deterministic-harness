package inventory

import (
	"os"
	"path/filepath"
	"testing"
)

// The five measured lines from the real repo (linux/amd64, go1.22.12). They are
// a fixture on purpose: if the parser silently starts reading a different
// column, the baselines in the registry would drift without any gate noticing.
const realBenchOutput = `goos: linux
goarch: amd64
pkg: github.com/jackby03/waffynx/internal/rules
cpu: AMD Ryzen 5 3500X 6-Core Processor
BenchmarkEvaluate_QueryParam_Single-6    	 9799684	       110.1 ns/op	      64 B/op	       1 allocs/op
BenchmarkEvaluate_QueryParam_Multi-6     	 7820853	       145.0 ns/op	      64 B/op	       1 allocs/op
BenchmarkEvaluate_QueryParam_NoMatch-6   	 9390098	       126.8 ns/op	      64 B/op	       1 allocs/op
pkg: github.com/jackby03/waffynx/internal/appsec
BenchmarkBasicScorer_Evaluate_Clean-6   	   79873	     14119 ns/op	    4024 B/op	      35 allocs/op
BenchmarkBasicScorer_CheckPatterns-6    	  271180	      5241 ns/op	     960 B/op	      12 allocs/op
`

func TestParseBenchMatchesMeasuredBaselines(t *testing.T) {
	got := ParseBench(realBenchOutput)
	if len(got) != 5 {
		t.Fatalf("parsed %d benchmarks, want 5", len(got))
	}

	want := map[string]int64{
		"github.com/jackby03/waffynx/internal/rules:BenchmarkEvaluate_QueryParam_Single":  1,
		"github.com/jackby03/waffynx/internal/rules:BenchmarkEvaluate_QueryParam_Multi":   1,
		"github.com/jackby03/waffynx/internal/rules:BenchmarkEvaluate_QueryParam_NoMatch": 1,
		"github.com/jackby03/waffynx/internal/appsec:BenchmarkBasicScorer_Evaluate_Clean": 35,
		"github.com/jackby03/waffynx/internal/appsec:BenchmarkBasicScorer_CheckPatterns":  12,
	}

	for _, r := range got {
		key := r.Package + ":" + r.Name
		n, ok := want[key]
		if !ok {
			t.Fatalf("unexpected benchmark key %q", key)
		}
		if r.AllocsPerOp != n {
			t.Errorf("%s: allocs/op = %d, want %d", key, r.AllocsPerOp, n)
		}
	}
}

// Case 1 — allocs regression: a synthetic benchmark that allocates against a
// zero ceiling must be rejected. This is the check that keeps §1.2 from
// decaying into a comment.
func TestSelftestAllocsRejectsRegression(t *testing.T) {
	output := "pkg: ./internal/engine\n" +
		"BenchmarkEngineHotPath-8   1000000   500.0 ns/op   16 B/op   1 allocs/op\n"

	results := ParseBench(output)
	if len(results) != 1 {
		t.Fatalf("parsed %d results, want 1", len(results))
	}

	limits := map[string]int64{"./internal/engine:BenchmarkEngineHotPath": 0}
	v := CheckAllocs(results, limits, true, []string{"./internal/engine:BenchmarkEngineHotPath"})
	if len(v) != 1 || v[0].Kind != "over-limit" {
		t.Fatalf("want one over-limit violation, got %v", v)
	}
}

// Case 2 — silence: a benchmark with no declared ceiling must not pass.
func TestSelftestUnlistedBenchmarkRejected(t *testing.T) {
	output := "pkg: ./internal/rules\n" +
		"BenchmarkEvaluate_QueryParam_BrandNew-6   100   200 ns/op   8 B/op   3 allocs/op\n"

	limits := map[string]int64{"./internal/rules:BenchmarkEvaluate_QueryParam_Single": 1}
	v := CheckAllocs(ParseBench(output), limits, true, nil)
	if len(v) != 1 || v[0].Kind != "unlisted" {
		t.Fatalf("want one unlisted violation, got %v", v)
	}
}

// Case 3 — a required selector that did not run, or that skipped, is not
// evidence. Both are rejected.
func TestSelftestSelectorSkipAndAbsenceRejected(t *testing.T) {
	output := "=== RUN   TestSidecarSocketPermissions\n" +
		"--- PASS: TestSidecarSocketPermissions (0.01s)\n" +
		"=== RUN   TestSocketPermissions\n" +
		"--- SKIP: TestSocketPermissions (0.00s)\n" +
		"    main_test.go:14: POSIX socket permissions are only enforced on Linux\n"

	statuses := ParseGoTestV(output)
	required := []string{"TestSidecarSocketPermissions", "TestSocketPermissions", "TestNeverRan"}

	v := CheckSelectors(statuses, required, true)
	if len(v) != 2 {
		t.Fatalf("want 2 violations (skipped + missing), got %d: %v", len(v), v)
	}
	got := map[string]string{}
	for _, x := range v {
		got[x.Subject] = x.Kind
	}
	if got["TestSocketPermissions"] != "skipped" {
		t.Errorf("skipped selector not reported: %v", got)
	}
	if got["TestNeverRan"] != "missing" {
		t.Errorf("absent selector not reported: %v", got)
	}
}

// Case 4 — a dangling citation must be rejected. Mirrors the real
// .golangci.yml:77 -> AGENTS.md §7.2 case, where the target has ## 7 but no ### 7.2.
func TestSelftestDanglingCitationRejected(t *testing.T) {
	agents := "# AGENTS\n\n## 5. Essential Commands\n\n## 7. Definition of Done\n\n1. Tests pass\n"
	constitution := "# Constitution\n\n## 1. Core\n\n### 1.1 Fail-Closed\n"

	source := "// see Constitution §1.1\n// see AGENTS.md §7.2\n// see AGENTS.md §5\n"

	headings := map[string]map[string]bool{
		"AGENTS.md":    Headings(agents),
		"Constitution": Headings(constitution),
	}
	cites := map[string][]Citation{"doc.md": Citations(source)}

	v := CheckCitations(map[string]string{"doc.md": source}, cites, headings)
	if len(v) != 1 {
		t.Fatalf("want exactly 1 dangling violation, got %d: %v", len(v), v)
	}
	if v[0].Kind != "dangling" || v[0].Subject != "doc.md:2" {
		t.Fatalf("wrong violation: %v", v[0])
	}
}

// Case 5 — a fuzz target with no seeds is a target with no corpus, and it must
// not read as coverage.
func TestSelftestSeedlessTargetRejected(t *testing.T) {
	src := `package parsers

import "testing"

func FuzzInspectGraphQL(f *testing.F) {
	f.Fuzz(func(t *testing.T, data []byte) {})
}

func FuzzInspectFileUpload(f *testing.F) {
	f.Add([]byte("seed"))
	f.Add([]byte("other"))
	f.Fuzz(func(t *testing.T, data []byte) {})
}
`
	seeds, err := CountFuzzSeeds([]byte(src), "fuzz_test.go")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if seeds["FuzzInspectGraphQL"] != 0 || seeds["FuzzInspectFileUpload"] != 2 {
		t.Fatalf("seed counts wrong: %v", seeds)
	}

	v := CheckSeeds(seeds, 1)
	if len(v) != 1 || v[0].Subject != "FuzzInspectGraphQL" {
		t.Fatalf("want the seedless target rejected, got %v", v)
	}
}

// Case 6 — an unjustified or unscoped suppression is a weakening that lives in
// ordinary code, so it must be rejected on its own terms.
func TestSelftestUnjustifiedNolintRejected(t *testing.T) {
	src := "package x\n\n" +
		"a := f() //nolint\n" +
		"b := f() //nolint:gosec\n" +
		"c := f() //nolint:gosec // G703: path is cleaned against a fixed root\n"

	directives := parseNolint("internal/api/x.go", src)
	if len(directives) != 3 {
		t.Fatalf("found %d directives, want 3", len(directives))
	}

	budget := map[string]int{"internal/api": 3}
	allowed := map[string]bool{"gosec": true}
	v := CheckNolintBudget(directives, budget, allowed)

	got := map[string]string{}
	for _, x := range v {
		got[x.Subject] = x.Kind
	}
	if got["internal/api/x.go:3"] != "unjustified" {
		t.Errorf("unscoped //nolint not rejected: %v", got)
	}
	if got["internal/api/x.go:4"] != "unjustified" {
		t.Errorf("//nolint without reason not rejected: %v", got)
	}
	if _, bad := got["internal/api/x.go:5"]; bad {
		t.Errorf("justified, scoped //nolint wrongly rejected: %v", got)
	}
}

func TestNolintBudgetEnforced(t *testing.T) {
	src := "package x\n\n" +
		"a := f() //nolint:gosec // 1\n" +
		"b := f() //nolint:gosec // 2\n"

	directives := parseNolint("internal/upstream/u.go", src)
	v := CheckNolintBudget(directives, map[string]int{"internal/upstream": 1}, map[string]bool{"gosec": true})
	if len(v) != 1 || v[0].Kind != "over-budget" {
		t.Fatalf("want one over-budget violation, got %v", v)
	}
}

// TestCensusOnFixtureTree keeps the walker honest about what it skips:
// vendored and generated trees must not inflate the census.
func TestCensusOnFixtureTree(t *testing.T) {
	root := t.TempDir()
	mustWrite := func(rel, body string) {
		full := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	mustWrite("internal/a/a.go", "package a\nvar _ = f() //nolint:gosec // ok\n")
	mustWrite("internal/a/a_test.go", "package a\nvar _ = f() //nolint:errcheck // ok\n")
	mustWrite("third_party/x/x.go", "package x\nvar _ = f() //nolint:all\n")
	mustWrite("internal/a/testdata/f.go", "package a\nvar _ = f() //nolint:all\n")

	directives, err := CensusNolint(root)
	if err != nil {
		t.Fatalf("census: %v", err)
	}
	if len(directives) != 2 {
		t.Fatalf("census found %d directives, want 2: %v", len(directives), directives)
	}
	if got := CensusByDir(directives)["internal/a"]; got != 2 {
		t.Errorf("CensusByDir[internal/a] = %d, want 2", got)
	}
}
