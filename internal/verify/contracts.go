package verify

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/jackby03/deterministic-harness/internal/inventory"
	"github.com/jackby03/deterministic-harness/internal/registry"
)

// This file implements the three contracts that make the multi-agent protocol
// more than prose: the dispatch spec, the worker report and the review verdict.
// Each is validated by the harness, not by the reader, which is the only way a
// format can be enforced without a human in the loop.

var shaRe = regexp.MustCompile(`^[0-9a-f]{7,40}$`)

// --------------------------------------------------------------------- spec

// Spec is a compiled dispatch spec.
type Spec struct {
	Task       string
	Base       string
	Target     string
	MayEdit    []string
	Tier       int
	Gates      []string
	Acceptance string
	Check      string
	TurnCap    int
}

// ParseSpec accepts the same YAML subset as the registry, so there is one
// parser and one dialect in the whole system.
func ParseSpec(body []byte, path string) (*Spec, error) {
	raw, err := registry.Parse(body, path)
	if err != nil {
		return nil, err
	}
	sp := &Spec{}
	sp.Task, _ = raw["task"].(string)
	sp.Base, _ = raw["base"].(string)
	sp.Target, _ = raw["target"].(string)
	sp.Acceptance, _ = raw["acceptance"].(string)
	sp.Tier, _ = raw["tier"].(int)
	sp.TurnCap, _ = raw["turn_cap"].(int)
	sp.MayEdit = strSlice(raw["may_edit"])
	sp.Gates = strSlice(raw["gates"])
	if ev, ok := raw["evidence"].(map[string]any); ok {
		sp.Check, _ = ev["check"].(string)
	}
	return sp, nil
}

// ValidateSpec checks a spec against the registry. Every rule here exists
// because breaking it costs a worktree: the wrong base rewrites verified work,
// a loose may_edit touches governance, a low tier skips gates.
func ValidateSpec(r *registry.Registry, sp *Spec) []inventory.Violation {
	var out []inventory.Violation

	add := func(subject, kind, detail string) {
		out = append(out, inventory.Violation{Subject: subject, Kind: kind, Detail: detail})
	}

	if strings.TrimSpace(sp.Task) == "" {
		add("task", "missing", "task id is required")
	}
	if strings.TrimSpace(sp.Target) == "" {
		add("target", "missing", "a worker needs a one-sentence target")
	}
	if strings.TrimSpace(sp.Acceptance) == "" {
		add("acceptance", "missing", "no verifiable acceptance criterion")
	}
	if !shaRe.MatchString(sp.Base) {
		add("base", "unjustified", "base must be a commit sha: a node that starts from main rewrites verified work")
	}
	if sp.TurnCap <= 0 {
		add("turn_cap", "missing", "turn cap is required")
	} else if sp.TurnCap > 20 {
		add("turn_cap", "over-budget", "turn cap above 20 defeats the breaker")
	}

	if len(sp.MayEdit) == 0 {
		add("may_edit", "missing", "a worker with no allowlist may edit anything")
	}
	for _, g := range sp.MayEdit {
		for _, gov := range r.Governance.Paths {
			if globsOverlap(g, gov) {
				add("may_edit "+g, "unjustified", "overlaps governance path "+gov)
			}
		}
	}

	// Tier is derived from what the worker may touch, not declared by it.
	required := requiredTier(r, sp.MayEdit)
	if sp.Tier < required {
		add("tier", "over-budget", fmt.Sprintf("declared %d, required %d by may_edit", sp.Tier, required))
	}
	if _, ok := r.Tiers[sp.Tier]; !ok {
		add("tier", "missing", fmt.Sprintf("tier %d is not declared in the registry", sp.Tier))
	} else {
		for _, need := range r.Tiers[sp.Tier] {
			if !contains(sp.Gates, need) {
				add("gates", "missing", "spec omits "+need+", which the tier requires")
			}
		}
		for _, g := range sp.Gates {
			if _, ok := r.Gates[g]; !ok {
				add("gates "+g, "missing", "spec names an undeclared gate")
			}
		}
	}

	if sp.Check == "" {
		add("evidence.check", "missing", "evidence must name a check the verifier evaluates")
	} else if sp.Check != "tree_sha_matches_report" {
		add("evidence.check", "unjustified", "unknown check "+sp.Check)
	}

	return out
}

// requiredTier takes the maximum tier any allowed glob could touch.
func requiredTier(r *registry.Registry, mayEdit []string) int {
	tier := 1
	for _, glob := range mayEdit {
		for _, t3 := range r.TouchSets["tier_3"] {
			if globsOverlap(glob, t3) {
				return 3
			}
		}
		if globsOverlap(glob, "docs/**") {
			continue
		}
		if tier < r.DefaultTier {
			tier = r.DefaultTier
		}
	}
	return tier
}

// globsOverlap compares the literal prefixes of two globs. It is deliberately
// conservative: a `**` has an empty prefix, so it overlaps everything and gets
// rejected. False negatives here would weaken the allowlist, so erring towards
// overlap is the safe direction.
func globsOverlap(a, b string) bool {
	pa, pb := globPrefix(a), globPrefix(b)
	return strings.HasPrefix(pa, pb) || strings.HasPrefix(pb, pa)
}

func globPrefix(g string) string {
	if i := strings.IndexAny(g, "*?["); i >= 0 {
		return g[:i]
	}
	return g
}

// ------------------------------------------------------------------- report

// WorkerReport is the fixed ≤15-line report a worker sends back.
type WorkerReport struct {
	Outcome  string
	Commit   string
	TreeSHA  string
	Green    bool
	Blockers []string
	Raw      string
}

