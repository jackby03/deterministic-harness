# Publishing this repository

Everything is committed locally under `repo/`. Creating the remote is a manual
step because it needs your credentials.

## Before you push: four decisions

1. **License.** There is no `LICENSE` file on purpose: choosing one is a legal
   decision, not a technical one. Add it before the repo goes public — without a
   license, nobody may legally reuse it, which defeats the purpose.
2. **Module path.** It currently says `github.com/jackby03/deterministic-harness`.
   If the final location differs, change it once:

   ```bash
   grep -rl "github.com/jackby03/deterministic-harness" --include=*.go . \
     | xargs sed -i 's|github.com/jackby03/deterministic-harness|<new path>|g'
   sed -i 's|^module .*|module <new path>|' go.mod
   go test ./...
   ```

   `purity/purity_test.go` has the same string in `modulePath`; it neutralises the
   module prefix before scanning, so it must be updated too.

3. **The example profile.** `examples/waffynx/` is a real, complete profile. Keep
   it as the reference specimen, or replace it with a sanitized one. The purity
   test expects *a* specimen at that path (or a `gates.yaml` next to the module).

4. **Owner names in examples and `CODEOWNERS`.** `@jackby03` appears in the
   example `CODEOWNERS` and in the module path.

## Create the remote and push

```bash
cd repo
git init -b main
git add -A
git commit -m "deterministic-harness: initial import"

# GitHub CLI, or create the repo in the web UI
gh repo create jackby03/deterministic-harness --public --source=. --remote=origin
git push -u origin main
git tag -a v0.1.0 -m "v0.1.0: motor, adapters, contratos, ejemplos"
git push origin v0.1.0
```

Without `gh`:

```bash
git remote add origin git@github.com:<owner>/<repo>.git
git push -u origin main
```

## What the first CI run will tell you

The workflow in `.github/workflows/ci.yml` does what the docs claim:

1. `go test ./...` — 38 tests.
2. `harness-verify -registry gates.yaml -tier 2 -backend host` — the repository
   verifies itself with its own profile.
3. Uploads `.artifacts/verify-report.json`.

If step 2 fails on a fresh clone, the harness is not trustworthy for anyone else
either. That is why the job exists.

## What is deliberately missing

| Missing | Why | Where it would go |
| :--- | :--- | :--- |
| `LICENSE` | a legal decision | root |
| Node adapters (`node-test-selectors`, `node-budget`) | specified in `docs/adapting.md`, not written | `internal/inventory`, `verify.inventoryFor` |
| A reference implementation of the agent hooks | written as a draft, never executed (`extensions/`) | the runtime's repo |
| Release automation | one tag is enough until there are consumers | `.github/workflows/release.yml` |

## Suggested first issues

- **`kind`-declared execution.** `verify.executesModule` guesses whether a gate
  runs module code by matching `"go test"`, `"make "`. Making it a `kind` field
  removes the last language assumption from the coverage rule.
- **N-tier support.** `all_tier3_globs_covered` hardcodes three tiers.
- **`scan.exclude` in the profile.** The skip-directory list is currently
  ecosystem convention baked into the walkers.
- **Node adapters.** The worked example in `docs/adapting.md` is the spec.
