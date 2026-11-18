package facts

import (
	"os"
	"path/filepath"
	"testing"
)

func write(t *testing.T, root, rel, body string) {
	t.Helper()
	full := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func fixtureTree(t *testing.T) string {
	t.Helper()
	root := t.TempDir()

	write(t, root, "constitution.md", "# Constitution\n\n## 1. Core\n\n### 1.1 Fail-Closed\n\n### 1.2 Hot Path\n")
	write(t, root, "AGENTS.md", "# AGENTS\n\n## 5. Essential Commands\n\n## 7. Definition of Done\n")

	write(t, root, "internal/x/fuzz_test.go", `package x

import "testing"

func FuzzA(f *testing.F) {
	f.Add([]byte("one"))
	f.Add([]byte("two"))
	f.Fuzz(func(t *testing.T, b []byte) {})
}

func FuzzB(f *testing.F) {
	f.Fuzz(func(t *testing.T, b []byte) {})
}
`)
	write(t, root, "internal/x/testdata/fuzz/FuzzA/seed-1", "go test fuzz v1\n[]byte(\"seeded\")\n")

	write(t, root, "internal/x/a.go", "package x\n\nvar _ = f() //nolint:gosec // G703: cleaned\n")

	write(t, root, "docs/guide.md", "See Constitution §1.1 and AGENTS.md §5.2.\n")
	write(t, root, "internal/x/b.go", "package x\n\n// See Constitution §1.2\n")

	return root
}

func TestCollectObservesSummaryFacts(t *testing.T) {
	root := fixtureTree(t)

	got, err := Collect(Options{
		Root:          root,
		CitationGlobs: []string{"constitution.md", "AGENTS.md", "docs/**", "internal/**"},
		DocTargets:    map[string]string{"Constitution": "constitution.md", "AGENTS.md": "AGENTS.md"},
	})
	if err != nil {
		t.Fatalf("collect: %v", err)
	}

	if got.Seeds["FuzzA"] != 3 {
		t.Errorf("FuzzA seeds = %d, want 3 (2 f.Add + 1 corpus file)", got.Seeds["FuzzA"])
	}
	if got.Seeds["FuzzB"] != 0 {
		t.Errorf("FuzzB seeds = %d, want 0", got.Seeds["FuzzB"])
	}
	if got.Nolint["internal/x"] != 1 {
		t.Errorf("nolint census = %v, want internal/x:1", got.Nolint)
	}
	if got.Citations.Resolvable != 2 {
		t.Errorf("resolvable = %d, want 2", got.Citations.Resolvable)
	}
	if len(got.Citations.Dangling) != 1 || got.Citations.Dangling[0] != "docs/guide.md:1" {
		t.Errorf("dangling = %v, want [docs/guide.md:1]", got.Citations.Dangling)
	}
}

func TestCollectSkipsVendoredTrees(t *testing.T) {
	root := t.TempDir()
	write(t, root, "third_party/z/z.go", "package z\nvar _ = f() //nolint:all\n")
	write(t, root, "internal/x/testdata/fuzz/FuzzA/seed", "x")
	write(t, root, "internal/x/testdata/gen.go", "package x\nvar _ = f() //nolint:all\n")

	got, err := Collect(Options{Root: root})
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	if len(got.Nolint) != 0 {
		t.Errorf("census leaked into vendored/generated trees: %v", got.Nolint)
	}
}

func TestGlobDialect(t *testing.T) {
	cases := []struct {
		globs []string
		path  string
		want  bool
	}{
		{[]string{"docs/**"}, "docs/a/b.md", true},
		{[]string{"docs/**"}, "docsx/a.md", false},
		{[]string{"internal/**"}, "internal/engine/x.go", true},
		{[]string{"**/*.md"}, "specs/f/spec.md", true},
		{[]string{"Makefile"}, "Makefile", true},
		{[]string{"Makefile"}, "Makefile.bak", false},
		{[]string{".golangci.yml", "docs/**"}, ".golangci.yml", true},
		{[]string{"scripts/**"}, "scripts/dev/x.sh", true},
	}
	for _, c := range cases {
		if got := Matches(c.globs, c.path); got != c.want {
			t.Errorf("Matches(%v, %q) = %v, want %v", c.globs, c.path, got, c.want)
		}
	}
}

func TestUnmeasuredBenchmarksStayNil(t *testing.T) {
	got, err := Collect(Options{Root: fixtureTree(t)})
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	if got.Benchmarks != nil {
		t.Fatalf("Benchmarks = %v, want nil when not measured", got.Benchmarks)
	}
}
