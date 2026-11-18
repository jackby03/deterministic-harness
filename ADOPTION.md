# Adoption checklist

Landing the harness in a repository, in the order that keeps the first day green.
Each step says what it prevents, because every step here exists because something
went wrong without it.

## 1. Profile first, gates second

Write `gates.yaml` with **only** `command` gates: build, lint, test by exit code.
Two tiers are enough. Run tier 1 until it is green.

*Prevents:* a registry that describes an intention instead of a fact.

## 2. Pin the environment

Build the verification image, push it, and put `image@sha256:…` in the profile.
Every executable gate is `not_run` until the digest is real — that is the point.

*Prevents:* baselines that are only valid on the author's machine. Measured on a
real project: `go1.26` gave 29 allocations per op where the pinned `go1.22.12`
gave 35. Same code, same architecture.

*Gotchas found the hard way:*

- `useradd`/`adduser` are absent from `debian:bookworm-slim`; either install
  `passwd` or create the user by writing `/etc/passwd`.
- `bash -lc` resets `PATH` from `/etc/profile`: a toolchain in `/usr/local`
  disappears. Use `bash -c` for gates.
- `--network none` requires a warm package cache, otherwise every build gate
  fails on a network error and it looks like a harness bug.

## 3. Make the tree the unit of evidence

The report must carry `tree_sha` (the working tree that was verified) and
`diff_sha256`, not just a commit. In CI, recompute `touched_files` from git and
discard the report if it disagrees.

*Prevents:* "verified" attaching to code that was never verified.

## 4. Give every gate a minimum inventory

A test run with an empty suite is green. A benchmark that was deleted is not
slow. A corpus with no seeds proves nothing. For each gate, declare what must be
*observed*:

- `required_selectors` — the tests that must run, and may not be skipped;
- `benchmarks_max_allocs` + `fail_on_unlisted_benchmark` — a deleted benchmark
  becomes a failure instead of a silence;
- `min_seeds_per_target` — counted from the AST plus the corpus, so in-code
  seeds are not duplicated;
- `min_passing_cases` — a floor on the suite;
- `non_empty_files` — the fixtures it reads.

*Prevents:* gates that pass for the wrong reason. In a real audit this found a
test that chmod-ed a socket and then asserted the permission: it would have
passed with the production `chmod` deleted.

## 5. Budgets are measured, then declared

Run the benchmark once in the image, write the number, and add
`fail_on_unlisted_benchmark`. Never write a budget by hand from memory.

*Prevents:* the class of bug where a ceiling of 29 is declared for a benchmark
that measures 35 in CI.

## 6. Generated facts stay generated

`harness-facts` + `registry-gen` own the observed blocks of the profile; a gate
(`registry-fresh`) fails when they drift. Policy keys are never generated, so the
generator cannot lower a ratchet.

*Prevents:* hand-maintained numbers, which is the same "stale prose" failure one
level down.

## 7. Governance, or explicit absence

`CODEOWNERS` on the gate files, branch protection requiring code-owner review and
signed commits, and `override_policy` naming what counts as a weakening.

If the project has no normative documents, set `features: { traceability: off }`
and delete `section-refs` / `enforced-by`. An absent rule is honest; a decorative
one is not.

*Prevents:* a signature that always appears. If every change needs a human, the
human stops reading in week three.

## 8. Let the repository verify itself

Add a CI job that runs the repository's own profile. Not as a formality: it is the
only evidence that the gates work on the project they were written for.

Checklist to leave the first day green:

| Item | Command |
| :--- | :--- |
| Tests | `go test ./...` (or your runner) |
| Lint | the project's linter, no baseline |
| Self-verification | `harness-verify -registry gates.yaml -tier 2` |
| Report artifact | upload `.artifacts/verify-report.json` |

## What is explicitly not done on day one

- **Budgets for tiers you cannot measure yet.** Leave them out; a wrong ceiling
  is worse than no ceiling.
- **A reviewer.** It is optional, expensive and only worth it once the gates are
  trustworthy. Add it with a negative control (see `docs/protocol.md`).
- **Reviewing every change.** The point of the system is that a green tier is
  sufficient for a merge in the cheap tiers.
