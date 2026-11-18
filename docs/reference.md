# Reference

Everything the harness reads and writes. If a key is not here, the harness does
not read it — and a key nobody reads is a lie, so the registry should not contain
it either.

## `gates.yaml` — top level

| Key | Type | Required | Meaning |
| :--- | :--- | :--- | :--- |
| `schema_version` | int | yes | profile schema version; the loader rejects 0 |
| `default_tier` | int | yes | tier for paths no glob matches. Must be non-zero: fail-closed means up |
| `harness` | map | no | `version`, `digest` of the motor, for the record |
| `bootstrap` | map | yes | see below |
| `runtime` | map | no | where gates execute; absent means host |
| `features` | map | no | `traceability: on\|off`, `bootstrap: on\|off` |
| `image_toolchains` | map | no | documentation of what the image guarantees; the digest is the truth |
| `traceability` | map | no | which documents are citable, and which one declares `enforced-by` |
| `governance` | map | no | `paths`: files no worker may edit |
| `review` | map | no | reviewer policy |
| `override_policy` | map | no | when a human approval is required |
| `gates` | map | yes | gate definitions, keyed by id |
| `tiers` | map int → [gate id] | yes | what each tier requires |
| `touch_sets` | map | yes | `tier_1`, `tier_3`: globs that move a change up |

### `bootstrap`

| Key | Meaning |
| :--- | :--- |
| `enabled` | while true, `planned` gates in `allowed_planned` are `not_applicable` |
| `deadline_date` | `YYYY-MM-DD`. After it, `bootstrap-expired` fails while `enabled` stays true |
| `allowed_planned` | exactly the `planned` gates the highest tier requires |

The bootstrap is a one-time exemption with an expiry, not a permanent escape
hatch. `registry-coverage` refuses `enabled: true` with no deadline.

### `runtime`

| Key | Default | Meaning |
| :--- | :--- | :--- |
| `kind` | `host` | `container` runs every gate in the image |
| `image`, `digest` | — | referenced as `image@sha256:…`; a tag is never accepted |
| `platform` | — | passed to `docker run --platform` |
| `user` | — | `uid:gid`. Root makes permission tests decorative |
| `network` | — | `none` is the intended value: no gate needs the network |
| `workdir` | `/src` | mount point of the repository |
| `volumes` | — | `repo` is a bind mount from the host; every other key is `name:path` |
| `git.core_filemode` | — | set `false` on filesystems that do not preserve modes |

If `digest` is not a `sha256:` value, every executable gate reports `not_run`
with that reason. A machine without the image cannot certify a tier.

### `review`

| Key | Meaning |
| :--- | :--- |
| `agent`, `model` | who reviews. `agent` is the runtime's agent id, `model` the model id |
| `limit.per_review_cap_tokens` | circuit breaker for one runaway review |
| `limit.monthly_ceiling_reviews` | circuit breaker for a streak |
| `max_context_requests` | how many `ask` round-trips a reviewer may make |
| `triggers` | when a review is required. Reuse `override_policy.triggers` ids |
| `scope` | `complement_of_gates`: do not re-check what the gates proved |
| `on_limit_exhausted` | `escalate_human`. Never "skip" |
| `verdict` | allowed verdicts |

Limits are breakers, not meters. The cost is controlled by scope, triggers and
the verdict being an artifact — not by watching a quota.

### `override_policy`

| Key | Meaning |
| :--- | :--- |
| `require_human_codeowner` | merging a weakening needs an approver who is not the author |
| `require_trailer` | commit trailer naming the reason |
| `triggers` | `tier_gate_shrink`, `linter_set_shrink`, `enforced_by_shrink`, `recomputation_mismatch`, `image_digest_mismatch`, `nolint_budget_increase` |
| `auto_allow` | `gate_added`, `inventory_added`, `toolchain_bump`: strengthening is free |

## Gates

| Key | Required | Meaning |
| :--- | :--- | :--- |
| `kind` | yes | one of the eight below |
| `status` | no | `active` (default) or `planned` |
| `run` | all but `self`, `go-fuzz` | shell command, executed with `bash -c` |
| `check` | `self` only | which in-process check |
| `covers` | yes | globs of paths this gate is responsible for |
| `min_inventory` | no | the facts that must be observed |
| `invocations` | `go-fuzz` only | one `{package, target, fuzztime, env}` per target |

### Kinds and their inventory

| kind | Inventory keys | Notes |
| :--- | :--- | :--- |
| `command` | `non_empty_files` | exit code only. No inventory unless it reads fixtures |
| `go-test` | `non_empty_files` | exit code; `go test` already fails on failure |
| `go-test-selectors` | `required_selectors`, `required_tests`, `forbid_skipped_selectors` | a skipped test is not evidence |
| `go-bench` | `benchmarks_max_allocs`, `fail_on_unlisted_benchmark` | keys are `<import path>:<Benchmark>`; `allocs/op` is exact, not timing |
| `go-fuzz` | `min_seeds_per_target` | seeds = `f.Add` (AST) + files in `testdata/fuzz/<Target>` |
| `nolint` | `allowed_linters`, `require_reason`, `fail_on_unlisted_directory`, `budget` | budget is per directory; a new directory is a failure |
| `json-report` | `min_passing_cases`, `max_failed_cases`, `non_empty_files` | reads the JSON object in the output |
| `self` | depends on `check` | see below |

