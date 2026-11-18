// Package verify runs a tier of gates and records what it observed.
//
// The five states are the whole contract: a gate is passed, failed, not_run
// (the environment cannot run it), not_applicable (a declared predicate says it
// does not apply) or not_implemented (the registry says it is planned). Only
// the first two and not_applicable are green, so a missing gate can never be
// mistaken for a passing one.
package verify

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackby03/deterministic-harness/internal/facts"
	"github.com/jackby03/deterministic-harness/internal/inventory"
	"github.com/jackby03/deterministic-harness/internal/regen"
	"github.com/jackby03/deterministic-harness/internal/registry"
)

// State is the outcome of one gate.
type State string

const (
	Passed         State = "passed"
	Failed         State = "failed"
	NotRun         State = "not_run"
	NotApplicable  State = "not_applicable"
	NotImplemented State = "not_implemented"
)

// Green reports whether a state allows closing a tier.
func (s State) Green() bool { return s == Passed || s == NotApplicable }

// Result is one observed gate.
type Result struct {
	Gate       string                `json:"gate"`
	Kind       string                `json:"kind"`
	Status     State                 `json:"status"`
	Reason     string                `json:"reason,omitempty"`
	Run        string                `json:"run,omitempty"`
	ExitCode   *int                  `json:"exit_code,omitempty"`
	DurationMS int64                 `json:"duration_ms"`
	OutputSHA  string                `json:"output_sha256,omitempty"`
	Violations []inventory.Violation `json:"violations,omitempty"`
	// Tail carries the last lines of output on failure. A digest alone cannot
	// be debugged, and the agent closing the turn needs the error, not a hash.
	Tail []string `json:"tail,omitempty"`
}

// Report is the evidence for one verification run.
type Report struct {
	RegistrySHA     string   `json:"registry_sha256"`
	Tier            int      `json:"tier_computed"`
	TierSource      string   `json:"tier_source"`
	MatchedGlobs    []string `json:"matched_globs,omitempty"`
	Touched         []string `json:"touched_files"`
	Backend         string   `json:"backend"`
	BackendOverride bool     `json:"backend_override,omitempty"`
	Env             Env      `json:"env"`
	Gates           []Result `json:"gates_results"`
	Green           bool     `json:"green"`
}

// Env records the environment that produced the evidence. It is diagnosis, not
// a requirement: the binding identity is the image digest.
type Env struct {
	OS        string `json:"os"`
	Arch      string `json:"arch"`
	GoVersion string `json:"go_version"`
	Image     string `json:"image,omitempty"`
	Digest    string `json:"image_digest,omitempty"`
}

// Options configures a run.
type Options struct {
	Root         string
	Registry     *registry.Registry
	RegistryPath string
	Tier         int      // 0 means compute from the diff
	Touched      []string // nil means ask git
	Base         string
	Backend      string // host | container
	Only         []string
	Facts        facts.Facts
	Now          time.Time
	Executor     *Executor
	Stdout       *os.File
}

// ComputeTier returns the highest tier matched by any touched path. An
// unmatched path falls to default_tier, which is why the default is 2: the
// fail-closed direction for process risk is up, not down.
func ComputeTier(touched []string, r *registry.Registry) (int, []string) {
	tier := 0
	var matched []string

	consider := func(set string, n int) {
		for _, glob := range r.TouchSets[set] {
			for _, path := range touched {
				if facts.Matches([]string{glob}, path) {
					if n > tier {
						tier = n
					}
					matched = append(matched, glob)
					break
				}
			}
		}
	}

	consider("tier_1", 1)
	consider("tier_3", 3)

	if tier == 0 {
		return r.DefaultTier, nil
	}
	sort.Strings(matched)
	return tier, matched
}

