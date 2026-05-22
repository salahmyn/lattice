package views

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/salahmyn/lattice/pkg/lattice/schema"
	"github.com/salahmyn/lattice/pkg/lattice/workspace"
)

// RenderC4 renders a C4 architecture view as Markdown with Mermaid C4
// diagrams: System Context (people + external systems from context.yaml),
// Container (code roots), and Component (top-level feature groups).
func RenderC4(ws *workspace.Workspace, kg schema.KnowledgeGraph) string {
	m := loadC4Model(ws, kg)
	var b strings.Builder
	b.WriteString("# C4 Architecture View\n\n")
	b.WriteString(fmt.Sprintf("_System: **%s** — generated from lattice.json._\n\n", m.system))
	b.WriteString("## System Context diagram\n\n```mermaid\n")
	b.WriteString(m.contextDiagram())
	b.WriteString("```\n\n## Container diagram\n\n```mermaid\n")
	b.WriteString(m.containerDiagram())
	b.WriteString("```\n\n## Component diagram\n\n```mermaid\n")
	b.WriteString(m.componentDiagram())
	b.WriteString("```\n")
	return b.String()
}

// loadC4Model reads context.yaml and builds the C4 model.
func loadC4Model(ws *workspace.Workspace, kg schema.KnowledgeGraph) c4Model {
	ctx, _ := schema.LoadContext(ws.ContextPath())
	return buildC4Model(ws, kg, ctx)
}

// c4Model is the resolved C4 model derived from the knowledge graph and the
// hand-authored architecture context.
type c4Model struct {
	system      string
	description string
	containers  []c4Container
	components  []c4Component
	actors      []schema.Actor
	externals   []schema.ExternalSystem
	eventExts   []string // external event producers inferred from surfaces
	rels        []c4Rel  // component-level relationships
}

type c4Container struct {
	name      string
	langs     []string
	nFeatures int
}

type c4Component struct {
	name      string // top-level feature-group id
	container string
	langs     []string
	nFeatures int
}

type c4Rel struct {
	from, to, label string
}

// buildC4Model derives the C4 model from the workspace, knowledge graph, and
// architecture context.
func buildC4Model(ws *workspace.Workspace, kg schema.KnowledgeGraph, ctx schema.ArchitectureContext) c4Model {
	m := c4Model{system: systemName(ws), actors: ctx.Actors, externals: ctx.ExternalSystems}
	if ctx.System != "" {
		m.system = ctx.System
	}
	m.description = ctx.Description

	roots := ws.CodeRoots
	singleRoot := len(roots) <= 1
	containerOfFile := func(file string) string {
		if singleRoot {
			return m.system
		}
		if i := strings.Index(filepath.ToSlash(file), "/"); i > 0 {
			return file[:i]
		}
		return roots[0].Name
	}

	type comp struct {
		container string
		langs     map[string]bool
		features  map[string]bool
	}
	comps := map[string]*comp{}
	getComp := func(name string) *comp {
		if c, ok := comps[name]; ok {
			return c
		}
		c := &comp{langs: map[string]bool{}, features: map[string]bool{}}
		comps[name] = c
		return c
	}

	for _, f := range kg.Features {
		c := getComp(c4TopLevel(f.ID))
		c.features[f.ID] = true
		for _, impl := range f.Implementations {
			c.langs[impl.Language] = true
			if c.container == "" {
				c.container = containerOfFile(impl.File)
			}
		}
	}
	for _, c := range comps {
		if c.container == "" {
			c.container = m.system
		}
	}

	names := make([]string, 0, len(comps))
	for name := range comps {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		c := comps[name]
		m.components = append(m.components, c4Component{
			name: name, container: c.container,
			langs: sortedKeys(c.langs), nFeatures: len(c.features),
		})
	}

	containers := map[string]*c4Container{}
	for _, comp := range m.components {
		cc := containers[comp.container]
		if cc == nil {
			cc = &c4Container{name: comp.container}
			containers[comp.container] = cc
		}
		cc.nFeatures += comp.nFeatures
		langSet := map[string]bool{}
		for _, l := range cc.langs {
			langSet[l] = true
		}
		for _, l := range comp.langs {
			langSet[l] = true
		}
		cc.langs = sortedKeys(langSet)
	}
	cnames := make([]string, 0, len(containers))
	for name := range containers {
		cnames = append(cnames, name)
	}
	sort.Strings(cnames)
	for _, name := range cnames {
		m.containers = append(m.containers, *containers[name])
	}

	m.rels, m.eventExts = c4Relationships(kg)
	return m
}

