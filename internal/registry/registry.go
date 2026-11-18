package registry

import (
	"fmt"
	"os"
	"sort"
	"strconv"
)

// Kinds is the closed set of gate kinds. A gate without a kind cannot declare
// an inventory, and a gate without an inventory has no teeth, so this list is
// the extension surface: adding a kind is a deliberate act, not a side effect.
var Kinds = map[string]bool{
	"self":              true,
	"command":           true,
	"go-test":           true,
	"go-test-selectors": true,
	"go-bench":          true,
	"go-fuzz":           true,
	"nolint":            true,
	"json-report":       true,
}

// Checks is the closed set of in-process checks a `kind: self` gate may name.
var Checks = map[string]bool{
	"registry-coverage": true,
	"registry-fresh":    true,
	"bootstrap-expired": true,
	"citations":         true,
	"traceability":      true,
}

type Registry struct {
	SchemaVersion int
	DefaultTier   int
	Bootstrap     Bootstrap
	Runtime       Runtime
	Governance    Governance
	Traceability  Traceability
	Review        Review
	Features      map[string]bool
	Gates         map[string]*Gate
	Tiers         map[int][]string
	TouchSets     map[string][]string
	Source        []byte
}

// Governance is the set of paths a worker may never edit. It lives in the
// registry so no dispatch spec has to enumerate it: a second copy in a spec is
// a copy that goes stale without anyone noticing.
type Governance struct {
	Paths []string
}

// Traceability is the project's naming of its normative documents. The framework
// has no idea what a "constitution" is; the profile says which files are citable.
type Traceability struct {
	// Docs maps the name a document is cited by ("Constitution") to its path.
	Docs map[string]string
	// EnforcedByDoc is the file that declares `enforced-by:` per rule.
	EnforcedByDoc string
}

// Review holds the reviewer policy. Triggers reuse the override vocabulary;
// limits are circuit breakers, not meters.
type Review struct {
	Agent              string
	Model              string
	MaxContextRequests int
	MonthlyCeiling     int
	PerReviewCapTokens int
}

type Bootstrap struct {
	Enabled        bool
	DeadlineDate   string
	AllowedPlanned []string
}

type Runtime struct {
	Kind     string
	Image    string
	Digest   string
	Platform string
	User     string
	Network  string
	Workdir  string
	Volumes  map[string]string
}

type Gate struct {
	Name        string
	Kind        string
	Status      string // active | planned
	Run         string
	Check       string
	Covers      []string
	Invocations []Invocation
	Inventory   map[string]any
}

type Invocation struct {
	Package  string
	Target   string
	Fuzztime string
	Env      map[string]string
}

// Load reads and decodes a registry.
func Load(path string) (*Registry, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return Decode(body, path)
}