// Run evaluates every gate of the tier and returns the evidence.
func Run(opts Options) (Report, error) {
	r := opts.Registry
	if opts.Root == "" {
		opts.Root = "."
	}
	if opts.Now.IsZero() {
		opts.Now = time.Now()
	}
	if opts.Backend == "" || opts.Backend == "auto" {
		opts.Backend = r.Runtime.Kind
		if opts.Backend == "" {
			opts.Backend = "host"
		}
	}
	override := strings.HasPrefix(r.Runtime.Kind, "container") && opts.Backend != r.Runtime.Kind
	if opts.Executor == nil {
		opts.Executor = NewExecutor(opts.Backend, r, opts.Root)
	}

	touched := opts.Touched
	if touched == nil {
		var err error
		touched, err = gitTouched(opts.Root, opts.Base)
		if err != nil {
			return Report{}, err
		}
	}

	computed, matched := ComputeTier(touched, r)
	tier, source := opts.Tier, "flag"
	if tier == 0 {
		tier, source = computed, "computed"
	} else if len(matched) > 0 && computed > tier {
		// Escalation is one-way, but only a real match may raise the tier: the
		// default tier is a floor for unknown paths, not a reason to override an
		// explicit light request on an empty diff.
		tier, source = computed, "escalated"
	}

	if _, ok := r.Tiers[tier]; !ok {
		return Report{}, fmt.Errorf("tier %d is not declared in the registry", tier)
	}

	names := r.TierGates(tier)
	if len(opts.Only) > 0 {
		names = opts.Only
	}

	// A gate that mutates the tree invalidates the evidence it is producing: the
	// report would describe a tree that no longer exists. Tracked files only —
	// fuzzing legitimately writes untracked corpus entries.
	before := trackedStatus(opts.Root)

	rep := Report{
		RegistrySHA:     hash(opts.Registry.Source),
		Tier:            tier,
		TierSource:      source,
		MatchedGlobs:    matched,
		Touched:         touched,
		Backend:         opts.Backend,
		BackendOverride: override,
		Env:             detectEnv(opts.Root, r),
		Green:           true,
	}

	for _, name := range names {
		g, ok := r.Gates[name]
		if !ok {
			rep.Gates = append(rep.Gates, Result{Gate: name, Status: NotImplemented, Reason: "gate is required by the tier but not declared"})
			rep.Green = false
			continue
		}
		res := evaluate(g, opts)
		if !res.Status.Green() {
			rep.Green = false
		}
		rep.Gates = append(rep.Gates, res)
	}

	if after := trackedStatus(opts.Root); after != before {
		rep.Gates = append(rep.Gates, Result{
			Gate:   "tree-mutation",
			Kind:   "self",
			Status: Failed,
			Reason: "a gate modified tracked files during the run: " + firstLineChange(before, after),
		})
		rep.Green = false
	}

	return rep, nil
}

// trackedStatus lists modifications to tracked files, ignoring untracked output.
func trackedStatus(root string) string {
	out, err := exec.Command("git", "-C", root, "status", "--porcelain", "--untracked-files=no").Output()
	if err != nil {
		return ""
	}
	return string(out)
}

// firstLineChange names one file that differs, which is all the agent needs.
func firstLineChange(before, after string) string {
	seen := map[string]bool{}
	for _, l := range strings.Split(before, "\n") {
		seen[l] = true
	}
	for _, l := range strings.Split(after, "\n") {
		if strings.TrimSpace(l) == "" {
			continue
		}
		if !seen[l] {
			return strings.TrimSpace(l)
		}
	}
	return "tracked files changed"
}

func evaluate(g *registry.Gate, opts Options) Result {
	if g.Status == "planned" {
		if opts.Registry.Bootstrap.Enabled && contains(opts.Registry.Bootstrap.AllowedPlanned, g.Name) {
			return Result{Gate: g.Name, Kind: g.Kind, Status: NotApplicable, Reason: "bootstrap: gate is planned and listed in allowed_planned"}
		}
		return Result{Gate: g.Name, Kind: g.Kind, Status: NotImplemented, Reason: "gate status is planned and not covered by bootstrap"}
	}

	if g.Kind == "self" {
		return evalSelf(g, opts)
	}
	if g.Kind == "go-fuzz" {
		return evalFuzz(g, opts)
	}
	return evalCommand(g, opts)
}

