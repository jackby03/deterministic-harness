# Architecture

## The problem this solves

An agent finishes a task and reports success. Three questions decide whether that
report is worth anything:

1. **What was verified?** A specific tree, or whatever happened to be on disk?
2. **In which environment?** The agent's laptop, or the one that will run in
   production?
3. **Could the gate have failed?** Or is it a check that passes because there is
   nothing left to check?

This harness answers all three as data: `tree_sha`, `image_digest`, and a
declared minimum inventory per gate.

## Four layers

```
┌──────────────────────────────────────────────────────────────┐
│ governance   constitution.md · CODEOWNERS · AGENTS.md        │  human
├──────────────────────────────────────────────────────────────┤
│ profile      gates.yaml                                      │  data
├──────────────────────────────────────────────────────────────┤
│ motor        registry · verify · regen · contracts           │  code
│ adapters     inventory (go-bench, go-test-selectors, …)      │  code
└──────────────────────────────────────────────────────────────┘
```

**Motor** is project-agnostic. It knows about tiers, states, inventories,
digests and coverage. It knows nothing about Go, Node, benchmarks or
constitutions.

**Adapters** translate one toolchain's output into violations. They are the only
place where a language appears, and they are named by `kind`:

| kind | adapter |
| :--- | :--- |
| `command` | exit code only |
| `go-test` | `go test` output |
| `go-test-selectors` | `go test -v`, per-test status, skips forbidden |
| `go-bench` | `-benchmem` allocations against a ceiling |
| `go-fuzz` | one invocation per target, seeds counted from AST + corpus |
| `nolint` | suppression census against a budget |
| `json-report` | structured tool output, floors and ceilings |
| `self` | in-process checks the motor runs itself |

**Profile** is `gates.yaml`: every project noun. Paths, ceilings, seeds, the
image digest, which documents are citable.

**Governance** is the repository's own law: who owns the gate files, what the
invariants are, who may approve a weakening.

## The boundary, made executable

The rule:

> A project noun never appears in the motor; it appears in the profile.
> A verb (the state machine) never appears in the profile; it appears in the motor.

`purity/purity_test.go` enforces both halves:

- `TestFrameworkHasNoProjectNouns` — `internal/` contains no project file name,
  path or module path, not even in a comment. Ecosystem conventions
  (`third_party`, `node_modules`, `Makefile`) are allowed; `cmd/` is exempt
  because it is the project's wiring, not the library.
- `TestProfileCarriesTheProjectNouns` — the specimen profile must actually
  declare them. Without this half the boundary could be moved by deletion: a
  clean motor and a project with no facts.

Four known leaks remain, each with its destination:

| Leak | Where | Should be |
| :--- | :--- | :--- |
| `executesModule` matches `"go test"`, `"make "` strings | `verify.go` | a `kind` field (`executes: true`) |
| Three tiers assumed (`tier_3` in coverage rules) | `verify.go` | N-tier schema, or documented as a limit |
| Skip-dir list (`third_party`, `node_modules`) | `facts`, `inventory` | profile (`scan.exclude`) |
| `go test -fuzz` command built in the motor | `verify.evalFuzz` | the `go-fuzz` adapter |

## Five states

A gate ends in exactly one:

| State | Meaning | Green? |
| :--- | :--- | :--- |
| `passed` | ran, inventory satisfied | yes |
| `failed` | ran, exit code or inventory violated | no |
| `not_run` | the environment could not run it | **no** |
| `not_applicable` | a declared reason says it does not apply | yes |
| `not_implemented` | the registry says `planned`, and bootstrap does not cover it | no |

Two design choices matter:

- `not_run` is **not** green. An unpinned image makes every executable gate
  `not_run`, so a machine without the environment cannot certify a tier.
- `planned` inside `allowed_planned` while `bootstrap.enabled` is
  `not_applicable` with a reason. Outside that window it is `not_implemented`.
  The bootstrap has a `deadline_date`; when it passes, the gate fails by itself.

## A run, end to end

```
touched files ──► tier = max(glob matches, default_tier=2)   ← never chosen by the agent
                 │
                 ├─► for each gate of the tier:
                 │      planned?        → not_applicable(bootstrap) | not_implemented
                 │      kind: self?     → run in-process
                 │      otherwise       → docker run <image>@<digest> bash -c <run>
                 │                          --network none --user 1000
                 │                          inventory mounted read-only
                 │
                 └─► parse output by kind ─► violations
                                               │
report.json ◄──────────────────────────────────┘
{ registry_sha256, tier, touched, backend, env, gates_results[{status, exit, tail, violations}] }
```

The host computes `touched_files`, `diff_sha256` and `worktree_tree_sha`; the
container only executes. That split is deliberate: the tree is a fact about the
host, the toolchain is a fact about the image.

## Contracts

Three artifacts cross agent boundaries and are validated, not read:

| Contract | Validated by | Rules that bite |
| :--- | :--- | :--- |
| dispatch **spec** | `harness-validate -kind spec` | tier ≥ tier derived from `may_edit`; no `may_edit` overlapping governance; evidence check must exist |
| worker **report** | `harness-validate -kind report` | ≤ 15 lines; `done` requires a green report; `blocked` requires a blocker |
| review **verdict** | `harness-validate -kind verdict` | `reject` needs a blocking reason; `approve` may not carry one; `context_requests` bounded and artifact-shaped |

A format without a validator is prose with line breaks.