### `self` checks

| check | Inventory | What it verifies |
| :--- | :--- | :--- |
| `registry-coverage` | `rules[]` | the registry constrains itself. Rules: `all_tier3_globs_covered`, `covers_intersect_touch_sets`, `no_orphan_gates`, `planned_gates_within_bootstrap_allowlist`, `hermetic_runs`, `bootstrap_has_deadline`, `covers_literals_exist` |
| `registry-fresh` | — | generated blocks match the tree (`registry-gen -check`) |
| `bootstrap-expired` | — | `deadline_date` has not passed while enabled |
| `citations` | `min_resolvable_citations` | every `§X.Y` resolves to a numbered heading |
| `traceability` | `sections_required`, `require_active_gate_for_enforced`, `require_selector_exists` | every rule names a gate that runs, or is explicitly ungated |

`all_tier3_globs_covered` is judged against **real files**, not by intersecting
glob strings: a glob that matches nothing in the tree is reported, which is how
ghost references are found.

## Generated blocks

A registry may contain regions `registry-gen` owns:

```yaml
      observed_seeds:
        # BEGIN GENERATED:seeds
        "FuzzInspectGraphQL": 4
        # END GENERATED:seeds
```

Four blocks exist: `seeds`, `citations`, `nolint`, `benchmarks`. Rules:

- Blocks hold **observed facts only**. Policy (`min_*`, `budget`, `tiers`) is
  never emitted, so regeneration cannot lower a ratchet or widen a budget.
- A block that was not measured this run is **left untouched**: absence of a
  measurement is not a measurement of zero (`benchmarks` is the only such block,
  since it requires `-measure`).
- Everything outside the markers is preserved byte for byte, comments included.
- A missing `# END GENERATED:` is an error, not a silent no-op.

## The report

```json
{
  "registry_sha256": "…",
  "tier_computed": 3,
  "tier_source": "computed | flag | escalated",
  "matched_globs": ["internal/engine/**"],
  "touched_files": ["internal/engine/eval.go"],
  "backend": "container",
  "backend_override": false,
  "env": { "os": "linux", "arch": "amd64", "go_version": "go1.22.12",
           "image": "ghcr.io/org/verify@sha256:…", "image_digest": "sha256:…" },
  "gates_results": [
    { "gate": "allocs-baseline", "kind": "go-bench", "status": "passed",
      "run": "…", "exit_code": 0, "duration_ms": 920,
      "output_sha256": "…" },
    { "gate": "nginx-module-build", "kind": "command", "status": "not_applicable",
      "reason": "bootstrap: gate is planned and listed in allowed_planned" },
    { "gate": "race", "kind": "go-test", "status": "not_run",
      "reason": "runtime.digest is not pinned (got \"sha256:<PINNED_AT_D0>\")",
      "tail": ["…last 12 lines of output…"] }
  ],
  "green": false
}
```

`tail` is present only on failure: a digest cannot be debugged, and the agent
closing the turn needs the error.

## Contracts

```
harness-validate -kind spec     -registry gates.yaml spec.yaml
harness-validate -kind report   report.txt
harness-validate -kind verdict  verdict.json
```

**Spec** (the dispatch unit):

```yaml
task: "n1"
base: "<sha>"                 # required: a node that starts from main rewrites verified work
target: "<one sentence>"
may_edit: ["internal/engine/**"]
tier: 3                       # must be >= the tier derived from may_edit
gates: [build-go, test-all, race]
acceptance: "<verifiable criterion>"
evidence:
  check: "tree_sha_matches_report"
turn_cap: 6
```

**Report** (≤ 15 lines, fixed keys):

```
outcome: done | blocked | needs-spec
commit: <sha>
tree_sha: <sha>
report: .artifacts/verify-report.json (green|red)
gates: build-go=passed, race=passed
blockers:
  - <one line per blocker>
asks:
```

**Verdict** (JSON): `verdict`, `against{commit,tree_sha}`, `checked[]`,
`reasons[{severity,file,line,why}]`, `context_requests[{question,artifact}]`.
A `reject` without a `blocking` reason is invalid; an `approve` carrying one is
a contradiction; a context request must point at `path#Lx-Ly`, not at prose.

## CLI

| Flag | Command | Meaning |
| :--- | :--- | :--- |
| `-root` | verify, facts | repository root |
| `-registry` | verify, validate | profile path |
| `-tier` | verify | `0` computes from the diff; a flag may be escalated, never lowered |
| `-base` | verify | git base for `touched_files` |
| `-backend` | verify | `auto` (from the profile), `container`, `host` |
| `-only` | verify | comma-separated gate ids, for debugging one gate |
| `-out` | verify, facts | write the artifact here |
| `-measure` | facts | also run benchmarks (needs the toolchain) |
| `-write` / `-check` | registry-gen | rewrite in place / exit 1 on drift |
