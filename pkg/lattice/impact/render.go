package impact

// @module-feature impact

import (
	"fmt"
	"io"
	"sort"
	"strings"
)

// MarkdownMarker opens every markdown report, so a CI job can find and
// replace its own sticky pull-request comment.
const MarkdownMarker = "<!-- lattice-impact -->"

// collapseAt is the row count above which a markdown list is folded into a
// <details> block, so a large change keeps the comment readable.
const collapseAt = 20

func short(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}

func baseLine(r Report) string {
	s := fmt.Sprintf("base %s (merge base of %s and HEAD %s)", short(r.Base), r.Since, short(r.Head))
	if r.Uncommitted {
		s += ", including uncommitted changes"
	}
	return s
}

// --- markdown ---

// Markdown writes the report as a pull-request comment.
func Markdown(w io.Writer, r Report) {
	p := func(format string, a ...interface{}) { fmt.Fprintf(w, format, a...) }
	p("%s\n## Lattice impact\n\n", MarkdownMarker)
	p("Compared with %s.\n\n%s · %s · %s · %s\n\n", mdBase(r),
		plural(len(r.ChangedFiles), "file changed", "files changed"),
		plural(len(r.ChangedSymbols), "symbol changed", "symbols changed"),
		plural(len(r.AffectedCriteria), "approved criterion affected", "approved criteria affected"),
		plural(len(r.RemovedEnforcement), "enforcement removed", "enforcements removed"))

	p("### Changed code\n\n")
	if len(r.ChangedSymbols) == 0 {
		p("_No symbol's own lines changed._\n\n")
	} else {
		open(w, len(r.ChangedSymbols), "changed symbols")
		p("| Change | Symbol | File | Lines | Enforces |\n|---|---|---|---|---|\n")
		for _, s := range r.ChangedSymbols {
			p("| %s | %s | %s | %s | %s |\n", s.Change, code(s.Symbol), code(fileWithOld(s.File, s.OldFile)),
				linesText(s), codeList(s.Enforces))
		}
		closeDetails(w, len(r.ChangedSymbols))
		p("\n")
	}

	p("### Affected criteria: approved BRDs\n\n")
	mdCriteria(w, r.AffectedCriteria)
	if r.DraftsIncluded {
		p("### Affected criteria: draft and proposed BRDs (not yet approved)\n\n")
		p("_These BRDs are not the source of truth until a human approves them._\n\n")
		mdCriteria(w, r.DraftCriteria)
	}

	p("### Removed enforcement\n\n")
	if len(r.RemovedEnforcement) == 0 {
		p("_No enforcer stopped enforcing its invariant._\n\n")
	} else {
		p("| Symbol | Invariant | File | Why |\n|---|---|---|---|\n")
		for _, re := range r.RemovedEnforcement {
			p("| %s | %s | %s | %s |\n", code(re.Symbol), code(re.Invariant), code(fmt.Sprintf("%s:%d", re.File, re.Line)), cell(ReasonText(re)))
		}
		p("\n")
	}

	p("### Manifest changes\n\n")
	if r.ManifestDiff.Empty() {
		p("_No feature, BRD or entry-point file changed._\n\n")
	} else {
		mdKind(w, "Features", r.ManifestDiff.Features)
		mdKind(w, "BRDs", r.ManifestDiff.BRDs)
		mdKind(w, "Entry points", r.ManifestDiff.EntryPoints)
	}

	p("### Code roll-up\n\n")
	cr := r.CodeRollup
	switch {
	case !cr.Available:
		p("_%s._\n", cr.Note)
	case cr.Total == 0:
		p("_No changed or removed enforcer to expand._\n")
	default:
		p("Expanded %d of %s", cr.Expanded, plural(cr.Total, "enforcer symbol", "enforcer symbols"))
		if cr.Truncated {
			p(" (truncated at --max-symbols %d)", cr.MaxSymbols)
		}
		p(".\n\n| Enforcer | References | Modules | Test files | Entry points |\n|---|---|---|---|---|\n")
		for _, s := range cr.Symbols {
			refs := fmt.Sprint(s.References)
			if !s.Resolved {
				refs = "not in index"
			}
			p("| %s | %s | %d | %d | %s |\n", code(s.Symbol), refs, len(s.Modules), len(s.TestFiles), codeList(s.EntryPoints))
		}
		if len(cr.EntryPoints) > 0 {
			p("\n**Entry points reached**\n\n")
			for _, ep := range cr.EntryPoints {
				p("- %s (%s%s) via %s\n", code(ep.ID), ep.Kind, triggerSuffix(ep.Trigger), strings.Join(ep.Via, ", "))
			}
		}
		if len(cr.TestFiles) > 0 {
			p("\n**Test files referencing them**: %s\n", codeList(cr.TestFiles))
		}
	}
}

