package verify

import (
	"strings"
	"testing"
	"time"

	"github.com/jackby03/deterministic-harness/internal/facts"
	"github.com/jackby03/deterministic-harness/internal/registry"
)

func TestDockerArgsPinTheEnvironment(t *testing.T) {
	rt := registry.Runtime{
		Kind:     "container",
		Image:    "ghcr.io/x/verify",
		Digest:   "sha256:abc",
		Platform: "linux/amd64",
		User:     "1000:1000",
		Network:  "none",
		Workdir:  "/src",
		Volumes: map[string]string{
			"repo":       "/src",
			"gocache":    "waffynx-gocache:/home/gate/.cache/go-build",
			"gomodcache": "waffynx-gomodcache:/home/gate/go/pkg/mod",
			"breaker":    "waffynx-orca-breaker:/breaker",
		},
	}

	args := dockerArgs(rt, "/host/repo", "ghcr.io/x/verify@sha256:abc", "build-go", "/tmp/inv",
		"CGO_ENABLED=0 go build ./...", map[string]string{"CGO_ENABLED": "0"})

	joined := strings.Join(args, " ")
	for _, want := range []string{
		"run --rm",
		"--platform linux/amd64",
		"--user 1000:1000",
		"--network none",
		"--workdir /src",
		"-v /host/repo:/src",
		"-v waffynx-gocache:/home/gate/.cache/go-build",
		"-v /tmp/inv:/gate-inventory:ro",
		"-e GATE_NAME=build-go",
		"-e GATE_INVENTORY=/gate-inventory",
		"-e CGO_ENABLED=0",
		"ghcr.io/x/verify@sha256:abc bash -c CGO_ENABLED=0 go build ./...",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("docker args missing %q\n%s", want, joined)
		}
	}

	// The image must be referenced by digest, never by tag.
	if strings.Contains(joined, "ghcr.io/x/verify bash") {
		t.Error("image referenced without its digest")
	}
}

func TestUnpinnedDigestMakesEveryCommandGateNotRun(t *testing.T) {
	r := mustRegistry(t)
	r.Runtime.Digest = "sha256:<PINNED_AT_DIA0>"

	rep, err := Run(Options{
		Registry: r,
		Tier:     2,
		Touched:  []string{"internal/x.go"},
		Backend:  "container",
		Now:      time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		Facts:    facts.Facts{Citations: facts.Citations{Resolvable: 5}},
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	for _, g := range rep.Gates {
		// Planned gates resolve before the executor is reached: not_applicable
		// under bootstrap, not_implemented outside it.
		if g.Kind == "self" || r.Gates[g.Gate].Status != "active" {
			continue
		}
		if g.Status != NotRun {
			t.Errorf("%s: status = %s, want not_run with an unpinned digest", g.Gate, g.Status)
		}
		if !strings.Contains(g.Reason, "digest") {
			t.Errorf("%s: reason %q does not explain the digest", g.Gate, g.Reason)
		}
	}
	if rep.Green {
		t.Error("report is green while every executable gate could not run")
	}
}

func TestSelfChecksStillRunWithoutAContainer(t *testing.T) {
	r := mustRegistry(t)
	r.Runtime.Digest = "sha256:<unpinned>"

	rep, err := Run(Options{
		Registry: r,
		Tier:     1,
		Touched:  []string{"docs/a.md"},
		Backend:  "container",
		Now:      time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		Facts:    facts.Facts{Citations: facts.Citations{Resolvable: 5}},
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	for _, g := range rep.Gates {
		if g.Kind == "self" && g.Status == NotRun {
			t.Errorf("%s: a self check must not need the image", g.Gate)
		}
	}
}