func evalSelf(g *registry.Gate, opts Options) Result {
	res := Result{Gate: g.Name, Kind: g.Kind, Status: Passed}
	v := selfCheck(g.Check, g, opts)
	res.Violations = v
	if len(v) > 0 {
		res.Status = Failed
	}
	return res
}

func selfCheck(name string, g *registry.Gate, opts Options) []inventory.Violation {
	switch name {
	case "registry-coverage":
		return coverage(g, opts.Registry, opts.Root)
	case "registry-fresh":
		return freshness(g, opts.Registry, opts.Facts)
	case "bootstrap-expired":
		return bootstrapExpiry(opts.Registry, opts.Now)
	case "citations":
		return citations(g, opts.Facts)
	case "traceability":
		return traceability(g, opts.Registry, opts.Root)
	}
	return []inventory.Violation{{Subject: name, Kind: "missing", Detail: "unknown self check"}}
}

// coverage is the registry checking itself. Each rule closes a way the registry
// could silently stop constraining anything.
func coverage(g *registry.Gate, r *registry.Registry, root string) []inventory.Violation {
	var out []inventory.Violation
	rules := map[string]bool{}
	for _, rule := range strSlice(g.Inventory["rules"]) {
		rules[rule] = true
	}

	// Coverage is judged against real files, not by intersecting glob strings.
	// Glob intersection can only be approximated with heuristics, and a
	// heuristic here would either hide a hole or invent one.
	files, errWalk := repoFiles(root)
	if errWalk != nil {
		out = append(out, inventory.Violation{Subject: root, Kind: "missing", Detail: "cannot walk the tree: " + errWalk.Error()})
		return out
	}

	if rules["all_tier3_globs_covered"] {
		active := map[string]bool{}
		for name, gate := range r.Gates {
			if gate.Status == "active" {
				active[name] = true
			}
		}
		for _, glob := range r.TouchSets["tier_3"] {
			matched := filesMatching(files, glob)
			if len(matched) == 0 {
				out = append(out, inventory.Violation{Subject: glob, Kind: "missing", Detail: "tier_3 glob matches no file in the tree"})
				continue
			}
			for _, f := range matched {
				var covering []string
				for name, gate := range r.Gates {
					if facts.Matches(gate.Covers, f) {
						covering = append(covering, name)
					}
				}
				if len(covering) == 0 {
					out = append(out, inventory.Violation{Subject: f, Kind: "missing", Detail: "file in tier_3 covered by no gate"})
					continue
				}
				if !anyActive(covering, active) && !bootstrapAllows(r) {
					out = append(out, inventory.Violation{Subject: f, Kind: "missing", Detail: "file in tier_3 covered only by planned gates: " + strings.Join(covering, ",")})
				}
			}
		}
	}

	if rules["covers_intersect_touch_sets"] {
		for _, name := range r.GateNames() {
			for _, c := range r.Gates[name].Covers {
				if !coversAnyTouchSetFile(files, c, r) {
					out = append(out, inventory.Violation{Subject: name + " covers " + c, Kind: "unjustified", Detail: "covers matches no file that any touch_set also matches"})
				}
			}
		}
	}

	if rules["no_orphan_gates"] {
		used := map[string]bool{}
		for _, names := range r.Tiers {
			for _, n := range names {
				used[n] = true
				if _, ok := r.Gates[n]; !ok {
					out = append(out, inventory.Violation{Subject: n, Kind: "missing", Detail: "tier references an undeclared gate"})
				}
			}
		}
		for _, name := range r.GateNames() {
			if !used[name] {
				out = append(out, inventory.Violation{Subject: name, Kind: "unjustified", Detail: "gate is in no tier, so it never runs"})
			}
		}
	}

	if rules["planned_gates_within_bootstrap_allowlist"] {
		for _, name := range r.GateNames() {
			if r.Gates[name].Status == "planned" && !contains(r.Bootstrap.AllowedPlanned, name) {
				out = append(out, inventory.Violation{Subject: name, Kind: "unjustified", Detail: "planned gate is not in bootstrap.allowed_planned"})
			}
		}
	}

	if rules["hermetic_runs"] {
		for _, name := range r.GateNames() {
			gate := r.Gates[name]
			if gate.Run == "" || !executesModule(gate) {
				continue
			}
			if !strings.Contains(gate.Run, "CGO_ENABLED=") {
				out = append(out, inventory.Violation{Subject: name, Kind: "unjustified", Detail: "run executes module code without pinning CGO_ENABLED"})
			}
		}
		for _, name := range r.GateNames() {
			for _, inv := range r.Gates[name].Invocations {
				if _, ok := inv.Env["CGO_ENABLED"]; !ok {
					out = append(out, inventory.Violation{Subject: name + "/" + inv.Target, Kind: "unjustified", Detail: "fuzz invocation does not pin CGO_ENABLED"})
				}
			}
		}
	}

	if rules["bootstrap_has_deadline"] {
		if r.Bootstrap.Enabled && r.Bootstrap.DeadlineDate == "" {
			out = append(out, inventory.Violation{Subject: "bootstrap", Kind: "missing", Detail: "bootstrap is enabled with no deadline_date"})
		}
	}

	if rules["covers_literals_exist"] {
		for _, name := range r.GateNames() {
			for _, c := range r.Gates[name].Covers {
				if strings.ContainsAny(c, "*?") {
					continue
				}
				if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(c))); err != nil {
					out = append(out, inventory.Violation{Subject: name + " covers " + c, Kind: "missing", Detail: "literal path does not exist"})
				}
			}
		}
	}

	return out
}

