# Adapting the harness to another project

The motor is reusable as-is. What changes per project is the profile, the image,
and — for a new language — one or two adapters.

## The three questions

Before writing anything, classify each item:

| If it breaks when… | It belongs to |
| :--- | :--- |
| you change **project** | the profile (`gates.yaml`) |
| you change **language/toolchain** | an adapter (`kind`) |
| you change the **gate model** | the motor |
| you change the **risk judgement** | governance (the repo's own law) |

## Step 1 — write the profile

Start from [`examples/minimal/gates.yaml`](../examples/minimal/gates.yaml). The
only mandatory parts are `schema_version`, `default_tier`, `gates`, `tiers`,
`touch_sets`. Everything else is opt-in.

Three decisions carry most of the value:

1. **What is tier 3?** The paths where a mistake is expensive and hard to undo:
   authentication, payments, data migrations, concurrency, hot paths.
2. **What is the default tier?** Keep it at 2. An unmapped path is a path nobody
   thought about, and the safe direction is up.
3. **What is the minimum inventory?** For each gate: what must be *observed* for
   the gate to mean something. A green test run proves nothing if the suite is
   empty.

## Step 2 — run what already works

Most projects need no new adapter. These kinds work for any toolchain:

```yaml
gates:
  install:
    kind: command
    run: "pnpm install --frozen-lockfile --offline"
    covers: ["package.json", "pnpm-lock.yaml"]
  typecheck:
    kind: command
    run: "pnpm tsc --noEmit"
    covers: ["src/**", "tsconfig.json"]
  lint:
    kind: command
    run: "pnpm eslint . --max-warnings 0"
    covers: ["src/**"]
  unit:
    kind: json-report
    run: "pnpm vitest run --reporter=json"
    covers: ["src/**", "tests/**"]
    min_inventory:
      min_passing_cases: 120
      non_empty_files: ["pnpm-lock.yaml"]
```

That is a complete tier 2 for a Node project: install, typecheck, lint, tests,
with a floor on how many cases must pass. No Go required — see step 5.

## Step 3 — write an adapter when the inventory demands it

A `command` gate can only say pass/fail. The moment you want "these named tests
ran and none were skipped", or "the bundle did not grow", you need a `kind`.

An adapter is a parser plus a comparator, both pure functions over text:

```go
// ParseVitestJSON maps `vitest --reporter=json` to test name -> status.
func ParseVitestJSON(out string) map[string]string

// CheckSelectors is already generic: required names, skip forbidden.
```

Then wire it in two places: `inventoryFor` (the switch on `kind`) and the
registry (the kind name). That is the whole extension surface. The Node adapters
worth writing, in order of value:

| Adapter | Why it is deterministic | Inventory |
| :--- | :--- | :--- |
| `node-test-selectors` | jest/vitest JSON reports per-assertion status | `required_selectors`, `forbid_skipped_selectors` |
| `node-budget` | bundle size is byte-exact | `max_bytes`, `bundle` |
| heap growth | noisy; CI-dependent | use as evidence, not a gate |

Node has no `allocs/op` and no runtime fuzzing. Pretending otherwise is how a
gate becomes decorative: prefer the deterministic metric (bundle size) and treat
heap and cold-start as reported evidence.

## Step 4 — build the verification image

```dockerfile
FROM golang:1.22-bookworm AS harness
COPY harness/ /h
RUN cd /h && CGO_ENABLED=0 go build -o /out/harness-verify ./cmd/harness-verify

FROM node:22-bookworm
COPY --from=harness /out/harness-verify /usr/local/bin/
RUN corepack enable && useradd -m -u 1000 gate
USER gate
WORKDIR /src
```

Why this matters: the motor is written in Go, but the consuming project never
installs Go. The image carries a static binary, and the digest pins it. Build it,
push it, and put `image:`, `digest:` in the profile.

Two operational details, both learned the hard way:

- **`--network none` needs a warm package cache.** Run the install step once with
  network into a named volume (`-v qatu-modcache:/home/gate/.local/share/pnpm`),
  then the offline `install` gate works forever.
- **Do not use a login shell** for gate commands (`bash -lc`): it resets `PATH`
  from `/etc/profile` and the toolchain disappears. The executor uses
  `bash -c`; if you run gates by hand, do the same.

## Step 5 — governance, or switch it off

If the project has no normative documents, set:

```yaml
features: { traceability: off }
```

and drop `traceability:` and the `section-refs`/`enforced-by` gates. The motor
does not care. If it does have them, declare the mapping explicitly:

```yaml
traceability:
  enforced_by_doc: "constitution.md"
  docs:
    "Constitution": "constitution.md"
    "AGENTS.md": "AGENTS.md"
```

The names on the left are what appears in citations (`Constitution §1.2`); the
values are paths. Notice that this is the *only* place the words "constitution"
or "AGENTS.md" exist in the system — the motor has no idea what a constitution
is.

## Worked example: a Node.js project

[`examples/node-qatu/gates.yaml`](../examples/node-qatu/gates.yaml) is a complete
profile for a Node service with a hot path (`src/server/**`, `src/payments/**`),
plus [`Dockerfile.verify`](../examples/node-qatu/Dockerfile.verify).

What it demonstrates:

- tiers by path, with `package.json` and the lockfile in tier 3 (a dependency
  bump is a risk decision, not a chore);
- `json-report` for the test suite, with a floor;
- a `node-budget` gate for bundle size;
- governance on `gates.yaml`, the lockfile and the CI workflow;
- no constitution, no Go, no helm.

Estimated effort for a project of that size: half a day for the profile and the
image, one day if you also write the two Node adapters.

## Adoption order

Do not port everything at once. The order that keeps the first day green:

1. `command` gates only (build, lint, test by exit code) → tier 1 and 2.
2. The image and its digest → the environment stops being a variable.
3. `json-report` with a passing-cases floor.
4. One `go-test-selectors`-style adapter for the tests that actually matter.
5. Budgets (allocs, bundle) — these need measurement first, not estimation.
6. Governance: `CODEOWNERS`, `traceability`, `override_policy`.

The full checklist, with the failure modes each step avoids, is in
[../ADOPTION.md](../ADOPTION.md).
