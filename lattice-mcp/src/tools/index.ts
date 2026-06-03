// Tool definitions for the Lattice MCP server. Every tool wraps one `lattice`
// CLI subcommand. Descriptions follow a strict template (purpose, when-to-call,
// returns, common errors) enforced by a build-time test.

import { z, type ZodRawShape } from "zod";

/** ToolInput is the parsed argument object passed to a tool's toArgs. */
export type ToolInput = Record<string, unknown>;

/** CliInvocation is what a tool turns its input into. */
export interface CliInvocation {
  args: string[];
  stdin?: string;
}

/** ToolDef fully describes one MCP tool. */
export interface ToolDef {
  name: string;
  summary: string;
  whenToCall: string[];
  returns: string;
  commonErrors: string[];
  inputSchema: ZodRawShape;
  toArgs: (input: ToolInput) => CliInvocation;
}

/** renderDescription builds the templated description string for a tool. */
export function renderDescription(def: ToolDef): string {
  const when = def.whenToCall.map((w) => `  - ${w}`).join("\n");
  const errs = def.commonErrors.map((e) => `  - ${e}`).join("\n");
  return `${def.name}: ${def.summary}

When to call:
${when}

Returns: ${def.returns}

Common errors:
${errs}`;
}

const str = (input: ToolInput, key: string): string => String(input[key] ?? "");

