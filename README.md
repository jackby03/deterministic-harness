# Deterministic Harness

A verification layer for repositories worked on by autonomous agents.

Most agent workflows fail the same way: the agent says it is done, the tests are
green, and nobody can say what was actually verified, in which environment, on
which tree. This harness replaces that with observed facts.

Three ideas carry the whole design:

1. **Facts, not claims.** A gate result is `{status, exit code, output digest,
   tool version}` for a specific `tree_sha`. A digest alone is not evidence.
2. **Nothing passes by being absent.** Every gate declares a minimum inventory —
   benchmarks, selectors, seeds, citations, suppressions — and fails if that
   inventory shrinks or disappears.
3. **The environment is part of the result.** Gates run inside a pinned container
   image; the report records its digest. "Green locally" and "green in CI" stop
   being two different claims.

## What it looks like

```
$ harness-verify -registry gates.yaml -tier 1
tier 1 (flag)  backend=container  env=linux/amd64
GATE                     KIND               STATUS             DETAIL
registry-coverage        self               failed   FEATURE_MAP.md: tier_3 glob matches no file (+9)
registry-fresh           self               passed
bootstrap-expired        self               passed
section-refs             self               failed   .golangci.yml:77: citation does not resolve (+1)
RED (4 gate(s))
```

The red is the point: the report names the missing files, not "verification
failed".

## The four layers

| Layer | What it is | Where |
| :--- | :--- | :--- |
| **Motor** | parser, five-state machine, tier computation, coverage rules, contracts, executor | `internal/` |
| **Adapters** | how a toolchain's output becomes inventory (`go-bench`, `go-test-selectors`, `go-fuzz`, `nolint`) | `internal/inventory` |
| **Profile** | every project noun: paths, ceilings, seeds, image digest, citable docs | `gates.yaml` |
| **Governance** | the project's own law and owners | your repo |

The boundary is enforced by a test: `internal/` may not contain the name of the
project it verifies. See [docs/architecture.md](docs/architecture.md).

## Install

There is no package to install. The harness is a set of `go run`-able commands,
or a single static binary:

```bash
go build -o bin/harness-verify ./cmd/harness-verify
```

Consumers that do not want Go on their machines should build it into their
verification image instead — see [docs/operations.md](docs/operations.md).

## Quickstart

1. Copy [`examples/minimal/gates.yaml`](examples/minimal/gates.yaml) to your repo
   root and replace the paths with yours.
2. Run `go run ./cmd/harness-verify -registry gates.yaml -tier 1`.
3. Fix what the red names. Then add a tier 3 for the paths where a mistake is
   expensive.

Full walkthrough, including a Node.js port: [docs/adapting.md](docs/adapting.md).

## Commands

| Command | Purpose |
| :--- | :--- |
| `harness-verify` | run a tier, write the report, exit non-zero unless green |
| `harness-facts` | observe the repo: seeds, citations, suppressions, allocations |
| `registry-gen` | rewrite the generated blocks of a registry from facts |
| `harness-validate` | check a dispatch spec, a worker report or a review verdict |
| `nolint-budget` | census of suppression directives against a declared budget |

## Documentation

- [Architecture](docs/architecture.md) — the four layers, the five states, the boundary test
- [Reference](docs/reference.md) — every key of `gates.yaml`, every kind, the report schema
- [Adapting](docs/adapting.md) — porting to another project, with a Node.js example
- [Operations](docs/operations.md) — image, digest, offline runs, generated blocks
- [Protocol](docs/protocol.md) — multi-agent roles, DAG, handoff, review policy
- [Profiles](docs/profiles.md) — session profiles for the agent runtime
- [Adoption](ADOPTION.md) — the ordered checklist for landing this in a repo
- [Publishing](PUBLISH.md) — what to change before this repo goes public

## Status

38 tests, `go vet` clean, no third-party dependencies. The Go adapters are
verified against a real project; the Node.js ones are specified in
[docs/adapting.md](docs/adapting.md) and not yet written.

## License

Not chosen yet. This is a blocker for publishing — see [PUBLISH.md](PUBLISH.md).
