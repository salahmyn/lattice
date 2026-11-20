# 0010 — Assurance decisions after v0.15

- **Status:** accepted (November 2026). The items are implemented across
  v0.15.x and v0.16.
- **Question:** v0.15 made Lattice judge whether a change was done *right*, not
  just done: break testing, the audit, `check --base`, and confirmation by a
  platform approval. An early adopter then ran it on a large codebase. What should
  change, and how should the review UI and the acceptance cases work?

## Why

That run found real fakes: tests that pass when the code they verify
is broken. It also found ways the tool could mislead a reviewer:

- results from another commit counted in a local check;
- a change no promise covers read `ASSURED`;
- a hand-written cache counted as a human confirmation;
- re-confirming a promise overwrote the earlier decision.

v0.15.1 fixed those. These notes record the decisions that came next.

## Decisions

### C. The derived graph is not tracked

`lattice/lattice.json` is rewritten by every `extract` (timestamps, and every
source edit). A project that tracks it never has a clean tree, so `lattice break`
refuses to run.

- `lattice init` adds it to `.gitignore`, next to `lattice/.cache/`.
- `lattice validate` warns when it is tracked.
- Making the file deterministic would not help: the graph changes with every
  source edit.

### U1. The ledger records decisions, not runs

Every `validate` run appended a `check-run` line to the tracked ledger. On the
adopter's repository these were most of the ledger. They also forced a commit,
and with it a review round, after each fix round.

- The tracked ledger keeps gate, flag and transition events.
- Routine run records go to the untracked cache, or nowhere.

### U2. A changed enforcer with an unchanged statement is named

When the code enforcing a promise changes and its statement does not, the
written promise may no longer describe the code. `check --base` and `impact`
add an info note, "re-read the statement". It never changes the verdict.

This belongs in `check` and `impact`, not `validate`: `validate` has no base to
compare with.

### U3. Run only the tests a change touches

`lattice impact --tests` lists the verifier tests of the touched promises,
grouped by `break.runners`, with ready-to-run commands. It lists and never runs
them. The full suite stays required on the merge commit.

### U4. A fail-before proof

For a fix, the useful proof is that a new test fails on the old code. Adopters
did this by hand: stash, rebuild, rerun. In one case it showed that a new test
passed either way.

The proposal is to run the new or changed verifiers of the touched promises on
the base commit, in a temporary worktree, and report `fails_on_base` or
`passes_on_base`. A run that cannot happen reads `unknown`, never a pass.

This is not what `break` does. `break` proves a test notices a crude break of
the enforcer, not that it fails on the specific old behaviour.

### U5. A moved enforcer is a gap, not a fall

Removing an enforcer read BROKEN even when the same change moved the guard
into another symbol, so honest refactors either kept dead wrappers or needed
a human-confirmed retirement. A removal now reads `ENFORCER_MOVED` when the
change adds an enforcer of the same tier 1 or 2 promise that did not enforce
it at base. The move opens a `break_tested` gap (`MOVE_NOT_BREAK_TESTED`)
that is new whatever the base read, and clears only when a break of each
added enforcer is killed at head. Kills on kept enforcers, waivers and older
break runs never clear it. A removal with only kept enforcers left, and any
removal on a tier-3 promise, stays `ENFORCER_REMOVED` (check:INV-1, INV-14).

### B. Acceptance cases under an invariant

Long invariants pull in many tests. When one of their tests fails, the report
names a paragraph, not a claim.

- **B1.** Each invariant may hold `cases`; this is "Proposal A". Reusing BRD user
  scenarios ("Proposal B") was rejected, because scenarios are actor journeys
  verified end to end, and cases are per rule.
- **B2.** Ids are stored `a, b, c`, referenced `@verifies f:INV-1#a`, and
  displayed `1a`. Ids are never reused; a retired case keeps its id.
- **B3.** `kind` is `happy`, `edge`, `error` or `security`.
- **B5.** Agents may draft cases. Each case has its own confirmation state, so a
  case added or edited after confirmation reads unconfirmed. Editing a case is a
  structural change.
- Coverage per case is computed by the kernel, never typed:
  - `covered`: a tagged verifier passed, and breaking the enforcer killed it;
  - `partial`;
  - `uncovered`;
  - `failing`.

```yaml
invariants:
  - id: INV-1
    statement: A fee never exceeds five percent of the amount.
    tier: 2
    cases:
      - { id: a, kind: happy, statement: A 100.00 withdrawal pays at most 5.00. }
      - { id: b, kind: edge,  statement: A 0.01 withdrawal pays no fee. }
      - { id: c, kind: error, statement: A negative amount is rejected first., enforcer: wallet.fees.validateAmount }
```

