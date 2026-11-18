# Operations

## The verification image

The image exists for one reason: a baseline measured in an unpinned environment is
an opinion. Concretely, on a real project, the same benchmarks measured with
`go1.26` gave 29 and 8 allocations per op; with the pinned `go1.22.12` they gave
35 and 12. Same architecture, different toolchain, different numbers.

Rules:

- The image is **not** the production image. It carries compilers and linters;
  production should not.
- Reference it as `image@sha256:…`. A tag is mutable, so a tag is not an identity.
- Every gate that executes code declares no `requires:` — the image is the
  requirement, and its digest is in the report.

Build and pin:

```bash
docker build -f deploy/Dockerfile.verify -t registry.example/verify:v1 .
docker push registry.example/verify:v1
docker buildx imagetools inspect registry.example/verify:v1   # -> sha256:…
```

Then in the profile:

```yaml
runtime:
  image: "registry.example/verify"
  digest: "sha256:…"
```

With a non-`sha256` digest, every executable gate reports `not_run` and the tier
cannot close. That is the intended behaviour: no false green.

The image should self-test at build time, so a missing tool is a build failure
and not a gate that silently never runs. See
[`deploy/verify-image-selftest`](../deploy/verify-image-selftest).

## Offline execution

Gates run with `--network none`. That is deliberate: a gate that downloads
dependencies is not reproducible, and a supply-chain compromise should not be one
`npm install` away from a green build.

The cost is that caches must be warmed once, outside the gate:

```bash
# Go
docker run --rm -v harness-gomod:/home/gate/go/pkg/mod \
  -v "$PWD":/src -w /src --user 1000:1000 <image>@<digest> \
  bash -c 'go mod download'

# Node
docker run --rm -v qatu-store:/home/gate/.local/share/pnpm \
  -v "$PWD":/src -w /src --user 1000:1000 <image>@<digest> \
  bash -c 'pnpm fetch'
```

Then the offline `install` gate (`go build`, `pnpm install --offline`) works with
the network off.

Two traps worth knowing:

- **Login shells reset `PATH`.** `bash -lc` sources `/etc/profile`, and a
  toolchain installed under `/usr/local` disappears. The executor uses
  `bash -c`; images should still add `/etc/profile.d/toolchain.sh` for humans.
- **Root hides permission bugs.** Run gates as a non-root user. A `0600`
  assertion executed as root proves nothing about the daemon that will run
  unprivileged in production.

## Gates must not mutate the tree

A run compares `git status --porcelain --untracked-files=no` before and after. If
a gate modifies a tracked file, the report gains a failing `tree-mutation` entry:
the evidence would otherwise describe a tree that no longer exists.

This is not theoretical. During the first real run, `go mod download` inside the
container wrote to the bind-mounted tree and rewrote `go.mod`, moving two
dependencies from indirect to direct. The gate passed; the tree had changed. Any
tool that is allowed to "fix" metadata (`go mod tidy`, `pnpm install` without
`--frozen-lockfile`, `cargo update`) is a candidate.

Untracked output is allowed on purpose: fuzzing writes new corpus entries, and
those are the point.

## Generated blocks

The registry owns some facts that are derived, not decided: how many seeds each
fuzz target has, how many citations resolve, which suppressions exist, what the
benchmarks measure. Those live in marked blocks and are rewritten, never edited:

```bash
harness-facts -root . -registry gates.yaml -measure -out facts.json
registry-gen  -registry gates.yaml -facts facts.json -write
registry-gen  -registry gates.yaml -facts facts.json -check   # CI: exit 1 on drift
```

`registry-fresh` runs the same comparison as a gate, so a tree that changed
without regenerating fails at close, not at review.

Rules that keep this safe:

- Only observed facts are generated. `min_*`, `budget`, `tiers` and `touch_sets`
  are never emitted by the generator, so it cannot lower a ratchet.
- Unmeasured blocks are left untouched: a fact that was not observed is not zero.
- Everything outside the markers is preserved byte for byte.

## The report and CI

The local report is advice for the agent. The merge decision belongs to CI, which
runs on the same image digest and recomputes the facts that matter:

1. CI recomputes `touched_files` from `git diff --name-only <base>...HEAD`. If it
   disagrees with the report, the report is discarded — the diff is the authority.
2. CI runs every gate of the tier that the *recomputed* diff requires, plus any
   gate the local run could not run (`not_run`).
3. CI requires the tier to end green: no `failed`, no `not_run`, no
   `not_implemented`.

A minimal GitHub Actions job:

```yaml
- run: go run ./cmd/harness-verify -registry gates.yaml -tier 0 \
       -backend container -out .artifacts/verify-report.json
- uses: actions/upload-artifact@v4
  with: { name: verify-report, path: .artifacts/verify-report.json }
```

## Suppression budget

`nolint-budget` counts suppression directives per directory and fails when the
count exceeds the declared budget, when a directive names no linter, or when it
carries no reason. A suppression is the cheapest way to weaken the system
without touching a governance file: it lives in the diff of the change it
excuses, and no hash covers it.

```bash
GATE_INVENTORY=/tmp/inv nolint-budget -root . -budget /tmp/inv/nolint-budget.json
```

The budget is generated (`-write`) and tightened by hand; it only ratchets down.

## Circuit breaker

The breaker lives outside any worktree, because workers are ephemeral:

```
$(git rev-parse --git-common-dir)/orca/breaker/<task_id>.json
{ "attempts": 2, "state": "retrying", "updated_at": "…", "tree_sha": "…" }
```

It is checked **before** dispatching a worker, so a blocked task costs zero
tokens. `needs-human-spec` is terminal until a human writes a better spec.

The DAG of a task lives next to it and is written before dispatching, atomically:
it is the recovery artifact when the coordinator dies.