func freshness(g *registry.Gate, r *registry.Registry, f facts.Facts) []inventory.Violation {
	out, err := regen.Regenerate(r.Source, f)
	if err != nil {
		return []inventory.Violation{{Subject: "registry-fresh", Kind: "unjustified", Detail: err.Error()}}
	}
	if string(out) == string(r.Source) {
		return nil
	}
	return []inventory.Violation{{Subject: "gates.yaml", Kind: "unjustified", Detail: "generated blocks are stale: run registry-gen -write"}}
}

func bootstrapExpiry(r *registry.Registry, now time.Time) []inventory.Violation {
	if !r.Bootstrap.Enabled || r.Bootstrap.DeadlineDate == "" {
		return nil
	}
	deadline, err := time.Parse("2006-01-02", r.Bootstrap.DeadlineDate)
	if err != nil {
		return []inventory.Violation{{Subject: "bootstrap.deadline_date", Kind: "unjustified", Detail: "not a YYYY-MM-DD date"}}
	}
	if now.After(deadline) {
		return []inventory.Violation{{Subject: "bootstrap", Kind: "over-budget", Detail: "deadline passed on " + r.Bootstrap.DeadlineDate + " and bootstrap is still enabled"}}
	}
	return nil
}

func citations(g *registry.Gate, f facts.Facts) []inventory.Violation {
	var out []inventory.Violation
	for _, d := range f.Citations.Dangling {
		out = append(out, inventory.Violation{Subject: d, Kind: "dangling", Detail: "citation does not resolve to a numbered heading"})
	}
	min := intValue(g.Inventory["min_resolvable_citations"])
	if min > 0 && f.Citations.Resolvable < min {
		out = append(out, inventory.Violation{
			Subject: "citations",
			Kind:    "over-budget",
			Detail:  fmt.Sprintf("%d resolvable, floor is %d", f.Citations.Resolvable, min),
		})
	}
	return out
}

var enforcedByRe = regexp.MustCompile(`enforced-by:\s*([A-Za-z0-9_#-]+)`)