/** The full v1.0 tool catalog. */
export const tools: ToolDef[] = [
  {
    name: "lattice_list_features",
    summary: "List every feature in the manifest corpus.",
    whenToCall: ["User asks what features exist", "Agent needs a feature overview before acting"],
    returns: "JSON array of {id, status, version, purpose}.",
    commonErrors: ["REPO_NOT_INITIALIZED: target directory has no .lattice/ folder"],
    inputSchema: {},
    toArgs: () => ({ args: ["feature", "list"] }),
  },
  {
    name: "lattice_get_feature",
    summary: "Full manifest for one feature with hydrated edges.",
    whenToCall: ["User asks about a specific feature by id", "Agent needs feature detail before changing it"],
    returns: "FeatureDetail JSON (manifest + implementations + verifications).",
    commonErrors: ["FEATURE_NOT_FOUND: feature_id is not in the corpus"],
    inputSchema: { feature_id: z.string() },
    toArgs: (i) => ({ args: ["feature", "show", str(i, "feature_id")] }),
  },
  {
    name: "lattice_get_symbol_context",
    summary: "Lattice context for one code symbol by fully-qualified name.",
    whenToCall: ["Agent is editing a symbol and needs its feature and invariants"],
    returns: "GraphSymbol JSON with resolved feature, capabilities, and invariants.",
    commonErrors: ["SYMBOL_NOT_FOUND: the fqn is not in the knowledge graph"],
    inputSchema: { fqn: z.string() },
    toArgs: (i) => ({ args: ["symbol", str(i, "fqn")] }),
  },
  {
    name: "lattice_get_blast_radius",
    summary: "Code-level impact of a symbol, derived from SCIP.",
    whenToCall: ["Agent needs to know what a change to a symbol affects"],
    returns: "BlastRadius JSON with definitions, references, and files.",
    commonErrors: ["SCIP_NOT_INDEXED: run extract --with-code-graph first"],
    inputSchema: { fqn: z.string() },
    toArgs: (i) => ({ args: ["blast-radius", str(i, "fqn")] }),
  },
  {
    name: "lattice_validate",
    summary: "Run every validation rule over the repository.",
    whenToCall: ["Before merging a change", "After editing manifests or annotations"],
    returns: "JSON {violations, errors, warnings, ok}.",
    commonErrors: ["EXTRACT_FAILED: the repository could not be parsed"],
    inputSchema: {},
    toArgs: () => ({ args: ["validate"] }),
  },
  {
    name: "lattice_extract",
    summary: "Extract the knowledge graph and write lattice.json.",
    whenToCall: ["After source or manifest changes, to refresh lattice.json"],
    returns: "JSON extraction summary with counts.",
    commonErrors: ["EXTRACT_FAILED: the repository could not be parsed"],
    inputSchema: {},
    toArgs: () => ({ args: ["extract"] }),
  },
  {
    name: "lattice_analyze_proposal",
    summary: "Conflict and impact analysis for a proposal manifest.",
    whenToCall: ["User proposes a change and wants its impact assessed"],
    returns: "ImpactReport JSON with deterministic and semantic findings.",
    commonErrors: ["ANALYZE_FAILED: the proposal file could not be loaded"],
    inputSchema: { path: z.string() },
    toArgs: (i) => ({ args: ["analyze", "proposal", str(i, "path")] }),
  },
  {
    name: "lattice_find_overlap",
    summary: "Semantic search across features, capabilities, and invariants.",
    whenToCall: ["Before adding a capability, to check for an existing equivalent"],
    returns: "JSON array of search hits ranked by similarity.",
    commonErrors: ["EXTRACT_FAILED: the repository could not be parsed"],
    inputSchema: { query: z.string() },
    toArgs: (i) => ({ args: ["search", str(i, "query"), "--semantic"] }),
  },
  {
    name: "lattice_preview_patch",
    summary: "Preview a typed patch without writing it.",
    whenToCall: ["Before applying any manifest, initiative, or task edit"],
    returns: "PatchPreview JSON with the diff and introduced/resolved violations.",
    commonErrors: ["PATCH_PARSE_FAILED: the patch JSON is malformed"],
    inputSchema: { patch: z.string() },
    toArgs: (i) => ({ args: ["patch", "--from-file", "-", "--preview"], stdin: str(i, "patch") }),
  },
  {
    name: "lattice_apply_patch",
    summary: "Apply a typed patch atomically, rolling back on new violations.",
    whenToCall: ["After a preview is acceptable, to commit the edit"],
    returns: "PatchResult JSON {applied, diff, rolled_back, violations}.",
    commonErrors: ["PATCH_APPLY_FAILED: stale base_version or unknown target"],
    inputSchema: { patch: z.string() },
    toArgs: (i) => ({ args: ["patch", "--from-file", "-", "--apply"], stdin: str(i, "patch") }),
  },
  {
    name: "lattice_suggest_annotation",
    summary: "Suggest Lattice annotations for a code symbol.",
    whenToCall: ["Agent added a symbol and needs to annotate it"],
    returns: "AnnotationResult JSON with ranked suggestions.",
    commonErrors: ["BAD_LINE: the line argument is not an integer"],
    inputSchema: { file: z.string(), line: z.number().int() },
    toArgs: (i) => ({ args: ["agent", "suggest-annotation", str(i, "file"), String(i.line)] }),
  },
  {
    name: "lattice_draft_proposal",
    summary: "Draft a proposal manifest from a prose change description.",
    whenToCall: ["User describes a change in prose and wants a manifest draft"],
    returns: "ProposalResult JSON with manifest YAML and open questions.",
    commonErrors: ["AGENT_FAILED: extraction failed"],
    inputSchema: { prose: z.string(), target: z.string().optional() },
    toArgs: (i) => {
      const args = ["agent", "draft-proposal", "--prose", "-"];
      if (i.target) args.push("--target", str(i, "target"));
      return { args, stdin: str(i, "prose") };
    },
  },
  {
    name: "lattice_recommend_decomposition",
    summary: "Recommend a sub-feature decomposition for an over-large feature.",
    whenToCall: ["A feature has grown past the complexity thresholds"],
    returns: "DecompositionResult JSON with proposed sub-features.",
    commonErrors: ["AGENT_FAILED: the feature id was not found"],
    inputSchema: { feature_id: z.string() },
    toArgs: (i) => ({ args: ["agent", "recommend-decomposition", str(i, "feature_id")] }),
  },
  {
    name: "lattice_list_initiatives",
    summary: "List every initiative in the repository.",
    whenToCall: ["User asks what work is in flight"],
    returns: "JSON array of initiatives.",
    commonErrors: ["EXTRACT_FAILED: the repository could not be parsed"],
    inputSchema: {},
    toArgs: () => ({ args: ["initiative", "list"] }),
  },
  {
    name: "lattice_get_initiative",
    summary: "Show one initiative and its tasks.",
    whenToCall: ["User asks about a specific initiative by id"],
    returns: "JSON {initiative, tasks}.",
    commonErrors: ["INITIATIVE_NOT_FOUND: the initiative id is unknown"],
    inputSchema: { initiative_id: z.string() },
    toArgs: (i) => ({ args: ["initiative", "show", str(i, "initiative_id")] }),
  },
  {
    name: "lattice_get_tasks",
    summary: "List tasks, optionally filtered to one initiative.",
    whenToCall: ["Agent needs the task list before picking work"],
    returns: "JSON array of tasks.",
    commonErrors: ["EXTRACT_FAILED: the repository could not be parsed"],
    inputSchema: { initiative_id: z.string().optional() },
    toArgs: (i) => {
      const args = ["task", "list"];
      if (i.initiative_id) args.push("--initiative", str(i, "initiative_id"));
      return { args };
    },
  },
  {
    name: "lattice_pick_next_task",
    summary: "Pick the next actionable task whose dependencies are satisfied.",
    whenToCall: ["An agent is ready to start work and needs an unblocked task"],
    returns: "JSON {task} or {task: null} when nothing is actionable.",
    commonErrors: ["EXTRACT_FAILED: the repository could not be parsed"],
    inputSchema: { initiative_id: z.string().optional() },
    toArgs: (i) => {
      const args = ["task", "pick-next"];
      if (i.initiative_id) args.push("--initiative", str(i, "initiative_id"));
      return { args };
    },
  },
  {
    name: "lattice_get_agent_context",
    summary: "Assemble a self-contained agent context bundle for a task.",
    whenToCall: ["An agent picked up a task and needs full context to act"],
    returns: "AgentContext JSON: task, manifests, code, tests, decisions, skills.",
    commonErrors: ["CONTEXT_FAILED: the task id was not found"],
    inputSchema: { task_id: z.string().optional() },
    toArgs: (i) => {
      const args = ["agent", "context"];
      if (i.task_id) args.push("--task", str(i, "task_id"));
      return { args };
    },
  },
  {
    name: "lattice_render_view",
    summary: "Render a view: developer, product, business, or agent_context.",
    whenToCall: ["User asks for a generated view of the system"],
    returns: "JSON {view, content} for markdown views.",
    commonErrors: ["UNKNOWN_VIEW: the view name is not recognized"],
    inputSchema: { name: z.string() },
    toArgs: (i) => ({ args: ["view", str(i, "name")] }),
  },
  {
    name: "lattice_run_mutation_tests",
    summary: "Run mutation tests over invariant-enforcing code.",
    whenToCall: ["CI gating, or when verifying invariant test strength"],
    returns: "Mutation Report JSON with per-invariant scores.",
    commonErrors: ["EXTRACT_FAILED: the repository could not be parsed"],
    inputSchema: { feature_id: z.string().optional() },
    toArgs: (i) => {
      const args = ["mutation", "run"];
      if (i.feature_id) args.push("--feature", str(i, "feature_id"));
      return { args };
    },
  },
  {
    name: "lattice_run_structural_checks",
    summary: "Run declared structural invariant checks as subprocesses.",
    whenToCall: ["Validating structural invariants in CI or before merge"],
    returns: "JSON {results, violations}.",
    commonErrors: ["EXTRACT_FAILED: the repository could not be parsed"],
    inputSchema: {},
    toArgs: () => ({ args: ["structural-checks", "run"] }),
  },
  // ─────────── v0.6 BRD / journey / actor / RTM surface ───────────
  // These tools turn the meaning layer added in v0.5+v0.6 into
  // structured answers an agent can branch on without re-reading code.
  {
    name: "lattice_list_brds",
    summary: "List every Business Requirements Document.",
    whenToCall: [
      "User asks what business intent the system covers",
      "Agent needs the BRD catalog before choosing scope",
    ],
    returns: "JSON array of {id, status, version, title, business_problem, implements_via, approval, provenance}.",
    commonErrors: ["EXTRACT_FAILED: the repository could not be parsed"],
    inputSchema: { status: z.string().optional() },
    toArgs: (i) => {
      const args = ["brd", "list"];
      if (i.status) args.push("--status", str(i, "status"));
      return { args };
    },
  },
  {
    name: "lattice_get_brd",
    summary: "Full BRD detail with implementing features.",
    whenToCall: [
      "User asks about a specific BRD by id",
      "Agent needs business intent context before changing a feature",
    ],
    returns: "JSON {brd, features} with the full BRD body + the feature ids in its scope.",
    commonErrors: ["BRD_NOT_FOUND: brd_id is not in the corpus"],
    inputSchema: { brd_id: z.string() },
    toArgs: (i) => ({ args: ["brd", "show", str(i, "brd_id")] }),
  },
  {
    name: "lattice_get_journey",
    summary: "Aggregate every entry point that touches a BRD's features.",
    whenToCall: [
      "User asks 'show me the X flow'",
      "Agent needs the full surface a business intent exercises before changing any single EP",
    ],
    returns: "JSON {brd_id, brd_title, features, entry_points, mermaid} — the same shape /journeys/{id} renders.",
    commonErrors: ["BRD_NOT_FOUND: brd_id is not in the corpus"],
    inputSchema: { brd_id: z.string() },
    toArgs: (i) => ({ args: ["journey", str(i, "brd_id")] }),
  },
  {
    name: "lattice_list_actors",
    summary: "List actors declared in lattice/context.yaml with EP/feature counts.",
    whenToCall: ["User asks who can use the system", "Agent needs actor coverage before drafting a flow"],
    returns: "JSON array of {id, name, feature_count, ep_count}.",
    commonErrors: ["CONTEXT_LOAD_FAILED: lattice/context.yaml is missing or malformed"],
    inputSchema: {},
    toArgs: () => ({ args: ["actor", "list"] }),
  },
  {
    name: "lattice_get_actor_touchpoints",
    summary: "Every entry point and feature one actor can exercise.",
    whenToCall: [
      "User asks 'what can a Merchant do here?'",
      "Agent needs the full touchpoint set before drafting an actor-centric change",
    ],
    returns: "JSON {actor, features, entry_points, brds} with the BRDs those features implement.",
    commonErrors: ["ACTOR_NOT_FOUND: actor_id is not declared in context.yaml"],
    inputSchema: { actor_id: z.string() },
    toArgs: (i) => ({ args: ["actor", "show", str(i, "actor_id")] }),
  },
  {
    name: "lattice_verify_brd",
    summary: "Walk a BRD's success_criteria to enforcers + verifiers + mutation; report per-row status.",
    whenToCall: [
      "User asks 'is this BRD actually verified?'",
      "Agent needs to know which business goals lack backing tests before declaring complete",
    ],
    returns: "JSON {coverage, matrix} — per-row status (verified/partial/unenforced/unverified/unmapped/phantom) for one BRD.",
    commonErrors: ["EXTRACT_FAILED: the repository could not be parsed"],
    inputSchema: { brd_id: z.string() },
    toArgs: (i) => ({ args: ["rtm", "--brd", str(i, "brd_id")] }),
  },
  {
    name: "lattice_list_unverified_criteria",
    summary: "List every BRD success_criterion that is NOT in verified state.",
    whenToCall: [
      "Agent is auditing 'what business goals don't trace to passing verification?'",
      "Pre-merge check that no shipped BRD lost its verification chain",
    ],
    returns: "JSON {coverage, matrix} — only rows whose status is unmapped/unverified/unenforced/partial/phantom.",
    commonErrors: ["EXTRACT_FAILED: the repository could not be parsed"],
    inputSchema: { status: z.string().optional() },
    toArgs: (i) => {
      // Default to unmapped — the most common starting point. Caller
      // can pass a different status filter to drill in.
      const args = ["rtm", "--status", i.status ? str(i, "status") : "unmapped"];
      return { args };
    },
  },
];