func mdBase(r Report) string {
	s := fmt.Sprintf("base `%s` (merge base of `%s` and HEAD `%s`)", short(r.Base), r.Since, short(r.Head))
	if r.Uncommitted {
		s += ", including uncommitted changes"
	}
	return s
}

func mdCriteria(w io.Writer, list []AffectedCriterion) {
	if len(list) == 0 {
		fmt.Fprintf(w, "_None._\n\n")
		return
	}
	for _, c := range list {
		dw := ""
		if c.DirectWire {
			dw = ", direct-wired"
		}
		fmt.Fprintf(w, "- **%s / %s** (tier %d%s): %s\n", code(c.BRD), c.Criterion, c.Tier, dw, c.Statement)
		if len(c.Invariants) > 0 {
			fmt.Fprintf(w, "  - invariants: %s\n", codeList(c.Invariants))
		}
		for _, ref := range groupRefs(c.Enforcers) {
			var parts []string
			for _, e := range c.Enforcers {
				if e.Invariant == ref {
					parts = append(parts, fmt.Sprintf("%s (%s, %s)", code(e.Symbol), changeText(e.Change), code(e.File)))
				}
			}
			fmt.Fprintf(w, "  - %s changed enforcers: %s\n", code(ref), strings.Join(parts, "; "))
		}
	}
	fmt.Fprintln(w)
}

func mdKind(w io.Writer, title string, k KindDiff) {
	if k.Empty() {
		return
	}
	n := len(k.Added) + len(k.Removed) + len(k.Renamed) + len(k.Changed)
	fmt.Fprintf(w, "**%s**\n\n", title)
	open(w, n, strings.ToLower(title)+" changed")
	for _, a := range k.Added {
		fmt.Fprintf(w, "- added %s: %s%s\n", code(a.ID), code(a.Path), errSuffix(a.Error))
	}
	for _, a := range k.Removed {
		fmt.Fprintf(w, "- removed %s: %s%s\n", code(a.ID), code(a.Path), errSuffix(a.Error))
	}
	for _, c := range k.Renamed {
		fmt.Fprintf(w, "- renamed %s\n", renameText(c, code))
		mdFields(w, c)
	}
	for _, c := range k.Changed {
		fmt.Fprintf(w, "- changed %s: %s\n", code(c.ID), code(c.Path))
		mdFields(w, c)
	}
	closeDetails(w, n)
	fmt.Fprintln(w)
}

func mdFields(w io.Writer, c ManifestChange) {
	if c.Error != "" {
		fmt.Fprintf(w, "  - could not compare: %s\n", c.Error)
	}
	for _, f := range c.Fields {
		fmt.Fprintf(w, "  - %s: %s\n", code(f.Field), fieldText(f, code))
	}
}

func open(w io.Writer, n int, what string) {
	if n > collapseAt {
		fmt.Fprintf(w, "<details><summary>%d %s</summary>\n\n", n, what)
	}
}

func closeDetails(w io.Writer, n int) {
	if n > collapseAt {
		fmt.Fprintf(w, "\n</details>\n")
	}
}