var reportOutcomes = map[string]bool{"done": true, "blocked": true, "needs-spec": true}

// ValidateReport enforces the shape the coordinator depends on. Prose here is
// the most expensive token in the system, because the coordinator is the one
// context that receives a wake from every worker.
func ValidateReport(raw []byte) (*WorkerReport, []inventory.Violation) {
	var out []inventory.Violation
	rep := &WorkerReport{Raw: string(raw)}
	add := func(subject, kind, detail string) {
		out = append(out, inventory.Violation{Subject: subject, Kind: kind, Detail: detail})
	}

	lines := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
	if len(lines) > 15 {
		add("report", "over-budget", fmt.Sprintf("%d lines, ceiling is 15", len(lines)))
	}

	inBlockers := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(trimmed, "outcome:"):
			rep.Outcome = strings.TrimSpace(strings.TrimPrefix(trimmed, "outcome:"))
		case strings.HasPrefix(trimmed, "commit:"):
			rep.Commit = strings.TrimSpace(strings.TrimPrefix(trimmed, "commit:"))
		case strings.HasPrefix(trimmed, "tree_sha:"):
			rep.TreeSHA = strings.TrimSpace(strings.TrimPrefix(trimmed, "tree_sha:"))
		case strings.HasPrefix(trimmed, "report:"):
			rep.Green = strings.Contains(trimmed, "green") && !strings.Contains(trimmed, "red")
		case strings.HasPrefix(trimmed, "blockers:"):
			inBlockers = true
		case inBlockers && strings.HasPrefix(trimmed, "-"):
			rep.Blockers = append(rep.Blockers, strings.TrimSpace(strings.TrimPrefix(trimmed, "-")))
		}
	}

	if !reportOutcomes[rep.Outcome] {
		add("outcome", "unjustified", "outcome must be done, blocked or needs-spec")
	}
	if !shaRe.MatchString(rep.Commit) {
		add("commit", "missing", "commit sha is required")
	}
	if !shaRe.MatchString(rep.TreeSHA) {
		add("tree_sha", "missing", "the tree that was verified is required")
	}

	switch rep.Outcome {
	case "done":
		if !rep.Green {
			add("outcome", "unjustified", "done requires a green verification report")
		}
		// A dirty tree may legitimately differ from its commit: the coordinator
		// compares report.tree_sha with the tree it verified, and does not guess.
	case "blocked":
		if len(rep.Blockers) == 0 {
			add("blockers", "missing", "blocked requires at least one blocker line")
		}
	}

	return rep, out
}

// ------------------------------------------------------------------ verdict

// Verdict is the reviewer's only output.
type Verdict struct {
	Verdict         string     `json:"verdict"`
	Against         Ref        `json:"against"`
	Checked         []string   `json:"checked"`
	Reasons         []Reason   `json:"reasons"`
	ContextRequests []ContextQ `json:"context_requests"`
}

type Ref struct {
	Commit  string `json:"commit"`
	TreeSHA string `json:"tree_sha"`
}

type Reason struct {
	Severity string `json:"severity"`
	File     string `json:"file"`
	Line     int    `json:"line"`
	Why      string `json:"why"`
}

type ContextQ struct {
	Question string `json:"question"`
	Artifact string `json:"artifact"`
}

var verdicts = map[string]bool{"approve": true, "reject": true, "needs-spec": true}
var severities = map[string]bool{"blocking": true, "nit": true}

// ValidateVerdict makes a verdict actionable: a reject without a blocking
// reason is an opinion, and an opinion costs money without moving the change.
func ValidateVerdict(body []byte, maxContextRequests int) (*Verdict, []inventory.Violation) {
	var out []inventory.Violation
	add := func(subject, kind, detail string) {
		out = append(out, inventory.Violation{Subject: subject, Kind: kind, Detail: detail})
	}

	var v Verdict
	if err := json.Unmarshal(body, &v); err != nil {
		add("verdict", "unjustified", "not valid JSON: "+err.Error())
		return nil, out
	}

	if !verdicts[v.Verdict] {
		add("verdict", "unjustified", "must be approve, reject or needs-spec")
	}
	if !shaRe.MatchString(v.Against.Commit) {
		add("against.commit", "missing", "the verdict must name the commit it judged")
	}
	if maxContextRequests <= 0 {
		maxContextRequests = 3
	}
	if len(v.ContextRequests) > maxContextRequests {
		add("context_requests", "over-budget", strconv.Itoa(len(v.ContextRequests))+" requests, ceiling is "+strconv.Itoa(maxContextRequests))
	}
	for _, q := range v.ContextRequests {
		if q.Question == "" || q.Artifact == "" {
			add("context_requests", "missing", "each request needs a question and an artifact (path#Lx-Ly)")
		}
		if !strings.Contains(q.Artifact, "#L") {
			add("context_requests "+q.Artifact, "unjustified", "answers must be artifacts, not narrative")
		}
	}

	blocking := 0
	for _, r := range v.Reasons {
		if !severities[r.Severity] {
			add("reasons", "unjustified", "severity must be blocking or nit")
		}
		if r.Severity == "blocking" {
			blocking++
		}
		if r.Severity == "blocking" && (r.File == "" || r.Why == "") {
			add("reasons", "missing", "a blocking reason needs file and why")
		}
	}

	switch v.Verdict {
	case "reject":
		if blocking == 0 {
			add("verdict", "unjustified", "reject requires at least one blocking reason")
		}
	case "approve":
		if blocking > 0 {
			add("verdict", "unjustified", "approve with a blocking reason contradicts itself")
		}
	}

	return &v, out
}