func traceability(g *registry.Gate, r *registry.Registry, root string) []inventory.Violation {
	doc := r.Traceability.EnforcedByDoc
	if doc == "" {
		return []inventory.Violation{{Subject: "traceability.enforced_by_doc", Kind: "missing", Detail: "the profile does not name the document that declares enforced-by"}}
	}
	body, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(doc)))
	if err != nil {
		return []inventory.Violation{{Subject: doc, Kind: "missing", Detail: "cannot read"}}
	}

	var out []inventory.Violation

	// A section with no annotation at all is the vacuous pass this gate exists
	// to prevent: absence of enforced-by must be louder than a bad one.
	for _, section := range strSlice(g.Inventory["sections_required"]) {
		if countInSection(string(body), section) == 0 {
			out = append(out, inventory.Violation{
				Subject: doc + " " + section,
				Kind:    "missing",
				Detail:  "section declares no enforced-by annotation",
			})
		}
	}
	for _, m := range enforcedByRe.FindAllStringSubmatch(string(body), -1) {
		gateName, selector, hasSelector := strings.Cut(m[1], "#")
		gate, ok := r.Gates[gateName]
		if !ok {
			out = append(out, inventory.Violation{Subject: m[1], Kind: "missing", Detail: "enforced-by names a gate that does not exist"})
			continue
		}
		if gate.Status != "active" {
			out = append(out, inventory.Violation{Subject: m[1], Kind: "unjustified", Detail: "enforced-by must name an active gate"})
		}
		if hasSelector {
			ok, err := selectorExists(root, selector)
			if err != nil {
				out = append(out, inventory.Violation{Subject: m[1], Kind: "missing", Detail: "cannot scan for selector: " + err.Error()})
				continue
			}
			if !ok {
				out = append(out, inventory.Violation{Subject: m[1], Kind: "missing", Detail: "selector " + selector + " does not exist"})
			}
		}
	}
	return out
}

// countInSection counts enforced-by annotations inside `## <n>.` up to the next
// `## ` heading.
func countInSection(doc, section string) int {
	n := strings.TrimPrefix(section, "§")
	head := regexp.MustCompile(`(?m)^## ` + regexp.QuoteMeta(n) + `\.`)
	loc := head.FindStringIndex(doc)
	if loc == nil {
		return 0
	}
	rest := doc[loc[1]:]
	if next := strings.Index(rest, "\n## "); next >= 0 {
		rest = rest[:next]
	}
	return len(enforcedByRe.FindAllString(rest, -1))
}

var testFuncRe = regexp.MustCompile(`(?m)^func (Test[A-Za-z0-9_]*)\(`)

func selectorExists(root, selector string) (bool, error) {
	found := false
	errWalk := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if found {
			return nil
		}
		if d.IsDir() {
			if d.Name() == "third_party" || d.Name() == ".git" {
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
		if testFuncRe.MatchString(string(body)) {
			for _, m := range testFuncRe.FindAllStringSubmatch(string(body), -1) {
				if m[1] == selector {
					found = true
					return nil
				}
			}
		}
		return nil
	})
	if errWalk != nil {
		return false, errWalk
	}
	return found, nil
}

func evalCommand(g *registry.Gate, opts Options) Result {
	start := time.Now()
	res := Result{Gate: g.Name, Kind: g.Kind, Run: g.Run}

	out := opts.Executor.Run(g.Name, g.Run, nil, g.Inventory)
	res.DurationMS = time.Since(start).Milliseconds()
	res.OutputSHA = hash(out.Output)

	if !out.Ran {
		res.Status = NotRun
		res.Reason = out.Reason
		return res
	}

	code := out.ExitCode
	res.ExitCode = &code
	res.Violations = inventoryFor(g, string(out.Output))
	if code != 0 || len(res.Violations) > 0 {
		res.Status = Failed
		res.Tail = lastLines(out.Output, 12)
		return res
	}
	res.Status = Passed
	return res
}

func inventoryFor(g *registry.Gate, out string) []inventory.Violation {
	v := nonEmptyFiles(g)
	switch g.Kind {
	case "go-bench":
		limits := map[string]int64{}
		var required []string
		for k, v := range mapValue(g.Inventory["benchmarks_max_allocs"]) {
			limits[k] = int64(intValue(v))
			required = append(required, k)
		}
		return append(v, inventory.CheckAllocs(inventory.ParseBench(out), limits, boolValue(g.Inventory["fail_on_unlisted_benchmark"]), required)...)

	case "go-test-selectors":
		statuses := inventory.ParseGoTestV(out)
		required := strSlice(g.Inventory["required_selectors"])
		v = append(v, inventory.CheckSelectors(statuses, required, boolValue(g.Inventory["forbid_skipped_selectors"]))...)
		for _, t := range strSlice(g.Inventory["required_tests"]) {
			if _, ran := statuses[t]; !ran && !contains(required, t) {
				v = append(v, inventory.Violation{Subject: t, Kind: "missing", Detail: "required test did not run"})
			}
		}
		return v

	case "json-report":
		return append(v, checkJSONReport(g, out)...)

	default:
		return v
	}
}

