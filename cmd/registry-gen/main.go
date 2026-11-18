// Command registry-gen rewrites the generated blocks of gates.yaml from observed
// facts. It never touches policy keys, so it can run unattended.
//
//	registry-gen -registry gates.yaml -facts facts.json -write
//	registry-gen -registry gates.yaml -facts facts.json -check   # exit 1 on drift
//
// Run it from the same container image as the gates: the facts it writes are
// only true for that image's toolchain and architecture.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/jackby03/deterministic-harness/internal/facts"
	"github.com/jackby03/deterministic-harness/internal/regen"
)

func main() {
	registry := flag.String("registry", "gates.yaml", "registry file to rewrite")
	factsPath := flag.String("facts", "", "facts JSON produced by harness-facts (required)")
	write := flag.Bool("write", false, "rewrite the registry in place")
	check := flag.Bool("check", false, "exit 1 if the registry is stale (default when -write is absent)")
	flag.Parse()

	if *factsPath == "" {
		fail("missing -facts")
	}
	if *check && *write {
		fail("-check and -write are mutually exclusive")
	}

	var in facts.Facts
	body, err := os.ReadFile(*factsPath)
	if err != nil {
		fail("read facts: %v", err)
	}
	if err := json.Unmarshal(body, &in); err != nil {
		fail("parse facts: %v", err)
	}

	src, err := os.ReadFile(*registry)
	if err != nil {
		fail("read registry: %v", err)
	}

	out, err := regen.Regenerate(src, in)
	if err != nil {
		fail("regenerate: %v", err)
	}

	if bytes.Equal(src, out) {
		fmt.Printf("registry is fresh (%d generated block(s): %v)\n", len(regen.BlockKeys(src)), regen.BlockKeys(src))
		return
	}

	if *write {
		if err := os.WriteFile(*registry, out, 0o644); err != nil {
			fail("write registry: %v", err)
		}
		fmt.Printf("registry regenerated\n%s", firstHunk(src, out))
		return
	}

	fmt.Fprintf(os.Stderr, "registry is stale: run registry-gen -write and commit the result\n%s", firstHunk(src, out))
	os.Exit(1)
}

// firstHunk reports the first region that differs, which is all the agent needs
// to understand the drift without dumping the whole file into its context.
func firstHunk(a, b []byte) string {
	al := bytes.Split(a, []byte("\n"))
	bl := bytes.Split(b, []byte("\n"))

	for i := 0; i < len(al) && i < len(bl); i++ {
		if !bytes.Equal(al[i], bl[i]) {
			lo := i - 2
			if lo < 0 {
				lo = 0
			}
			hi := i + 3
			var sb bytes.Buffer
			fmt.Fprintf(&sb, "\n  first difference at line %d\n  --- committed\n", i+1)
			for _, l := range al[lo:min(hi, len(al))] {
				fmt.Fprintf(&sb, "  %s\n", l)
			}
			fmt.Fprintf(&sb, "  +++ regenerated\n")
			for _, l := range bl[lo:min(hi, len(bl))] {
				fmt.Fprintf(&sb, "  %s\n", l)
			}
			return sb.String()
		}
	}
	return "\n  (files differ only in length)\n"
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(2)
}
