package verify

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackby03/deterministic-harness/internal/facts"
	"github.com/jackby03/deterministic-harness/internal/registry"
)

const miniRegistry = `
schema_version: 12
default_tier: 2
bootstrap:
  enabled: true
  deadline_date: "2026-01-31"
  allowed_planned: ["planned-gate"]
runtime:
  kind: container
  image: "img"
  digest: "sha256:abc"
  platform: "linux/amd64"
gates:
  docs-only:
    kind: self
    check: citations
    covers: ["docs/**"]
    min_inventory:
      min_resolvable_citations: 1
  core:
    kind: command
    run: "true"
    covers: ["internal/**"]
  planned-gate:
    kind: command
    status: planned
    run: "true"
    covers: ["modules/**"]
  unlisted:
    kind: command
    status: planned
    run: "true"
    covers: ["other/**"]
tiers:
  1: [docs-only]
  2: [docs-only, core]
  3: [docs-only, core, planned-gate, unlisted]
touch_sets:
  tier_1: ["docs/**"]
  tier_3: ["internal/**", "modules/**", "nothing/**"]
`

func mustRegistry(t *testing.T) *registry.Registry {
	t.Helper()
	r, err := registry.Decode([]byte(miniRegistry), "gates.yaml")
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	return r
}

func TestComputeTierTakesTheMaximum(t *testing.T) {
	r := mustRegistry(t)

	cases := []struct {
		name    string
		touched []string
		want    int
	}{
		{"docs only", []string{"docs/a.md"}, 1},
		{"docs and core", []string{"docs/a.md", "internal/x.go"}, 3},
		{"unmapped falls to default", []string{"README2.md"}, 2},
		{"nested under a tier3 glob", []string{"internal/engine/x.go"}, 3},
	}
	for _, c := range cases {
		got, _ := ComputeTier(c.touched, r)
		if got != c.want {
			t.Errorf("%s: tier = %d, want %d", c.name, got, c.want)
		}
	}
}

func TestRunEscalatesButNeverDowngrades(t *testing.T) {
	r := mustRegistry(t)

	rep, err := Run(Options{
		Registry: r,
		Tier:     1, // human asked for light
		Touched:  []string{"internal/engine/x.go"},
		Backend:  "host",
		Now:      time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		Facts:    facts.Facts{Citations: facts.Citations{Resolvable: 5}},
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if rep.Tier != 3 {
		t.Fatalf("tier = %d, want 3 (escalation is one-way)", rep.Tier)
	}
	if rep.TierSource != "escalated" {
		t.Fatalf("tier_source = %q, want escalated", rep.TierSource)
	}
}

func TestPlannedGatesResolveToBootstrapOrNotImplemented(t *testing.T) {
	r := mustRegistry(t)

	rep, err := Run(Options{
		Registry: r,
		Tier:     3,
		Touched:  []string{"internal/x.go"},
		Backend:  "host",
		Now:      time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		Facts:    facts.Facts{Citations: facts.Citations{Resolvable: 5}},
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	got := map[string]Result{}
	for _, g := range rep.Gates {
		got[g.Gate] = g
	}

	if got["planned-gate"].Status != NotApplicable {
		t.Errorf("planned gate inside allowed_planned = %v, want not_applicable", got["planned-gate"].Status)
	}
	if got["unlisted"].Status != NotImplemented {
		t.Errorf("planned gate outside the allowlist = %v, want not_implemented", got["unlisted"].Status)
	}
	if rep.Green {
		t.Error("report is green with a not_implemented gate")
	}
}

func TestBootstrapExpiryIsDeterministic(t *testing.T) {
	r := mustRegistry(t)

	before := bootstrapExpiry(r, time.Date(2026, 1, 30, 0, 0, 0, 0, time.UTC))
	if len(before) != 0 {
		t.Fatalf("deadline not reached but gate failed: %v", before)
	}
	after := bootstrapExpiry(r, time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC))
	if len(after) != 1 {
		t.Fatalf("deadline passed but got %d violations", len(after))
	}
}

func TestDecodeRejectsUnusableRegistries(t *testing.T) {
	cases := map[string]string{
		"unknown kind": `
schema_version: 1
default_tier: 2
gates:
  g:
    kind: magic
    run: "true"
    covers: ["x/**"]
tiers:
  1: [g]
`,
		"self with run": `
schema_version: 1
default_tier: 2
gates:
  g:
    kind: self
    check: citations
    run: "true"
    covers: ["x/**"]
tiers:
  1: [g]
`,
		"no covers": `
schema_version: 1
default_tier: 2
gates:
  g:
    kind: command
    run: "true"
tiers:
  1: [g]
`,
		"no default tier": `
schema_version: 1
gates:
  g:
    kind: command
    run: "true"
    covers: ["x/**"]
tiers:
  1: [g]
`,
		"duplicate key": `
schema_version: 1
default_tier: 2
default_tier: 3
gates:
  g: {kind: command, run: "true", covers: ["x/**"]}
tiers:
  1: [g]
`,
	}
	for name, src := range cases {
		if _, err := registry.Decode([]byte(src), "gates.yaml"); err == nil {
			t.Errorf("%s: expected an error, got none", name)
		}
	}
}

func TestCoverageFlagsGhostGlobs(t *testing.T) {
	r := mustRegistry(t)
	root := t.TempDir()
	for _, f := range []string{"internal/x.go", "docs/a.md", "modules/y.c", "README.md"} {
		full := filepath.Join(root, filepath.FromSlash(f))
		if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	g := &registry.Gate{
		Name:      "registry-coverage",
		Kind:      "self",
		Check:     "registry-coverage",
		Inventory: map[string]any{"rules": []any{"all_tier3_globs_covered"}},
	}

	v := coverage(g, r, root)
	var ghost bool
	for _, x := range v {
		if x.Subject == "nothing/**" {
			ghost = true
		}
		if x.Subject == "internal/x.go" {
			t.Fatalf("covered file reported as uncovered: %v", x)
		}
	}
	if !ghost {
		t.Fatalf("ghost glob not reported: %v", v)
	}
}