// c4Relationships derives component relationships from feature dependencies
// and surface emit/consume pairs, plus external event producers for events
// consumed but never emitted.
func c4Relationships(kg schema.KnowledgeGraph) ([]c4Rel, []string) {
	seen := map[string]bool{}
	var rels []c4Rel
	add := func(from, to, label string) {
		if from == to || from == "" || to == "" {
			return
		}
		key := from + "|" + to + "|" + label
		if seen[key] {
			return
		}
		seen[key] = true
		rels = append(rels, c4Rel{from: from, to: to, label: label})
	}

	for _, f := range kg.Features {
		for _, dep := range f.DependsOn {
			add(c4TopLevel(f.ID), c4TopLevel(dep), "depends on")
		}
	}

	emitters := map[string][]string{}
	consumers := map[string][]string{}
	for _, f := range kg.Features {
		comp := c4TopLevel(f.ID)
		for _, s := range f.Surface {
			switch s.Type {
			case schema.SurfaceEventEmit:
				emitters[s.Name] = append(emitters[s.Name], comp)
			case schema.SurfaceEventConsume:
				consumers[s.Name] = append(consumers[s.Name], comp)
			}
		}
	}
	extSet := map[string]bool{}
	for event, cons := range consumers {
		emts := emitters[event]
		if len(emts) == 0 {
			extSet[event] = true
			continue
		}
		for _, e := range emts {
			for _, c := range cons {
				add(e, c, event)
			}
		}
	}
	externals := make([]string, 0, len(extSet))
	for e := range extSet {
		externals = append(externals, e)
	}
	sort.Strings(externals)
	return rels, externals
}

// contextDiagram renders the Mermaid C4Context diagram (Level 1).
func (m c4Model) contextDiagram() string {
	var b strings.Builder
	b.WriteString("C4Context\n")
	b.WriteString(fmt.Sprintf("    title System Context — %s\n", m.system))
	for _, a := range m.actors {
		b.WriteString(fmt.Sprintf("    Person(%s, \"%s\", \"%s\")\n",
			alias("p", a.ID), a.Name, a.Description))
	}
	desc := m.description
	if desc == "" {
		desc = "The software system."
	}
	b.WriteString(fmt.Sprintf("    System(sys, \"%s\", \"%s\")\n", m.system, desc))
	for _, e := range m.externals {
		b.WriteString(fmt.Sprintf("    System_Ext(%s, \"%s\", \"%s\")\n",
			alias("ext", e.ID), e.Name, e.Description))
	}
	for _, a := range m.actors {
		b.WriteString(fmt.Sprintf("    Rel(%s, sys, \"uses\")\n", alias("p", a.ID)))
	}
	for _, e := range m.externals {
		if len(e.UsedBy) > 0 {
			b.WriteString(fmt.Sprintf("    Rel(sys, %s, \"integrates with\")\n", alias("ext", e.ID)))
		}
		if len(e.Uses) > 0 {
			b.WriteString(fmt.Sprintf("    Rel(%s, sys, \"calls\")\n", alias("ext", e.ID)))
		}
		if len(e.UsedBy) == 0 && len(e.Uses) == 0 {
			b.WriteString(fmt.Sprintf("    Rel(sys, %s, \"integrates with\")\n", alias("ext", e.ID)))
		}
	}
	return b.String()
}