Cases are also the answer to "split the broad invariants". Splitting is still
right only where one invariant holds unrelated claims.

### B4. BRD sources; `story` held

BRDs get optional `sources` (`ticket`, `roadmap`, `help_page`, `api_doc` or `adr`,
with `ref`, `title` and `url`).

- `checked_by` and `checked_at` are set by a human only.
- The kernel checks the shape and never fetches a URL.
- An agent regenerator never writes them.

`story` is held. The word is reserved for the planned PRD/Story layer, and its
name must be settled first.

### A. Review UI rules

- **A1.** Seeded bad packets, used to measure whether reviewers catch them, are
  allowed only after real confirmations exist. A seeded packet can never produce
  a decision branch.
- **A2.** First approvals and structural drift take full friction: the reviewer
  types the version, and the key sections must have been viewed. Text-only drift
  gets a lighter dialog. Any edit to a promise, criterion or case statement is
  structural.
- **A3.** Batching is allowed within one BRD only.
- **A4.** Before the new UI, the existing map gets an assurance lens. Every colour
  comes from a kernel field. Unknown or `UNPROVEN` is never drawn as a pass.
- **A5.** `lattice serve` stays local and prepare-only until the hosted phase.
  It never confirms anything.

### D. Delegated confirmation

By default, `confirmed` counts only a platform approval by a non-author human,
or a hardware-key signature over the decision.

**Update: the signature route.** A project with one GitHub account cannot get a
non-author approval without a second account. `lattice sign` closes that gap: the
human signs the decision with an `sk-` SSH key, and `confirm` counts it only when
the key is in `lattice/allowed_signers` at the base commit (never the PR head) and
among the login's GitHub signing keys. Software keys are refused, so there is no
weaker label; the state is `human`, always shown with its method (`via: signature`,
login, fingerprint). Earlier local `brd ack` records stay as they are: re-confirm
them with `prepare` and `sign`. Delegation, below, stays the fallback for projects
without a hardware key, and its state stays `delegated`. A change that adds a key to `allowed_signers` counts only with an endorsement by a key
trusted before it (`allowed_signers.sig` in the same commit); the first file is
the root of trust (`ROOT_SIGNERS`), removals need nothing, and a key added
otherwise reads `SIGNER_NOT_ENDORSED`. That closes the rebase-merge hole of the
base-commit rule without relying on merge style. Planned next: an opt-in to
accept the signature route only. The user guide's "Confirm by signature" has the
threat model.

A project owner may delegate confirmation to an agent. That weakens a
guarantee: `confirmed` is otherwise the only signal that a non-agent read the
intent.

The design keeps that concern visible:

1. **The mandate is itself a decision.** The owner pins it with
   `lattice prepare mandate pin` and approves its PR on the platform. A mandate
   only typed into config grants nothing (`MANDATE_UNCONFIRMED`). Agents never
   pin, widen or renew a mandate.
2. **The delegate has its own platform identity**: a machine user, not a bot
   app. `lattice confirm` sees its approving review on the decision PR.
3. **Some things are never delegated:**
   - tier 3;
   - regulatory, legal or financial constraints;
   - anything the delegate wrote;
   - anything past the mandate's `tier_max` or `expires`.

   Codes: `DELEGATE_TIER_EXCEEDED`, `DELEGATE_CONSTRAINT_EXCLUDED`,
   `DELEGATE_SELF_REVIEW`, `MANDATE_EXPIRED`.
4. **The state is `delegated`, never `human`.**
   - A project counts it in the gate only with `confirm.count_delegated: true`.
   - The verdict says how many promises rest on a delegate.
5. **A weekly digest file lists delegated confirmations.** The owner can overturn
   any of them.

```yaml
# lattice/decisions/mandates/m-confirm-agent-<packet8>.yaml (merged through a PR the owner approved)
id: m-confirm-agent
grants: [brd_approve, promise_confirm]
delegate: { actor: reviewer-agent, platform: github, login: reviewer-agent-bot-user }
delegated_by: owner-login
tier_max: 2
excludes_constraints: [regulatory, legal, financial]
expires: "2027-05-10"
digest: weekly
```

## What is not decided

- The name of the Story layer, and with it the BRD `story` field.
- Whether U4's fail-before proof becomes a gate, or stays a signal.