// Decode builds the model and rejects anything the verifier cannot act on.
// Structural problems are errors here; semantic problems are reported by the
// registry-coverage gate, because a gate result is visible and an init error is
// just a stack trace.
func Decode(body []byte, path string) (*Registry, error) {
	raw, err := Parse(body, path)
	if err != nil {
		return nil, err
	}

	r := &Registry{
		Gates:     map[string]*Gate{},
		Tiers:     map[int][]string{},
		TouchSets: map[string][]string{},
		Features:  map[string]bool{},
		Source:    body,
	}

	if r.SchemaVersion, _ = raw["schema_version"].(int); r.SchemaVersion == 0 {
		return nil, fmt.Errorf("%s: schema_version is required", path)
	}
	if r.DefaultTier, _ = raw["default_tier"].(int); r.DefaultTier == 0 {
		return nil, fmt.Errorf("%s: default_tier is required and must be non-zero (fail-closed)", path)
	}

	if b, ok := raw["bootstrap"].(map[string]any); ok {
		r.Bootstrap.Enabled, _ = b["enabled"].(bool)
		r.Bootstrap.DeadlineDate, _ = b["deadline_date"].(string)
		r.Bootstrap.AllowedPlanned = strings_(b["allowed_planned"])
	}

	if rt, ok := raw["runtime"].(map[string]any); ok {
		r.Runtime.Kind, _ = rt["kind"].(string)
		r.Runtime.Image, _ = rt["image"].(string)
		r.Runtime.Digest, _ = rt["digest"].(string)
		r.Runtime.Platform, _ = rt["platform"].(string)
		r.Runtime.User, _ = rt["user"].(string)
		r.Runtime.Network, _ = rt["network"].(string)
		r.Runtime.Workdir, _ = rt["workdir"].(string)
		if vols, ok := rt["volumes"].(map[string]any); ok {
			r.Runtime.Volumes = map[string]string{}
			for k, v := range vols {
				if s, ok := v.(string); ok {
					r.Runtime.Volumes[k] = s
				}
			}
		}
	}

	if f, ok := raw["features"].(map[string]any); ok {
		for k, v := range f {
			if b, ok := v.(bool); ok {
				r.Features[k] = b
			}
		}
	}

	if gv, ok := raw["governance"].(map[string]any); ok {
		r.Governance.Paths = strings_(gv["paths"])
	}

	if tv, ok := raw["traceability"].(map[string]any); ok {
		r.Traceability.EnforcedByDoc, _ = tv["enforced_by_doc"].(string)
		r.Traceability.Docs = map[string]string{}
		if docs, ok := tv["docs"].(map[string]any); ok {
			for name, v := range docs {
				if path, ok := v.(string); ok {
					r.Traceability.Docs[name] = path
				}
			}
		}
	}

	if rv, ok := raw["review"].(map[string]any); ok {
		r.Review.Agent, _ = rv["agent"].(string)
		r.Review.Model, _ = rv["model"].(string)
		r.Review.MaxContextRequests, _ = rv["max_context_requests"].(int)
		if l, ok := rv["limit"].(map[string]any); ok {
			r.Review.MonthlyCeiling, _ = l["monthly_ceiling_reviews"].(int)
			r.Review.PerReviewCapTokens, _ = l["per_review_cap_tokens"].(int)
		}
	}

	gates, ok := raw["gates"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%s: gates is required", path)
	}
	for name, v := range gates {
		gm, ok := v.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("%s: gate %q is not a mapping", path, name)
		}
		g := &Gate{Name: name, Status: "active"}
		g.Kind, _ = gm["kind"].(string)
		if !Kinds[g.Kind] {
			return nil, fmt.Errorf("%s: gate %q has unknown kind %q", path, name, g.Kind)
		}
		if s, ok := gm["status"].(string); ok {
			g.Status = s
		}
		if g.Status != "active" && g.Status != "planned" {
			return nil, fmt.Errorf("%s: gate %q has invalid status %q", path, name, g.Status)
		}
		g.Run, _ = gm["run"].(string)
		g.Check, _ = gm["check"].(string)
		g.Covers = strings_(gm["covers"])
		g.Inventory, _ = gm["min_inventory"].(map[string]any)

		if g.Kind == "self" {
			if g.Run != "" {
				return nil, fmt.Errorf("%s: gate %q is kind self and must not declare run", path, name)
			}
			if !Checks[g.Check] {
				return nil, fmt.Errorf("%s: gate %q names unknown check %q", path, name, g.Check)
			}
		} else if g.Kind == "go-fuzz" {
			if g.Run != "" {
				return nil, fmt.Errorf("%s: gate %q is go-fuzz and must use invocations, not run", path, name)
			}
			if g.Status == "active" {
				for _, iv := range list_(gm["invocations"]) {
					im, ok := iv.(map[string]any)
					if !ok {
						return nil, fmt.Errorf("%s: gate %q has a non-mapping invocation", path, name)
					}
					inv := Invocation{}
					inv.Package, _ = im["package"].(string)
					inv.Target, _ = im["target"].(string)
					inv.Fuzztime, _ = im["fuzztime"].(string)
					if inv.Package == "" || inv.Target == "" || inv.Fuzztime == "" {
						return nil, fmt.Errorf("%s: gate %q invocation needs package, target and fuzztime", path, name)
					}
					if env, ok := im["env"].(map[string]any); ok {
						inv.Env = map[string]string{}
						for k, v := range env {
							if s, ok := v.(string); ok {
								inv.Env[k] = s
							}
						}
					}
					g.Invocations = append(g.Invocations, inv)
				}
				if len(g.Invocations) == 0 {
					return nil, fmt.Errorf("%s: gate %q is active go-fuzz with no invocations", path, name)
				}
			}
		} else if g.Run == "" {
			return nil, fmt.Errorf("%s: gate %q (kind %s) has no run", path, name, g.Kind)
		}

		if len(g.Covers) == 0 {
			return nil, fmt.Errorf("%s: gate %q declares no covers", path, name)
		}
		r.Gates[name] = g
	}

	tiers, ok := raw["tiers"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%s: tiers is required", path)
	}
	for k, v := range tiers {
		n, err := strconv.Atoi(k)
		if err != nil {
			return nil, fmt.Errorf("%s: tier key %q is not a number", path, k)
		}
		r.Tiers[n] = strings_(v)
	}

	if ts, ok := raw["touch_sets"].(map[string]any); ok {
		for k, v := range ts {
			r.TouchSets[k] = strings_(v)
		}
	}

	return r, nil
}

// GateNames returns gate names in a stable order.
func (r *Registry) GateNames() []string {
	out := make([]string, 0, len(r.Gates))
	for n := range r.Gates {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// TierGates returns the gate names required by a tier, in declared order.
func (r *Registry) TierGates(tier int) []string { return r.Tiers[tier] }

func strings_(v any) []string {
	raw := list_(v)
	out := make([]string, 0, len(raw))
	for _, item := range raw {
		if s, ok := item.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func list_(v any) []any {
	if l, ok := v.([]any); ok {
		return l
	}
	return nil
}