// containerDiagram renders the Mermaid C4Container diagram (Level 2).
func (m c4Model) containerDiagram() string {
	var b strings.Builder
	b.WriteString("C4Container\n")
	b.WriteString(fmt.Sprintf("    title Container diagram — %s\n", m.system))
	b.WriteString(fmt.Sprintf("    System_Boundary(sys, \"%s\") {\n", m.system))
	for _, c := range m.containers {
		b.WriteString(fmt.Sprintf("        Container(%s, \"%s\", \"%s\", \"%d feature(s)\")\n",
			alias("c", c.name), c.name, techOf(c.langs), c.nFeatures))
	}
	b.WriteString("    }\n")
	for _, e := range m.externals {
		b.WriteString(fmt.Sprintf("    System_Ext(%s, \"%s\", \"%s\")\n",
			alias("ext", e.ID), e.Name, e.Description))
	}
	compContainer := map[string]string{}
	for _, comp := range m.components {
		compContainer[comp.name] = comp.container
	}
	seen := map[string]bool{}
	for _, r := range m.rels {
		from, to := compContainer[r.from], compContainer[r.to]
		if from == "" || to == "" || from == to {
			continue
		}
		key := from + "|" + to
		if seen[key] {
			continue
		}
		seen[key] = true
		b.WriteString(fmt.Sprintf("    Rel(%s, %s, \"integrates with\")\n",
			alias("c", from), alias("c", to)))
	}
	return b.String()
}

// componentDiagram renders the Mermaid C4Component diagram (Level 3).
func (m c4Model) componentDiagram() string {
	var b strings.Builder
	b.WriteString("C4Component\n")
	b.WriteString(fmt.Sprintf("    title Component diagram — %s\n", m.system))

	byContainer := map[string][]c4Component{}
	for _, c := range m.components {
		byContainer[c.container] = append(byContainer[c.container], c)
	}
	containers := make([]string, 0, len(byContainer))
	for name := range byContainer {
		containers = append(containers, name)
	}
	sort.Strings(containers)
	for _, cont := range containers {
		b.WriteString(fmt.Sprintf("    Container_Boundary(%s, \"%s\") {\n", alias("b", cont), cont))
		for _, comp := range byContainer[cont] {
			b.WriteString(fmt.Sprintf("        Component(%s, \"%s\", \"%s\", \"%d feature(s)\")\n",
				alias("cmp", comp.name), comp.name, techOf(comp.langs), comp.nFeatures))
		}
		b.WriteString("    }\n")
	}
	for _, e := range m.eventExts {
		b.WriteString(fmt.Sprintf("    System_Ext(%s, \"%s\", \"External event producer\")\n",
			alias("ext", e), "External: "+e))
	}
	for _, r := range m.rels {
		b.WriteString(fmt.Sprintf("    Rel(%s, %s, \"%s\")\n",
			alias("cmp", r.from), alias("cmp", r.to), r.label))
	}
	return b.String()
}

// --- helpers ---

// c4TopLevel returns the first dot-separated segment of a feature id.
func c4TopLevel(featureID string) string {
	if i := strings.Index(featureID, "."); i > 0 {
		return featureID[:i]
	}
	return featureID
}

// systemName derives a display name for the software system.
func systemName(ws *workspace.Workspace) string {
	name := filepath.Base(filepath.Dir(ws.LatticeDir))
	if ws.Mode == workspace.ModeStandalone {
		name = filepath.Base(ws.LatticeDir)
	}
	if name == "" || name == "." || name == string(filepath.Separator) {
		return "System"
	}
	return name
}

// alias produces a Mermaid-safe identifier from a prefix and a name.
func alias(prefix, name string) string {
	var b strings.Builder
	b.WriteString(prefix)
	b.WriteString("_")
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else {
			b.WriteRune('_')
		}
	}
	return b.String()
}

// techOf summarizes the technologies (languages) of a node.
func techOf(langs []string) string {
	if len(langs) == 0 {
		return "n/a"
	}
	return strings.Join(langs, ", ")
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