// code renders s as an inline code span safe inside a table cell.
func code(s string) string {
	if s == "" {
		return ""
	}
	s = strings.ReplaceAll(s, "|", `\|`)
	if strings.Contains(s, "`") {
		return "`` " + s + " ``"
	}
	return "`" + s + "`"
}

func cell(s string) string { return strings.ReplaceAll(s, "|", `\|`) }

func codeList(list []string) string {
	var out []string
	for _, s := range list {
		out = append(out, code(s))
	}
	return strings.Join(out, ", ")
}

// --- text ---

// Text writes the report for a terminal.
func Text(w io.Writer, r Report) {
	p := func(format string, a ...interface{}) { fmt.Fprintf(w, format, a...) }
	plain := func(s string) string { return s }
	p("lattice impact: %s\n", baseLine(r))
	p("  %s, %s\n\n", plural(len(r.ChangedFiles), "file changed", "files changed"),
		plural(len(r.ChangedSymbols), "symbol changed", "symbols changed"))

	p("CHANGED CODE\n")
	if len(r.ChangedSymbols) == 0 {
		p("  (no symbol's own lines changed)\n")
	}
	for _, s := range r.ChangedSymbols {
		p("  %-9s %s  %s:%s", s.Change, s.Symbol, fileWithOld(s.File, s.OldFile), linesText(s))
		if len(s.Enforces) > 0 {
			p("  enforces %s", strings.Join(s.Enforces, ", "))
		}
		p("\n")
	}

	p("\nAFFECTED CRITERIA (approved BRDs)\n")
	textCriteria(w, r.AffectedCriteria)
	if r.DraftsIncluded {
		p("\nAFFECTED CRITERIA (draft and proposed BRDs, not yet approved)\n")
		textCriteria(w, r.DraftCriteria)
	}

	p("\nREMOVED ENFORCEMENT\n")
	if len(r.RemovedEnforcement) == 0 {
		p("  (none)\n")
	}
	for _, re := range r.RemovedEnforcement {
		p("  %s  %s  %s:%d  %s\n", re.Symbol, re.Invariant, re.File, re.Line, ReasonText(re))
	}

	p("\nMANIFEST CHANGES\n")
	if r.ManifestDiff.Empty() {
		p("  (none)\n")
	}
	for _, kd := range []struct {
		title string
		k     KindDiff
	}{{"features", r.ManifestDiff.Features}, {"brds", r.ManifestDiff.BRDs}, {"entry points", r.ManifestDiff.EntryPoints}} {
		if kd.k.Empty() {
			continue
		}
		p("  %s:\n", kd.title)
		for _, a := range kd.k.Added {
			p("    added    %s  %s%s\n", a.ID, a.Path, errSuffix(a.Error))
		}
		for _, a := range kd.k.Removed {
			p("    removed  %s  %s%s\n", a.ID, a.Path, errSuffix(a.Error))
		}
		for _, c := range kd.k.Renamed {
			p("    renamed  %s\n", renameText(c, plain))
			textFields(w, c)
		}
		for _, c := range kd.k.Changed {
			p("    changed  %s  %s\n", c.ID, c.Path)
			textFields(w, c)
		}
	}

	p("\nCODE ROLL-UP\n")
	cr := r.CodeRollup
	switch {
	case !cr.Available:
		p("  %s\n", cr.Note)
	case cr.Total == 0:
		p("  (no changed or removed enforcer to expand)\n")
	default:
		p("  expanded %d of %s", cr.Expanded, plural(cr.Total, "enforcer symbol", "enforcer symbols"))
		if cr.Truncated {
			p(" (truncated at --max-symbols %d)", cr.MaxSymbols)
		}
		p("\n")
		for _, s := range cr.Symbols {
			if !s.Resolved {
				p("  %s: not in the SCIP index\n", s.Symbol)
				continue
			}
			p("  %s: %s, %s, %s\n", s.Symbol, plural(s.References, "reference", "references"),
				plural(len(s.Modules), "module", "modules"), plural(len(s.TestFiles), "test file", "test files"))
		}
		for _, ep := range cr.EntryPoints {
			p("  entry point %s (%s%s) via %s\n", ep.ID, ep.Kind, triggerSuffix(ep.Trigger), strings.Join(ep.Via, ", "))
		}
	}
}

