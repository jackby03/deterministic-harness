# Multi-agent protocol

The harness answers *when a change is done*. This document answers *who does what*
and *how work hands off*.

It assumes an orchestrator that can start isolated worker sessions, wake a
coordinator when a worker settles, and let a worker ask a question. Orca is one
such orchestrator; the primitives below are the ones that matter, and they are
general.

## Roles

| Role | Runs in | May edit | Closes with |
| :--- | :--- | :--- | :--- |
| **Coordinator** | the main session | nothing | a quick turn end, not a verification |
| **Implementer** | a worker, own worktree | only `spec.may_edit` | `verify-report.json` green |
| **Reviewer** | a worker, fresh worktree | nothing that merges | a verdict artifact |

Slots are a concurrency budget, not identities. With four slots:
`1 coordinator + 2 implementers + 1 reviewer`.

### Coordinator obligations

1. **Compiles** the spec from the profile; it does not write it.
2. **Partitions by touch set** before dispatching. Two nodes whose touch sets
   overlap never run concurrently.
3. **Serialises the top tier**: one worker at a time on the expensive paths.
4. **Persists the DAG** before dispatching. The plan does not live in its context.
5. **Ends its turn** after starting workers. Coordinates by being woken.
6. **Answers questions** when the answer follows from the spec; otherwise escalates.
7. **Never marks a failed report as done.**
8. **Checks the breaker** before dispatching.

It does not verify. A coordinator that runs the settle gate delays every dispatch,
because the orchestrator wakes it and the turn must close.

### Implementer obligations

Work inside `may_edit`; escalate the tier when touching an expensive path, never
lower it; do not close without a green report for its own tree; report in the
fixed format.

### Reviewer obligations

Receive `spec + diff + report + the list of gates that passed`, never the
implementer's transcript. It may make a bounded number of `ask` requests, and each
answer must be an artifact (`path#Lx-Ly`), logged in the verdict. Its scope is the
complement of the gates: intent against spec, changes the gates cannot see, and
whether a gate was satisfied by a trick.

## The spec: compiled, not written

```yaml
task: "n1"
base: "<sha>"                  # a node that starts from main rewrites verified work
target: "<one sentence>"
may_edit: ["src/server/**"]
tier: 3                        # derived from may_edit; a lower value is rejected
gates: [build, test, race]
acceptance: "<verifiable criterion>"
evidence:
  check: "tree_sha_matches_report"
turn_cap: 6
```

No `must_not_edit`: governance paths live in the profile and the tool guard
enforces them. A second copy in the spec goes stale without anyone noticing.

## The DAG

```
$(git rev-parse --git-common-dir)/orca/tasks/<task_id>.json
{
  "nodes": [
    {"id": "n1", "role": "implement", "touch": ["src/server/**"],
     "state": "settled", "commit": "<sha>", "report": "green"},
    {"id": "n2", "role": "implement", "touch": ["src/payments/**"],
     "depends_on": ["n1"], "state": "pending"},
    {"id": "n3", "role": "review", "depends_on": ["n2"],
     "state": "pending", "trigger": "tier_3_paths"}
  ]
}
```

States: `pending | running | settled | blocked | needs-human-spec`.

- An edge carries an **artifact**: `commit + tree_sha + report + spec delta`.
- A node becomes `settled` **only** if its report is green; a worker's "done" with
  a red report is `blocked`.
- A `needs-human-spec` node blocks its dependents; it is not re-dispatched.
- The DAG is written before dispatching, atomically.

Two stores, on purpose: the DAG where the coordinator runs, the breaker where the
worker runs. Unifying them "for consistency" breaks one of the two.

## Review policy

Optional, expensive, bounded:

```yaml
review:
  agent: "pi"
  model: "<reviewer model>"
  limit: { per_review_cap_tokens: 120000, monthly_ceiling_reviews: 20 }
  max_context_requests: 3
  triggers: ["tier_3_paths", "governance_paths", "enforced_by_shrink", "linter_set_shrink"]
  scope: "complement_of_gates"
  on_limit_exhausted: "escalate_human"
```

Three structural rules do the real cost control, not the limits:

1. `scope: complement_of_gates` — a reviewer never re-checks what a gate proved.
2. Narrow triggers — most changes get no review.
3. The verdict is an artifact — nobody reads it by hand.

A **negative control** is mandatory when the reviewer runs the same model as the
implementers: a fixture diff that evades the gates (deletes a benchmark, weakens
an assertion, adds a suppression with a false reason) must be rejected. Same model
means correlated blind spots; the procedural framing is the only advantage, so it
has to be tested.

Measure the reviewer by **rejections per N reviews**. A reviewer that never
rejects is either unnecessary or decorative.
