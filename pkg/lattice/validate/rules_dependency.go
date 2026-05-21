package validate

import (
	"fmt"
	"sort"
	"strings"

	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

// checkDependencies runs the dependency-integrity rules.
func (c *corpus) checkDependencies() []schema.Violation {
	var v []schema.Violation

	for i := range c.kg.Features {
		m := &c.kg.Features[i]
		loc := &schema.Location{File: m.SourcePath}

		// DEPENDS_ON_MISSING.
		for _, dep := range m.DependsOn {
			if _, ok := c.features[dep]; !ok {
				v = append(v, schema.Violation{
					Code: schema.CodeDependsOnMissing, Severity: schema.SeverityError,
					FeatureID: m.ID, Location: loc,
					Message: fmt.Sprintf("depends_on references unknown feature %q", dep),
					NextAction: &schema.NextAction{Kind: "edit_manifest", Field: "depends_on",
						Detail: "remove the dependency or create the referenced feature"},
				})
			}
		}

		// SUBFEATURE_PARENT_MISSING.
		if idx := strings.LastIndex(m.ID, "."); idx > 0 {
			parent := m.ID[:idx]
			if _, ok := c.features[parent]; !ok {
				v = append(v, schema.Violation{
					Code: schema.CodeSubfeatureParentMissing, Severity: schema.SeverityError,
					FeatureID: m.ID, Location: loc,
					Message: fmt.Sprintf("sub-feature %q has no parent feature %q", m.ID, parent),
					NextAction: &schema.NextAction{Kind: "create_manifest",
						Ref: parent, Detail: "create the parent feature manifest"},
				})
			}
		}
	}

	v = append(v, c.dependencyCycles()...)
	return v
}

// dependencyCycles detects cycles in the depends_on graph.
func (c *corpus) dependencyCycles() []schema.Violation {
	const (
		white = 0
		gray  = 1
		black = 2
	)
	color := map[string]int{}
	var stack []string
	var cycles [][]string

	var visit func(id string)
	visit = func(id string) {
		m, ok := c.features[id]
		if !ok {
			return
		}
		color[id] = gray
		stack = append(stack, id)
		deps := append([]string(nil), m.DependsOn...)
		sort.Strings(deps)
		for _, dep := range deps {
			switch color[dep] {
			case white:
				visit(dep)
			case gray:
				cycles = append(cycles, extractCycle(stack, dep))
			}
		}
		stack = stack[:len(stack)-1]
		color[id] = black
	}

	ids := make([]string, 0, len(c.features))
	for id := range c.features {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		if color[id] == white {
			visit(id)
		}
	}

	reported := map[string]bool{}
	var v []schema.Violation
	for _, cyc := range cycles {
		key := cycleKey(cyc)
		if reported[key] {
			continue
		}
		reported[key] = true
		head := cyc[0]
		loc := &schema.Location{}
		if m, ok := c.features[head]; ok {
			loc.File = m.SourcePath
		}
		v = append(v, schema.Violation{
			Code: schema.CodeDependsOnCycle, Severity: schema.SeverityError,
			FeatureID: head, Location: loc,
			Message: "dependency cycle: " + strings.Join(append(cyc, head), " -> "),
			NextAction: &schema.NextAction{Kind: "edit_manifest", Field: "depends_on",
				Detail: "break the cycle by removing one dependency edge"},
		})
	}
	return v
}

// extractCycle returns the slice of stack from the first occurrence of back.
func extractCycle(stack []string, back string) []string {
	for i, id := range stack {
		if id == back {
			return append([]string(nil), stack[i:]...)
		}
	}
	return append([]string(nil), back)
}

// cycleKey produces a rotation-invariant key for a cycle.
func cycleKey(cyc []string) string {
	min := 0
	for i := range cyc {
		if cyc[i] < cyc[min] {
			min = i
		}
	}
	rot := append(append([]string(nil), cyc[min:]...), cyc[:min]...)
	return strings.Join(rot, ">")
}
