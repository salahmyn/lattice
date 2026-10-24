package validate

// @module-feature validation

import (
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/salahmyn/lattice/pkg/lattice/featurespec"
	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

// checkAMA runs the five v0.7 AMA structural rules. All five default
// to warning. CROSS_FEATURE_IMPORT escalates to error when
// `architecture.ama_mode: true`; MIXED_COMMAND_QUERY only fires when
// ama_mode is on (a `mixed` capability is the legacy default and
// shouldn't pollute the validation noise floor for projects that
// haven't opted into AMA). The line caps follow the same rule: they run
// under ama_mode, or when the workspace sets file_line_cap /
// method_line_cap itself. AMA's 150/25 defaults fire on almost every
// component and test of a project that never chose AMA, which buries
// the findings that matter.
//
// The rules read the same KnowledgeGraph + Config that every other
// rule uses; the AMA-specific knobs live in `cfg.Architecture` and
// the IR's LineCount field. No new dependencies.
func (c *corpus) checkAMA() []schema.Violation {
	var v []schema.Violation
	v = append(v, c.checkCrossFeatureImport()...)
	v = append(v, c.checkFeatureColocation()...)
	if c.cfg.Architecture.AMAMode || c.cfg.Architecture.FileLineCap > 0 {
		v = append(v, c.checkFileLineCap()...)
	}
	if c.cfg.Architecture.AMAMode || c.cfg.Architecture.MethodLineCap > 0 {
		v = append(v, c.checkMethodLineCap()...)
	}
	v = append(v, c.checkMixedCommandQuery()...)
	v = append(v, c.checkFeatureSpecSize()...)
	return v
}

// checkFeatureSpecSize fires when a feature's deterministic
// .ai-spec.md render exceeds the AMA word cap (default 500 words).
// Surfaced as info severity so it doesn't drown the dashboard —
// the message is a decomposition hint, not a contract violation.
//
// The cap is the same WordCap constant used by `lattice feature spec`,
// so an operator who reads the spec and sees ~600 words gets the
// matching warning here without a separate tolerance to tune.
func (c *corpus) checkFeatureSpecSize() []schema.Violation {
	var v []schema.Violation
	for _, f := range c.kg.Features {
		spec := featurespec.Render(f)
		words := featurespec.WordCount(spec)
		if words <= featurespec.WordCap {
			continue
		}
		v = append(v, schema.Violation{
			Code:      schema.CodeFeatureSpecTooLarge,
			Severity:  schema.SeverityInfo,
			FeatureID: f.ID,
			Message: fmt.Sprintf("feature %q .ai-spec.md is %d words (cap %d) — consider decomposition",
				f.ID, words, featurespec.WordCap),
			Location: &schema.Location{File: f.SourcePath},
			NextAction: &schema.NextAction{
				Kind:   "decompose_feature",
				Detail: "split into sub-features so each spec fits the AMA ≤500-word ceiling",
			},
		})
	}
	return v
}

// checkCrossFeatureImport fires when feature A's symbols depend on
// feature B (any feature other than A's own). The existing
// DEPENDS_ON_FEATURE_NOT_DECLARED rule flags *undeclared* deps;
// AMA's contract is stricter — even declared cross-feature imports
// are wrong unless they go through `Core/Contracts/*` and the
// mediator. We dedupe per (source feature, target feature, file)
// so a feature with 100 cross-imports doesn't produce 100 rows.
func (c *corpus) checkCrossFeatureImport() []schema.Violation {
	severity := schema.SeverityWarning
	if c.cfg.Architecture.AMAMode {
		severity = schema.SeverityError
	}

	type edge struct{ src, dst, file string }
	seen := map[edge]bool{}
	var v []schema.Violation

	for _, sym := range c.kg.Symbols {
		if sym.Feature == "" || len(sym.DependsOnFeatures) == 0 {
			continue
		}
		for _, dst := range sym.DependsOnFeatures {
			if dst == sym.Feature {
				continue
			}
			e := edge{src: sym.Feature, dst: dst, file: sym.File}
			if seen[e] {
				continue
			}
			seen[e] = true
			v = append(v, schema.Violation{
				Code:      schema.CodeCrossFeatureImport,
				Severity:  severity,
				FeatureID: sym.Feature,
				Message: fmt.Sprintf("feature %q crosses into feature %q (file %s) — AMA bans direct feature-to-feature imports",
					sym.Feature, dst, sym.File),
				Location: &schema.Location{File: sym.File, Line: sym.Line},
				NextAction: &schema.NextAction{
					Kind:   "refactor_to_mediator",
					Detail: "publish an event or route through Core/Contracts/* instead of the direct cross-feature import",
				},
			})
		}
	}
	return v
}

// checkFeatureColocation fires when a feature's implementations span
// more than one top-level directory. AMA's "vertical slice" rule
// requires each feature to live in one folder; outside ama_mode this
// is a hygiene signal that the feature is sprawling and may need
// decomposition.
//
// "Top-level directory" is the first slash-segment of the file path
// relative to the code root (e.g. `app/Checkout/...` → `app`,
// `Modules/Checkout/...` → `Modules`). Good enough for the warning;
// the exact rule the user wires later can use config.exclude.
func (c *corpus) checkFeatureColocation() []schema.Violation {
	history := changeHistoryFiles(c.kg)
	var v []schema.Violation
	for _, f := range c.kg.Features {
		if len(f.Implementations) == 0 {
			continue
		}
		dirs := map[string]bool{}
		var sampleFiles []string
		for _, impl := range f.Implementations {
			// A feature legitimately owns rules in database migrations next
			// to its code; migrations do not count toward colocation.
			if history[impl.File] {
				continue
			}
			top := topDir(impl.File)
			if !dirs[top] {
				dirs[top] = true
				sampleFiles = append(sampleFiles, top)
			}
		}
		if len(dirs) <= 1 {
			continue
		}
		sort.Strings(sampleFiles)
		v = append(v, schema.Violation{
			Code:      schema.CodeFeatureNotColocated,
			Severity:  schema.SeverityWarning,
			FeatureID: f.ID,
			Message: fmt.Sprintf("feature %q spans %d top-level directories: %s — consider decomposition or relocation",
				f.ID, len(dirs), strings.Join(sampleFiles, ", ")),
			Location: &schema.Location{File: f.SourcePath},
			NextAction: &schema.NextAction{
				Kind:   "decompose_or_relocate",
				Detail: "AMA expects each feature to live in one folder; split or move so all implementations colocate",
			},
		})
	}
	return v
}

// checkFileLineCap walks every parsed module and fires when its
// LineCount exceeds the configured cap (default 150). One row per
// over-limit file — the operator wants to know where to refactor,
// not get one rolled-up count.
// @enforces validation:INV-10
func (c *corpus) checkFileLineCap() []schema.Violation {
	cap := c.cfg.Architecture.EffectiveFileLineCap()
	tests := map[string]bool{}
	for _, t := range c.kg.Tests {
		tests[t.FQN] = true
	}
	var v []schema.Violation
	for _, mod := range c.kg.Modules {
		// A migration is change history: it cannot be split after it is applied.
		if mod.LineCount <= cap || onlyTests(mod, tests) || mod.ChangeHistory {
			continue
		}
		v = append(v, schema.Violation{
			Code:     schema.CodeFileLineCap,
			Severity: schema.SeverityWarning,
			Subject:  mod.File,
			Message: fmt.Sprintf("file %s is %d lines (cap %d) — AMA recommends splitting",
				mod.File, mod.LineCount, cap),
			Location: &schema.Location{File: mod.File, Line: cap},
			NextAction: &schema.NextAction{
				Kind:   "split_file",
				Detail: fmt.Sprintf("extract logic into helpers, sub-features, or Core/Contracts until each file is <= %d lines", cap),
			},
		})
	}
	return v
}

// onlyTests reports whether every symbol of a module is a test: a test
// file is long by nature and is not what the file cap is about.
func onlyTests(mod schema.GraphModule, tests map[string]bool) bool {
	if len(mod.SymbolFQNs) == 0 {
		return false
	}
	for _, fqn := range mod.SymbolFQNs {
		if !tests[fqn] {
			return false
		}
	}
	return true
}

// checkMethodLineCap fires for each function/method whose footprint
// exceeds the configured cap (default 25). The footprint is the
// declaration's own extent when the adapter reports its end line.
// Otherwise it is approximated by the distance to the next same-file
// symbol's start line, with the module LineCount as the bound for the
// last symbol (which over-reports a short function at the end of a
// file). Test symbols live in kg.Tests and are never measured.
// @enforces validation:INV-10
func (c *corpus) checkMethodLineCap() []schema.Violation {
	cap := c.cfg.Architecture.EffectiveMethodLineCap()

	// Index symbols per file, sorted by start line.
	symsByFile := map[string][]schema.GraphSymbol{}
	for _, sym := range c.kg.Symbols {
		if !isMethodKind(sym.Kind) {
			continue
		}
		symsByFile[sym.File] = append(symsByFile[sym.File], sym)
	}
	// Need module line counts for the last-symbol fallback.
	moduleLines := map[string]int{}
	history := changeHistoryFiles(c.kg)
	for _, mod := range c.kg.Modules {
		moduleLines[mod.File] = mod.LineCount
	}

	var v []schema.Violation
	for file, syms := range symsByFile {
		if history[file] {
			continue // a function in an applied migration is changed by a new migration, not a refactor
		}
		sort.Slice(syms, func(i, j int) bool { return syms[i].Line < syms[j].Line })
		for i, sym := range syms {
			end := moduleLines[file]
			if i+1 < len(syms) {
				end = syms[i+1].Line - 1
			}
			if sym.EndLine >= sym.Line {
				end = sym.EndLine
			}
			footprint := end - sym.Line + 1
			if footprint <= cap {
				continue
			}
			v = append(v, schema.Violation{
				Code:      schema.CodeMethodLineCap,
				Severity:  schema.SeverityWarning,
				Subject:   sym.FQN,
				FeatureID: sym.Feature,
				Message: fmt.Sprintf("symbol %s is ~%d lines (cap %d) — AMA recommends splitting",
					sym.FQN, footprint, cap),
				Location: &schema.Location{File: sym.File, Line: sym.Line},
				NextAction: &schema.NextAction{
					Kind:   "split_method",
					Detail: fmt.Sprintf("extract helpers until each method is <= %d lines", cap),
				},
			})
		}
	}
	return v
}

// checkMixedCommandQuery flags capabilities whose Kind is `mixed`
// (the legacy default). Only fires when `ama_mode: true` is set —
// outside AMA, `mixed` is the perfectly valid default, and we
// shouldn't generate noise.
func (c *corpus) checkMixedCommandQuery() []schema.Violation {
	if !c.cfg.Architecture.AMAMode {
		return nil
	}
	var v []schema.Violation
	for _, f := range c.kg.Features {
		for _, cap := range f.Capabilities {
			if cap.EffectiveKind() != schema.CapabilityMixed {
				continue
			}
			v = append(v, schema.Violation{
				Code:         schema.CodeMixedCommandQuery,
				Severity:     schema.SeverityWarning,
				FeatureID:    f.ID,
				CapabilityID: cap.ID,
				Message: fmt.Sprintf("capability %s:%s is `mixed` — AMA requires command/query separation",
					f.ID, cap.ID),
				Location: &schema.Location{File: f.SourcePath},
				NextAction: &schema.NextAction{
					Kind:   "edit_manifest",
					Field:  "capabilities.kind",
					Detail: "set kind: command (state writer) or kind: query (state reader); split if the capability does both",
				},
			})
		}
	}
	return v
}

// isMethodKind reports whether a symbol kind is a function or method
// (the AMA cap applies to executable code, not types). The adapter
// layer uses string kinds matching ir.KindFunction / ir.KindMethod.
func isMethodKind(k string) bool {
	switch k {
	case "function", "method":
		return true
	}
	return false
}

// topDir returns the first path segment of file. For
// `app/Checkout/Refund.php` → `app`; for a bare file at the root,
// the file itself is returned (single-dir, won't trigger the rule).
func topDir(file string) string {
	clean := path.Clean(file)
	if i := strings.IndexByte(clean, '/'); i >= 0 {
		return clean[:i]
	}
	return clean
}

// changeHistoryFiles is the set of files that record changes rather than
// hold code (database migrations): structural rules do not apply to them.
func changeHistoryFiles(kg schema.KnowledgeGraph) map[string]bool {
	out := map[string]bool{}
	for _, m := range kg.Modules {
		if m.ChangeHistory {
			out[m.File] = true
		}
	}
	return out
}