// nonEmptyFiles is a kind-independent inventory: a gate that reads fixture files
// must prove they exist and have content, or a truncated corpus reads as passing.
func nonEmptyFiles(g *registry.Gate) []inventory.Violation {
	var v []inventory.Violation
	for _, f := range strSlice(g.Inventory["non_empty_files"]) {
		info, err := os.Stat(filepath.FromSlash(f))
		if err != nil {
			v = append(v, inventory.Violation{Subject: f, Kind: "missing", Detail: "declared file does not exist"})
			continue
		}
		if info.Size() == 0 {
			v = append(v, inventory.Violation{Subject: f, Kind: "missing", Detail: "declared file is empty"})
		}
	}
	return v
}

func checkJSONReport(g *registry.Gate, out string) []inventory.Violation {
	var doc map[string]any
	start := strings.Index(out, "{")
	if start < 0 || json.Unmarshal([]byte(out[start:]), &doc) != nil {
		return []inventory.Violation{{Subject: g.Name, Kind: "missing", Detail: "output is not a JSON object"}}
	}
	var v []inventory.Violation
	minPassed := intValue(g.Inventory["min_passing_cases"])
	passed := intValue(doc["passed"])
	if minPassed > 0 && passed < minPassed {
		v = append(v, inventory.Violation{Subject: g.Name, Kind: "over-budget", Detail: fmt.Sprintf("%d passing cases, floor is %d", passed, minPassed)})
	}
	maxFailed := intValue(g.Inventory["max_failed_cases"])
	if failed := intValue(doc["failed"]); failed > maxFailed {
		v = append(v, inventory.Violation{Subject: g.Name, Kind: "over-budget", Detail: fmt.Sprintf("%d failed cases, ceiling is %d", failed, maxFailed)})
	}
	return v
}

func detectEnv(root string, r *registry.Registry) Env {
	e := Env{OS: runtime.GOOS, Arch: runtime.GOARCH, Image: r.Runtime.Image, Digest: r.Runtime.Digest}
	if strings.HasPrefix(r.Runtime.Digest, "sha256:") {
		e.Image = r.Runtime.Image + "@" + r.Runtime.Digest
	}
	if out, err := exec.Command("go", "env", "GOVERSION").Output(); err == nil {
		e.GoVersion = strings.TrimSpace(string(out))
	}
	return e
}

// gitTouched reports the files changed against base plus untracked files.
func gitTouched(root, base string) ([]string, error) {
	args := []string{"diff", "--name-only"}
	if base != "" {
		args = append(args, base+"...HEAD")
	}
	out, err := exec.Command("git", append([]string{"-C", root}, args...)...).Output()
	if err != nil && base != "" {
		return nil, fmt.Errorf("git diff against %s: %w", base, err)
	}

	seen := map[string]bool{}
	var files []string
	add := func(s string) {
		s = strings.TrimSpace(s)
		if s != "" && !seen[s] {
			seen[s] = true
			files = append(files, s)
		}
	}
	for _, l := range strings.Split(string(out), "\n") {
		add(l)
	}

	status, err := exec.Command("git", "-C", root, "status", "--porcelain").Output()
	if err == nil {
		for _, l := range strings.Split(string(status), "\n") {
			if len(l) > 3 {
				add(l[3:])
			}
		}
	}
	sort.Strings(files)
	return files, nil
}

// lastLines returns the last n non-empty lines, which is where a toolchain puts
// the reason it failed.
func lastLines(b []byte, n int) []string {
	var all []string
	for _, l := range strings.Split(string(b), "\n") {
		if strings.TrimSpace(l) != "" {
			all = append(all, l)
		}
	}
	if len(all) > n {
		all = all[len(all)-n:]
	}
	return all
}

