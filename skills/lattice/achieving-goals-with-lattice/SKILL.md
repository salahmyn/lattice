---
name: achieving-goals-with-lattice
description: Use Lattice to make the BRD goal true, not to satisfy its flow. Goal-first work, AMA as enablement, and closing the loop to demonstrated.
---

# Achieving goals with Lattice

Lattice exists to answer one question with evidence: **is what we built what
the business asked for?** Every artifact — BRD, feature, invariant, entry
point, test — is a means to make that answer *yes and true*. Optimize for the
true answer, never for a green check. A clean `validate` on a broken product
is the failure this skill prevents.

## The ceremony trap

You are in it when you are: writing a manifest to make a rule stop firing;
spraying `@enforces` across a class so the enforcer count rises; adding a
`@verifies` test that asserts something easier than the invariant; treating
`validate: clean` as "done." Green is a floor, not the goal.

## Work goal-first

1. **Start at intent, not the file.** Read the BRD goal and the
   `user_scenario` you are serving — `lattice brd show <id>`. Name the
   user-observable outcome before you touch code.
2. **Orient with the graph.** `lattice feature show <id>`, `lattice rtm`,
   `lattice view journeys/<brd>`, and `lattice_get_agent_context` tell you
   what already exists, what is unverified, and what entry point reaches the
   slice. `lattice next` ranks the weakest links if you have no assignment.
   Act *after* orienting, not before.
3. **Claim before you change.** In a multi-agent workspace, `lattice lease
   acquire <unit> --actor <you>` before editing a slice, and release it when
   done — overlapping unleased edits surface as `LEASE_SCOPE_OVERLAP`.
4. **Pick the smallest slice that moves a real number.** A change should push
   a concrete RTM / journey / coverage value toward *demonstrated* — not just
   keep `validate` green. If it moves no number, ask what you are actually
   verifying.
5. **Demonstrate, then declare.** Wire the invariant *and* the scenario
   verifier that exercises it through an entry point. See
   [[verifying-meaning]] for what "correctly meant and demonstrated" requires.

## AMA is enablement, not a cage

The AMA rules — vertical slices, no cross-feature imports, file/method line
caps, command-vs-query — exist so **you can act on one slice with one slice's
worth of context** and not break the rest. Treat them as the thing that makes
your job safe, not as the objective:

- When a rule fights the goal, that is a **signal to reshape the slice**
  (split it, extend it, or move the seam), not to `@suppress` it. Suppression
  is loud and rare and needs a reason.
- The ≤500-word `.ai-spec.md` is your loading contract: if you need more than
  the slice + its spec to make the change safely, the slice is too big —
  propose a split before coding.
- "No shared mutable state / features publish events" is what lets a future
  agent touch a neighbour without loading you. Honor the seam even when a
  shortcut compiles.

## Close the loop

Leave the slice **demonstrable by the next agent**: the scenario it serves has
a passing verifier through a declared entry point, the invariant means what the
success criterion means, and the RTM row reads `demonstrated`. Before claiming
it, run `lattice runs-clean` — the app must install, build, boot, and answer
its probes after your slice, not only at project end — and ingest your test
run (`lattice results ingest`) so the graph sees the green, not your word for
it. `lattice demonstrate` composes the whole sign-off and ledgers it. A task
is done when the *meaning* is proven, not when the *flow* is satisfied.

Two hard rules close the loop honestly: a row with an open flag
(`demonstrated⚑`) is NOT done — a human clears flags, not you; and changing a
grounded requirement (including deleting its tests) goes through
`lattice cr` — deletion is legal only against an approved retirement item,
and `lattice sweep` checks exactly that.

Related: [[verifying-meaning]], [[authoring-manifests]], [[diagnosing-violations]].
