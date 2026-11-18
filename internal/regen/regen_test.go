package regen

import (
	"strings"
	"testing"

	"github.com/jackby03/deterministic-harness/internal/facts"
)

const fixture = `# a registry
schema_version: 1

gates:
  allocs:
    kind: go-bench
    min_inventory:
      fail_on_unlisted_benchmark: true     # policy, must survive untouched
      # BEGIN GENERATED:benchmarks
      observed_allocs:
        "old:stale": 999
      # END GENERATED:benchmarks
  fuzz:
    kind: go-fuzz
    min_inventory:
      min_seeds_per_target: 1              # policy, must survive untouched
      # BEGIN GENERATED:seeds
      observed_seeds:
        "StaleTarget": 0
      # END GENERATED:seeds
  refs:
    kind: self
    min_inventory:
      min_resolvable_citations: 25         # policy, must survive untouched
      # BEGIN GENERATED:citations
      observed:
        resolvable: 0
      # END GENERATED:citations
`

func fixtureFacts() facts.Facts {
	return facts.Facts{
		Seeds:      map[string]int{"FuzzA": 3, "FuzzB": 1},
		Citations:  facts.Citations{Resolvable: 24, Dangling: []string{".golangci.yml:77"}},
		Nolint:     map[string]int{},
		Benchmarks: map[string]int64{"./internal/rules:BenchmarkX": 1},
	}
}

func TestRegenerateReplacesOnlyGeneratedBlocks(t *testing.T) {
	out, err := Regenerate([]byte(fixture), fixtureFacts())
	if err != nil {
		t.Fatalf("regenerate: %v", err)
	}
	got := string(out)

	for _, want := range []string{
		`"FuzzA": 3`,
		`"FuzzB": 1`,
		`"./internal/rules:BenchmarkX": 1`,
		`resolvable: 24`,
		`- ".golangci.yml:77"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in output:\n%s", want, got)
		}
	}

	// Stale values inside the blocks are replaced, not appended.
	for _, stale := range []string{"stale", "StaleTarget", "999"} {
		if strings.Contains(got, stale) {
			t.Errorf("stale value %q survived:\n%s", stale, got)
		}
	}

	// Policy keys and comments outside the blocks survive byte for byte.
	for _, keep := range []string{
		"# a registry",
		"fail_on_unlisted_benchmark: true     # policy, must survive untouched",
		"min_seeds_per_target: 1              # policy, must survive untouched",
		"min_resolvable_citations: 25         # policy, must survive untouched",
	} {
		if !strings.Contains(got, keep) {
			t.Errorf("policy line lost: %q", keep)
		}
	}
}

func TestRegenerateIsIdempotent(t *testing.T) {
	once, err := Regenerate([]byte(fixture), fixtureFacts())
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	twice, err := Regenerate(once, fixtureFacts())
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if string(once) != string(twice) {
		t.Fatalf("not idempotent:\n--- once\n%s\n--- twice\n%s", once, twice)
	}
}

func TestRegenerateIsDeterministic(t *testing.T) {
	a, err := Regenerate([]byte(fixture), fixtureFacts())
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		b, err := Regenerate([]byte(fixture), fixtureFacts())
		if err != nil {
			t.Fatal(err)
		}
		if string(a) != string(b) {
			t.Fatal("map iteration order leaked into the output")
		}
	}
}

func TestRegenerateKeepsIndentation(t *testing.T) {
	out, err := Regenerate([]byte(fixture), fixtureFacts())
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(out), "\n") {
		if strings.Contains(line, `"FuzzA"`) && !strings.HasPrefix(line, "      ") {
			t.Fatalf("generated line lost its indent: %q", line)
		}
	}
}

func TestRegenerateErrorsAreLoud(t *testing.T) {
	cases := map[string]string{
		"unclosed":  "# BEGIN GENERATED:seeds\n  a: 1\n",
		"unknown":   "# BEGIN GENERATED:nope\n# END GENERATED:nope\n",
		"no blocks": "schema_version: 1\n",
		"duplicate": "# BEGIN GENERATED:seeds\n# END GENERATED:seeds\n" +
			"# BEGIN GENERATED:seeds\n# END GENERATED:seeds\n",
	}
	for name, src := range cases {
		if _, err := Regenerate([]byte(src), fixtureFacts()); err == nil {
			t.Errorf("%s: expected an error, got none", name)
		}
	}
}

func TestBlockKeys(t *testing.T) {
	keys := BlockKeys([]byte(fixture))
	want := []string{"benchmarks", "seeds", "citations"}
	if strings.Join(keys, ",") != strings.Join(want, ",") {
		t.Fatalf("keys = %v, want %v", keys, want)
	}
}

func TestUnmeasuredBlockIsLeftUntouched(t *testing.T) {
	in := fixtureFacts()
	in.Benchmarks = nil // not measured this run

	out, err := Regenerate([]byte(fixture), in)
	if err != nil {
		t.Fatalf("regenerate: %v", err)
	}
	if !strings.Contains(string(out), `"old:stale": 999`) {
		t.Fatalf("unmeasured block was rewritten instead of skipped:\n%s", out)
	}
	if got := SkippedBlocks(in); len(got) != 1 || got[0] != "benchmarks" {
		t.Fatalf("SkippedBlocks = %v, want [benchmarks]", got)
	}
}
