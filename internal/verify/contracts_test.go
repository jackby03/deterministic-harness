package verify

import (
	"strings"
	"testing"

	"github.com/jackby03/deterministic-harness/internal/inventory"
)

const validSpec = `
task: "n1"
base: "a1b2c3d4e5f60718293a4b5c6d7e8f9012345678"
target: "wire the engine hot path"
may_edit: ["internal/engine/**"]
tier: 3
gates: [docs-only, core, planned-gate, unlisted]
acceptance: "allocs-zero-engine passes with 0 allocs/op"
evidence:
  check: "tree_sha_matches_report"
turn_cap: 6
`

func TestValidSpecPasses(t *testing.T) {
	r := mustRegistry(t)
	r.Governance.Paths = []string{"gates.yaml", "constitution.md", "tools/**"}

	sp, err := ParseSpec([]byte(validSpec), "spec.yaml")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if v := ValidateSpec(r, sp); len(v) != 0 {
		t.Fatalf("valid spec rejected: %v", v)
	}
}

func TestSpecRejectsGovernanceEdits(t *testing.T) {
	r := mustRegistry(t)
	r.Governance.Paths = []string{"gates.yaml", "tools/**"}

	sp, _ := ParseSpec([]byte(strings.Replace(validSpec,
		`may_edit: ["internal/engine/**"]`, `may_edit: ["tools/harness/**"]`, 1)), "spec.yaml")

	v := ValidateSpec(r, sp)
	if !hasViolation(v, "may_edit tools/harness/**") {
		t.Fatalf("governance overlap not rejected: %v", v)
	}
}

func TestSpecTierCannotBeLowerThanTheTouchSet(t *testing.T) {
	r := mustRegistry(t)
	sp, _ := ParseSpec([]byte(strings.Replace(validSpec, "tier: 3", "tier: 1", 1)), "spec.yaml")

	v := ValidateSpec(r, sp)
	if !hasViolation(v, "tier") {
		t.Fatalf("tier downgrade not rejected: %v", v)
	}
}

func TestSpecRequiresAcceptanceAndBase(t *testing.T) {
	r := mustRegistry(t)
	for _, tc := range []struct{ name, from, to, subject string }{
		{"no acceptance", `acceptance: "allocs-zero-engine passes with 0 allocs/op"`, `acceptance: ""`, "acceptance"},
		{"base is a branch", `base: "a1b2c3d4e5f60718293a4b5c6d7e8f9012345678"`, `base: "main"`, "base"},
		{"no turn cap", `turn_cap: 6`, `turn_cap: 0`, "turn_cap"},
	} {
		sp, _ := ParseSpec([]byte(strings.Replace(validSpec, tc.from, tc.to, 1)), "spec.yaml")
		v := ValidateSpec(r, sp)
		if !hasViolation(v, tc.subject) {
			t.Errorf("%s: not rejected: %v", tc.name, v)
		}
	}
}

func TestReportContract(t *testing.T) {
	good := `outcome: done
commit: a1b2c3d4e5f60718293a4b5c6d7e8f9012345678
tree_sha: a1b2c3d4e5f60718293a4b5c6d7e8f9012345678
report: .artifacts/verify-report.json (green)
gates: build-go=passed, race=passed
blockers:
asks:
`
	if _, v := ValidateReport([]byte(good)); len(v) != 0 {
		t.Fatalf("good report rejected: %v", v)
	}

	red := strings.Replace(good, "(green)", "(red)", 1)
	if _, v := ValidateReport([]byte(red)); !hasViolation(v, "outcome") {
		t.Fatalf("done with a red report not rejected: %v", v)
	}

	blocked := `outcome: blocked
commit: a1b2c3d4e5f60718293a4b5c6d7e8f9012345678
tree_sha: a1b2c3d4e5f60718293a4b5c6d7e8f9012345678
report: (red)
blockers:
`
	if _, v := ValidateReport([]byte(blocked)); !hasViolation(v, "blockers") {
		t.Fatalf("blocked without blockers not rejected: %v", v)
	}

	verbose := good + strings.Repeat("noise: filler\n", 12)
	if _, v := ValidateReport([]byte(verbose)); !hasViolation(v, "report") {
		t.Fatalf("over-long report not rejected: %v", v)
	}
}

func TestVerdictContract(t *testing.T) {
	approve := `{"verdict":"approve","against":{"commit":"a1b2c3d4e5f60718293a4b5c6d7e8f9012345678"},"checked":["intent_vs_spec"],"reasons":[],"context_requests":[]}`
	if _, v := ValidateVerdict([]byte(approve), 3); len(v) != 0 {
		t.Fatalf("valid approve rejected: %v", v)
	}

	emptyReject := `{"verdict":"reject","against":{"commit":"a1b2c3d4e5f60718293a4b5c6d7e8f9012345678"},"reasons":[]}`
	if _, v := ValidateVerdict([]byte(emptyReject), 3); !hasViolation(v, "verdict") {
		t.Fatalf("reject without a blocking reason not rejected: %v", v)
	}

	contradiction := `{"verdict":"approve","against":{"commit":"a1b2c3d4e5f60718293a4b5c6d7e8f9012345678"},
		"reasons":[{"severity":"blocking","file":"x.go","why":"because"}]}`
	if _, v := ValidateVerdict([]byte(contradiction), 3); !hasViolation(v, "verdict") {
		t.Fatalf("approve with a blocking reason not rejected: %v", v)
	}

	narrative := `{"verdict":"approve","against":{"commit":"a1b2c3d4e5f60718293a4b5c6d7e8f9012345678"},
		"context_requests":[{"question":"why?","artifact":"internal/x.go"}]}`
	if _, v := ValidateVerdict([]byte(narrative), 3); !hasViolation(v, "context_requests internal/x.go") {
		t.Fatalf("narrative answer not rejected: %v", v)
	}

	tooMany := `{"verdict":"approve","against":{"commit":"a1b2c3d4e5f60718293a4b5c6d7e8f9012345678"},
		"context_requests":[
			{"question":"q","artifact":"a.go#L1-L2"},
			{"question":"q","artifact":"b.go#L1-L2"},
			{"question":"q","artifact":"c.go#L1-L2"},
			{"question":"q","artifact":"d.go#L1-L2"}]}`
	if _, v := ValidateVerdict([]byte(tooMany), 3); !hasViolation(v, "context_requests") {
		t.Fatalf("over-limit context requests not rejected: %v", v)
	}
}

func hasViolation(v []inventory.Violation, subject string) bool {
	for _, x := range v {
		if strings.Contains(x.Subject, subject) || strings.Contains(x.Detail, subject) {
			return true
		}
	}
	return false
}
