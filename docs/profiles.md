# Session profiles

The harness hooks are useful only if a session knows which role it is playing.
The role comes from the environment, not from the prompt: a prompt can claim
anything, and a dispatcher cannot.

```
PI_ROLE=dispatch | implement | review | solo
```

Absent means `solo`. That is the only profile where a human is the guardrail, so
it keeps no path allowlist — and keeps the settle gate.

## What changes per role

| Profile | `write`/`edit` | settle gate | extra |
| :--- | :--- | :--- | :--- |
| `dispatch` | blocked | **off** | dispatch tools only |
| `implement` | only `spec.may_edit` | mandatory | governance blocked |
| `review` | blocked (its worktree never merges) | off | `ask` + verdict |
| `solo` | anything | mandatory | — |

The model is not part of a role. The session runs whatever model the operator
chose; pinning one per role would encode a cost decision in the protocol.

## `dispatch`

| Rule | Mechanism |
| :--- | :--- |
| No edits | `tool_call` returns `block` with a reason |
| Settle gate off | verifying here delays every dispatch |
| Spec is validated, not written | `harness-validate -kind spec` before dispatch |
| Breaker checked | read before `worker-start` |
| DAG persisted | dispatch fails if no `running` node was written |

Why no edits: the coordinator's context is the most expensive and the most
polluted (it receives a wake per worker). A coordinator that edits stops being
observable.

## `implement`

| Rule | Mechanism |
| :--- | :--- |
| Allowlist | globs from `spec.may_edit`; violation blocks with the allowed paths |
| Governance blocked | derived from the profile, not from the spec |
| Tier escalates, never lowers | touching a tier-3 path raises the effective tier |
| Close verified | `agent_before_settle` runs `harness-verify`, cached by `tree_sha` |
| Handoff | the report goes to the DAG node, not to the coordinator's chat |

## `review`

Receives a bundle; may not fix. Its worktree is never merged, so "read-only" is
about the *output*, not the filesystem.

Verdict schema:

```json
{
  "verdict": "approve | reject | needs-spec",
  "against": { "commit": "<sha>", "tree_sha": "<sha>" },
  "checked": ["intent_vs_spec", "gate_evasion", "evidence_matches_commit"],
  "reasons": [
    { "severity": "blocking", "file": "src/x.ts", "line": 42,
      "why": "<one sentence naming the rule or the gap>" }
  ],
  "context_requests": [
    { "question": "<what was missing>", "artifact": "src/x.ts#L1-L20" }
  ]
}
```

A `reject` with no `blocking` reason is not actionable and is rejected by
`harness-validate`.

## Where each obligation is enforced

| Obligation | Mechanism | Status |
| :--- | :--- | :--- |
| No edits outside the allowlist | `tool_call` hook | to implement |
| No edits to governance | hook + `CODEOWNERS` | to implement |
| Close with verification | `agent_before_settle` | designed |
| Coordinator does not verify | `dispatch` profile | to implement |
| Evidence names the tree | `verify-report.tree_sha` | built |
| Tier computed from the diff | motor | built |
| DAG persisted, atomic | coordinator | to implement |
| `settled` needs a green report | coordinator validation | to implement |
| Report ≤ 15 lines | schema validated by the coordinator | to implement |
| Review bounded | profile + `escalate_human` | designed |
| Breaker per node | file in the git dir | designed |

The table is the point: an obligation without a mechanism is a paragraph.

## Reference implementation

[`extensions/waffynx-harness.ts`](../extensions/waffynx-harness.ts) is a draft for
one agent runtime. It has never been executed: it lists, in its header, the three
API details that must be confirmed before trusting it (export shape,
`{continue: true}` on the settle boundary, and that the input event fires for
dispatched prompts).

Its shape is the interesting part: `PI_ROLE` selects a policy, the policy is a
list of allow/deny decisions, and the settle gate shells out to
`harness-verify`. Nothing about a specific project lives there.