func hash(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// repoFiles lists repository-relative files, skipping the trees that are not
// the project's own source.
func repoFiles(root string) ([]string, error) {
	var out []string
	errWalk := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "third_party", "node_modules", "vendor", "dist", "bin", ".artifacts":
				return filepath.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		out = append(out, filepath.ToSlash(rel))
		return nil
	})
	if errWalk != nil {
		return nil, errWalk
	}
	sort.Strings(out)
	return out, nil
}

func filesMatching(files []string, glob string) []string {
	var out []string
	for _, f := range files {
		if facts.Matches([]string{glob}, f) {
			out = append(out, f)
		}
	}
	return out
}

func coversAnyTouchSetFile(files []string, covers string, r *registry.Registry) bool {
	for _, f := range filesMatching(files, covers) {
		for _, set := range r.TouchSets {
			for _, t := range set {
				if facts.Matches([]string{t}, f) {
					return true
				}
			}
		}
	}
	return false
}

func anyActive(names []string, active map[string]bool) bool {
	for _, n := range names {
		if active[n] {
			return true
		}
	}
	return false
}

func bootstrapAllows(r *registry.Registry) bool { return r.Bootstrap.Enabled }

func executesModule(g *registry.Gate) bool {
	for _, p := range []string{"go test", "go build", "go run ./cmd", "go vet", "make "} {
		if strings.Contains(g.Run, p) {
			return true
		}
	}
	return false
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func strSlice(v any) []string {
	l, ok := v.([]any)
	if !ok {
		return nil
	}
	var out []string
	for _, x := range l {
		if s, ok := x.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func mapValue(v any) map[string]any {
	m, _ := v.(map[string]any)
	return m
}

func intValue(v any) int {
	switch n := v.(type) {
	case int:
		return n
	case float64:
		return int(n)
	case string:
		i, _ := strconv.Atoi(n)
		return i
	}
	return 0
}

func boolValue(v any) bool {
	b, _ := v.(bool)
	return b
}

func asExitError(err error, target **exec.ExitError) bool {
	return errors.As(err, target)
}

// evalFuzz spawns one process per target, because Go refuses -fuzz with more
// than one package or more than one matching target. A corpus shortfall is a
// failure even when every invocation exits zero: fuzzing nothing is not
// evidence of robustness.
func evalFuzz(g *registry.Gate, opts Options) Result {
	res := Result{Gate: g.Name, Kind: g.Kind}
	start := time.Now()
	var combined []byte

	minSeeds := intValue(g.Inventory["min_seeds_per_target"])
	for _, inv := range g.Invocations {
		line := fmt.Sprintf("go test -run=^$ -fuzz=^%s$ -fuzztime=%s %s", inv.Target, inv.Fuzztime, inv.Package)
		out := opts.Executor.Run(g.Name+"/"+inv.Target, line, inv.Env, nil)
		combined = append(combined, out.Output...)
		if !out.Ran {
			res.Status = NotRun
			res.Reason = out.Reason
			return res
		}
		if out.ExitCode != 0 {
			code := out.ExitCode
			res.ExitCode = &code
			res.Status = Failed
			res.Violations = append(res.Violations, inventory.Violation{Subject: inv.Target, Kind: "failed", Detail: "fuzz invocation failed"})
		}
		if minSeeds > 0 {
			if seeds, ok := opts.Facts.Seeds[inv.Target]; ok && seeds < minSeeds {
				res.Violations = append(res.Violations, inventory.Violation{Subject: inv.Target, Kind: "missing", Detail: fmt.Sprintf("%d seeds, minimum is %d", seeds, minSeeds)})
			}
			if _, ok := opts.Facts.Seeds[inv.Target]; !ok {
				res.Violations = append(res.Violations, inventory.Violation{Subject: inv.Target, Kind: "missing", Detail: "target has no seeds and is not in the facts"})
			}
		}
	}

	res.DurationMS = time.Since(start).Milliseconds()
	res.OutputSHA = hash(combined)
	if len(res.Violations) > 0 {
		res.Status = Failed
		return res
	}
	res.Status = Passed
	return res
}
