package views

import (
	"fmt"
	"sort"
	"strings"

	"github.com/salahmyn/lattice/pkg/lattice/schema"
	"github.com/salahmyn/lattice/pkg/lattice/workspace"
)

// RenderC4Structurizr renders the C4 model as a Structurizr DSL workspace —
// the canonical C4 text format, consumable by the Structurizr tooling for
// proper layout and rendering.
func RenderC4Structurizr(ws *workspace.Workspace, kg schema.KnowledgeGraph) string {
	m := loadC4Model(ws, kg)
	return m.structurizr()
}

// structurizr renders the model as a Structurizr DSL workspace.
func (m c4Model) structurizr() string {
	var b strings.Builder
	desc := m.description
	if desc == "" {
		desc = "Generated from lattice.json."
	}
	b.WriteString(fmt.Sprintf("workspace %q %q {\n\n", m.system, desc))
	b.WriteString("    model {\n")

	// People.
	for _, a := range m.actors {
		b.WriteString(fmt.Sprintf("        %s = person %q %q\n",
			alias("p", a.ID), a.Name, a.Description))
	}

	// The software system, with containers and their components nested.
	byContainer := map[string][]c4Component{}
	for _, c := range m.components {
		byContainer[c.container] = append(byContainer[c.container], c)
	}
	b.WriteString(fmt.Sprintf("        sys = softwareSystem %q %q {\n", m.system, desc))
	for _, c := range m.containers {
		b.WriteString(fmt.Sprintf("            %s = container %q %q %q {\n",
			alias("c", c.name), c.name, fmt.Sprintf("%d feature(s)", c.nFeatures), techOf(c.langs)))
		for _, comp := range byContainer[c.name] {
			b.WriteString(fmt.Sprintf("                %s = component %q %q %q\n",
				alias("cmp", comp.name), comp.name,
				fmt.Sprintf("%d feature(s)", comp.nFeatures), techOf(comp.langs)))
		}
		b.WriteString("            }\n")
	}
	b.WriteString("        }\n")

	// External systems.
	for _, e := range m.externals {
		b.WriteString(fmt.Sprintf("        %s = softwareSystem %q %q {\n",
			alias("ext", e.ID), e.Name, e.Description))
		b.WriteString("            tags \"External\"\n")
		b.WriteString("        }\n")
	}

	// Relationships.
	for _, a := range m.actors {
		b.WriteString(fmt.Sprintf("        %s -> sys \"uses\"\n", alias("p", a.ID)))
	}
	for _, r := range m.rels {
		b.WriteString(fmt.Sprintf("        %s -> %s %q\n",
			alias("cmp", r.from), alias("cmp", r.to), r.label))
	}
	for _, e := range m.externals {
		if len(e.Uses) > 0 {
			b.WriteString(fmt.Sprintf("        %s -> sys \"calls\"\n", alias("ext", e.ID)))
		}
		if len(e.UsedBy) > 0 || len(e.Uses) == 0 {
			b.WriteString(fmt.Sprintf("        sys -> %s \"integrates with\"\n", alias("ext", e.ID)))
		}
	}
	b.WriteString("    }\n\n")

	// Views.
	b.WriteString("    views {\n")
	b.WriteString("        systemContext sys \"Context\" {\n            include *\n            autoLayout lr\n        }\n")
	b.WriteString("        container sys \"Containers\" {\n            include *\n            autoLayout lr\n        }\n")
	conts := make([]string, 0, len(m.containers))
	for _, c := range m.containers {
		conts = append(conts, c.name)
	}
	sort.Strings(conts)
	for _, name := range conts {
		b.WriteString(fmt.Sprintf("        component %s %q {\n            include *\n            autoLayout lr\n        }\n",
			alias("c", name), "Components-"+name))
	}
	b.WriteString("        theme default\n")
	b.WriteString("    }\n}\n")
	return b.String()
}