func textCriteria(w io.Writer, list []AffectedCriterion) {
	if len(list) == 0 {
		fmt.Fprintf(w, "  (none)\n")
		return
	}
	for _, c := range list {
		fmt.Fprintf(w, "  %s/%s (tier %d): %s\n", c.BRD, c.Criterion, c.Tier, c.Statement)
		for _, ref := range groupRefs(c.Enforcers) {
			var parts []string
			for _, e := range c.Enforcers {
				if e.Invariant == ref {
					parts = append(parts, fmt.Sprintf("%s (%s)", e.Symbol, changeText(e.Change)))
				}
			}
			fmt.Fprintf(w, "    %s: %s\n", ref, strings.Join(parts, "; "))
		}
	}
}

func textFields(w io.Writer, c ManifestChange) {
	if c.Error != "" {
		fmt.Fprintf(w, "      could not compare: %s\n", c.Error)
	}
	for _, f := range c.Fields {
		fmt.Fprintf(w, "      %s: %s\n", f.Field, fieldText(f, func(s string) string { return s }))
	}
}

// --- shared ---

func groupRefs(list []EnforcerChange) []string {
	seen := map[string]bool{}
	var out []string
	for _, e := range list {
		if !seen[e.Invariant] {
			seen[e.Invariant] = true
			out = append(out, e.Invariant)
		}
	}
	return out
}

func changeText(c string) string {
	switch c {
	case ChangeEnforcementRemoved:
		return "enforcement removed"
	case ChangeEnforcementAdded:
		return "enforcement added"
	case ChangeUnscanned:
		return "left the scanned tree"
	}
	return c
}

func renameText(c ManifestChange, q func(string) string) string {
	var parts []string
	if c.OldID != "" {
		parts = append(parts, q(c.OldID)+" → "+q(c.ID))
	} else {
		parts = append(parts, q(c.ID))
	}
	if c.OldPath != "" {
		parts = append(parts, q(c.OldPath)+" → "+q(c.Path))
	} else {
		parts = append(parts, q(c.Path))
	}
	return strings.Join(parts, ": ")
}

func fieldText(f FieldChange, q func(string) string) string {
	switch f.Change {
	case "added":
		return "added: " + q(fmtValue(f.After))
	case "removed":
		return "removed (was " + q(fmtValue(f.Before)) + ")"
	}
	return q(fmtValue(f.Before)) + " → " + q(fmtValue(f.After))
}

// fmtValue renders a YAML value compactly on one line.
func fmtValue(v interface{}) string {
	switch t := v.(type) {
	case nil:
		return "(empty)"
	case string:
		return strings.Join(strings.Fields(t), " ")
	case []interface{}:
		var parts []string
		for _, x := range t {
			parts = append(parts, fmtValue(x))
		}
		return "[" + strings.Join(parts, ", ") + "]"
	case map[string]interface{}:
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		var parts []string
		for _, k := range keys {
			parts = append(parts, k+": "+fmtValue(t[k]))
		}
		return "{" + strings.Join(parts, ", ") + "}"
	}
	return fmt.Sprint(v)
}

func fileWithOld(file, old string) string {
	if old != "" && old != file {
		return old + " → " + file
	}
	return file
}

func linesText(s ChangedSymbol) string {
	switch {
	case s.Lines != "" && s.OldLines != "" && s.Lines != s.OldLines:
		return s.Lines + " (was " + s.OldLines + ")"
	case s.Lines != "":
		return s.Lines
	}
	return s.OldLines + " (base)"
}

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}

func triggerSuffix(t string) string {
	if t == "" {
		return ""
	}
	return " " + t
}

func errSuffix(e string) string {
	if e == "" {
		return ""
	}
	return " (does not parse: " + e + ")"
}
