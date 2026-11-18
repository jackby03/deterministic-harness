// Command harness-validate checks the three protocol contracts: a dispatch spec,
// a worker report and a review verdict.
//
//	harness-validate -kind spec    -registry gates.yaml spec.yaml
//	harness-validate -kind report  report.txt
//	harness-validate -kind verdict verdict.json
//
// Exit 0 means the contract holds. Anything else must be fixed before the
// artifact is used, because each one feeds a decision nobody re-reads.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/jackby03/deterministic-harness/internal/registry"
	"github.com/jackby03/deterministic-harness/internal/verify"
)

func main() {
	kind := flag.String("kind", "", "spec | report | verdict")
	registryPath := flag.String("registry", "gates.yaml", "registry file (needed for spec)")
	maxContext := flag.Int("max-context-requests", 0, "override review.max_context_requests")
	flag.Parse()

	if *kind == "" {
		fail("missing -kind")
	}

	body, err := readInput(flag.Args())
	if err != nil {
		fail("%v", err)
	}

	var violations []string
	switch *kind {
	case "spec":
		reg, err := registry.Load(*registryPath)
		if err != nil {
			fail("registry: %v", err)
		}
		sp, err := verify.ParseSpec(body, "spec")
		if err != nil {
			fail("spec: %v", err)
		}
		for _, v := range verify.ValidateSpec(reg, sp) {
			violations = append(violations, v.String())
		}

	case "report":
		_, vs := verify.ValidateReport(body)
		for _, v := range vs {
			violations = append(violations, v.String())
		}

	case "verdict":
		max := *maxContext
		if max <= 0 {
			if reg, err := registry.Load(*registryPath); err == nil {
				max = reg.Review.MaxContextRequests
			}
		}
		_, vs := verify.ValidateVerdict(body, max)
		for _, v := range vs {
			violations = append(violations, v.String())
		}

	default:
		fail("unknown -kind %q", *kind)
	}

	if len(violations) == 0 {
		fmt.Printf("%s: ok\n", *kind)
		return
	}
	for _, v := range violations {
		fmt.Fprintf(os.Stderr, "  %s\n", v)
	}
	fmt.Fprintf(os.Stderr, "%s: %d violation(s)\n", *kind, len(violations))
	os.Exit(1)
}

func readInput(args []string) ([]byte, error) {
	if len(args) == 0 || args[0] == "-" {
		return io.ReadAll(os.Stdin)
	}
	return os.ReadFile(args[0])
}

func fail(format string, a ...any) {
	fmt.Fprintf(os.Stderr, "harness-validate: "+format+"\n", a...)
	os.Exit(2)
}
