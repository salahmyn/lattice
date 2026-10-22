package blocks

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

// Proposal is an initiative drawn over the graph, with its evidence when it has any.
type Proposal struct {
	Initiative schema.Initiative
	Evidence   *Evidence
}

var routeVar = regexp.MustCompile(`\{[^}]*\}`)

// normRoute reduces a request path, or a route's "METHOD path", to a comparable
// form: no method or query, every variable as {}, no outer slashes, and the
// first matching prefix removed.
func normRoute(p string, prefixes []string) string {
	if i := strings.IndexByte(p, ' '); i >= 0 {
		p = p[i+1:]
	}
	p = routeVar.ReplaceAllString(p, "{}") // before the query cut: {x?} is a variable, not a query
	if i := strings.IndexByte(p, '?'); i >= 0 {
		p = p[:i]
	}
	p = strings.Trim(p, "/")
	for _, x := range prefixes {
		if strings.HasPrefix(p, x) {
			return p[len(x):]
		}
	}
	return p
}

type routeRef struct{ path, id string }

// routesFor returns the route blocks a request path reaches: the same path, or
// one with more variable segments after it.
func routesFor(p string, routes map[string][]routeRef, prefixes map[string][]string) []string {
	var out []string
	for _, src := range sortedKeys(routes) {
		n := normRoute(p, prefixes[src])
		for _, r := range routes[src] {
			if r.path == n || strings.HasPrefix(r.path, n+"/{}") {
				out = append(out, r.id)
			}
		}
	}
	return out
}

// attachProposals draws each initiative and its evidence: the proposal's own
// blocks under init:<id>, a verdict overlay on every block the evidence judges,
// and the edges from gaps, payload differences and the package's requests.
func (g *graph) attachProposals(ps []Proposal, srcs []SourceInput) {
	routes, prefixes := map[string][]routeRef{}, map[string][]string{}
	keys := map[string]map[string]string{}
	for i, im := range g.sources {
		id := srcs[i].Info.ID
		keys[id] = im.keys
		prefixes[id] = srcs[i].Info.RoutePrefixes
		for _, k := range sortedKeys(im.blocks) {
			if b := im.blocks[k]; b.Kind == "route" && b.Attrs["http"] != "" {
				routes[id] = append(routes[id], routeRef{normRoute(b.Attrs["http"], prefixes[id]), k})
			}
		}
	}
	inits := map[string]bool{}
	for _, p := range ps {
		inits[p.Initiative.ID] = true
	}
	for _, p := range ps {
		if p.Evidence != nil && !inits[p.Evidence.ID] {
			g.warnf("evidence %s has no initiative; skipped", p.Evidence.ID)
			continue
		}
		if p.Initiative.ID != "" {
			g.attachProposal(p, keys, routes, prefixes)
		}
	}
}

func (g *graph) attachProposal(p Proposal, keys map[string]map[string]string, routes map[string][]routeRef, prefixes map[string][]string) {
	in, ev := p.Initiative, p.Evidence
	if ev == nil {
		ev = &Evidence{}
	}
	key := "init:" + in.ID
	human := schema.Provenance{Kind: schema.ProvenanceHuman, Source: "initiative:" + in.ID}
	prov := schema.Provenance{Kind: schema.ProvenanceAdapter, Source: "evidence:" + in.ID}
	add := func(id, kind, parent, title, desc string, attrs map[string]string, pv schema.Provenance) *schema.Block {
		for k, v := range attrs {
			if v == "" {
				delete(attrs, k)
			}
		}
		if len(attrs) == 0 {
			attrs = nil
		}
		if g.blocks[id] != nil {
			g.warnf("initiative %s: block %s is already defined; the proposal's block replaces it", in.ID, id)
		}
		b := &schema.Block{ID: id, Kind: kind, Parent: parent, Title: title, Description: desc,
			Truth: schema.TruthDeclared, Provenance: pv, Attrs: attrs, Initiative: in.ID}
		g.blocks[id] = b
		return b
	}
	m := in.Map
	if m == nil {
		m = &schema.InitiativeMap{}
	}
	title := m.System.Title
	if title == "" {
		title = in.Title
	}
	if title == "" {
		title = in.ID
	}
	pkg := ev.Package
	if pkg == nil {
		pkg = &EvidencePackage{}
	}
	sys := add(key, "system", "", title, m.System.Description,
		map[string]string{"technology": strings.TrimSpace("proposed · " + pkg.Name + " " + pkg.Version),
			"status": string(in.Status), "decision": m.Decision, "flag": m.Flag}, human)

	if p.Evidence != nil && keys[ev.Source] == nil {
		g.warnf("evidence %s: source %q is not a workspace source; its ids resolve through aliases only", in.ID, ev.Source)
	}
	resolve := func(id string) (string, bool) {
		if k, ok := keys[ev.Source][id]; ok {
			return k, true
		}
		if k := g.resolve(id); g.blocks[k] != nil && g.blocks[k].Initiative == "" {
			return k, true
		}
		return "", false
	}

	if len(pkg.Components) > 0 {
		gk := key + ":components"
		add(gk, "group", key, "Components", fmt.Sprintf("Components in %s/ at %s, extracted from source.", pkg.Dir, short(pkg.Commit)),
			map[string]string{"technology": fmt.Sprintf("%d components", len(pkg.Components))}, prov)
		for _, c := range pkg.Components {
			add(gk+":"+c.Tag, "component", gk, "<"+c.Tag+">", strings.Trim(pkg.Dir+"/"+c.File, "/"),
				map[string]string{"props": strings.Join(c.Props, ", "), "events": strings.Join(c.Events, ", "),
					"methods": strings.Join(c.Methods, ", ")}, prov)
		}
	}
	if len(pkg.Modules) > 0 {
		gk, n := key+":modules", 0
		for _, ms := range pkg.Modules {
			n += len(ms.Modules)
		}
		add(gk, "group", key, "Modules", "The package's modules, grouped by concern.",
			map[string]string{"technology": fmt.Sprintf("%d modules", n)}, prov)
		for _, ms := range pkg.Modules {
			add(gk+":"+ms.Concern, "module", gk, ms.Concern, "",
				map[string]string{"technology": plural(len(ms.Modules), "module"), "modules": strings.Join(ms.Modules, " ")}, prov)
		}
	}

	// verdicts: a block's overlay is the top-ranked class among its cases
	rank, classTitle, replaces := map[string]int{}, map[string]string{}, map[string]bool{}
	for i, c := range ev.Classes {
		rank[c.ID], classTitle[c.ID], replaces[c.ID] = i, c.Title, c.Replaces
	}
	counts := map[string]int{}
	cases := map[string][]schema.OverlayCase{}
	var unmapped []string
	for _, v := range ev.Verdicts {
		counts[v.Class]++
		c := schema.OverlayCase{Case: v.Case, ID: v.Block, Class: v.Class, Requirement: v.Requirement, Evidence: v.Evidence, Notes: v.Notes}
		k, ok := resolve(v.Block)
		if !ok {
			unmapped = append(unmapped, v.Block)
			continue
		}
		cases[k] = append(cases[k], c)
	}

	// gaps: the initiative says which block it; the evidence says which are low
	blocking := map[string]bool{}
	for _, c := range m.Blocking {
		blocking[c] = true
	}
	nBlocking := 0
	if len(ev.Gaps) > 0 {
		for _, gp := range ev.Gaps {
			if blocking[gp.Code] {
				nBlocking++
			}
		}
		gk := key + ":gaps"
		add(gk, "group", key, "Gaps and issues", "From the gap analysis.",
			map[string]string{"technology": fmt.Sprintf("%d gaps · %d blocking", len(ev.Gaps), nBlocking)}, prov)
		for _, gp := range ev.Gaps {
			sev := "gap"
			switch {
			case blocking[gp.Code]:
				sev = "blocking"
			case gp.Low:
				sev = "low"
			}
			var cites, lost []string
			for _, x := range gp.Cites {
				if k, ok := resolve(x); ok {
					cites = append(cites, k)
				} else {
					lost = append(lost, x)
				}
			}
			tech := sev
			if len(gp.Owners) > 0 {
				tech += " · owner " + strings.Join(gp.Owners, "/")
			}
			id := gk + ":" + gp.Code
			add(id, "gap", gk, gp.Code, gp.Summary, map[string]string{"technology": tech, "severity": sev,
				"owners": strings.Join(gp.Owners, " "), "case": gp.Case, "cites": strings.Join(cites, " "),
				"unmapped_cites": strings.Join(lost, " ")}, prov)
			for _, c := range cites {
				for i := range cases[c] {
					cases[c][i].Gaps = append(cases[c][i].Gaps, gp.Code)
				}
				g.edge(id, c, "gap", sev, nil, prov)
			}
		}
	}

	for _, k := range sortedKeys(cases) {
		cs := cases[k]
		top := cs[0].Class
		for _, c := range cs[1:] {
			if rank[c.Class] < rank[top] {
				top = c.Class
			}
		}
		b := g.blocks[k]
		if b.Overlays == nil {
			b.Overlays = map[string]schema.Overlay{}
		}
		b.Overlays[in.ID] = schema.Overlay{Verdict: top, Title: classTitle[top], Cases: cs}
		if replaces[top] {
			g.edge(key, k, "replaces", classTitle[top], nil, prov)
		}
	}

	// payload differences, one block per endpoint
	byEP := map[string][]PayloadDiff{}
	sevs := map[string]int{}
	for _, r := range ev.Payload {
		byEP[r.Endpoint] = append(byEP[r.Endpoint], r)
		sevs[r.Severity]++
	}
	if len(byEP) > 0 {
		gk := key + ":payload"
		add(gk, "group", key, "Request payload differences", "What the package sends compared with today, per endpoint.",
			map[string]string{"technology": tally(sevs)}, prov)
		for i, ep := range sortedKeys(byEP) {
			rows, sv := byEP[ep], map[string]int{}
			var fields, diffs []string
			for _, r := range rows {
				sv[r.Severity]++
				fields = append(fields, r.Field)
				diffs = append(diffs, fmt.Sprintf("[%s] %s: %s", r.Severity, r.Field, r.Difference))
			}
			id := gk + ":" + strconv.Itoa(i)
			add(id, "difference", gk, ep, truncate(strings.Join(fields, "; "), 300),
				map[string]string{"technology": tally(sv), "differences": strings.Join(diffs, " | ")}, prov)
			seen := map[string]bool{}
			for _, r := range rows {
				for _, pth := range r.Paths {
					if seen[pth] {
						continue
					}
					seen[pth] = true
					for _, rt := range routesFor(pth, routes, prefixes) {
						g.edge(id, rt, "api", "differs", nil, prov)
					}
				}
			}
		}
	}

	for _, pth := range pkg.Paths {
		for _, rt := range routesFor(pth, routes, prefixes) {
			g.edge(key, rt, "api", "calls "+normRoute(pth, prefixes[routeSource(rt, routes)]), nil, prov)
		}
	}
	for _, x := range m.Externals {
		if g.blocks[x.To] == nil {
			g.warnf("initiative %s: external %q is not a block", in.ID, x.To)
		}
		g.edge(key, x.To, x.Kind, x.Label, nil, human)
	}
	if h := m.HostBlock; h != "" {
		if g.blocks[h] == nil {
			g.warnf("initiative %s: host block %q is not a block", in.ID, h)
		}
		label := "mounts the proposal"
		if m.Flag != "" {
			label += " (" + m.Flag + ")"
		}
		g.edge(h, key, "uses", label, nil, human)
	}

	var vs []string
	for _, c := range ev.Classes {
		if counts[c.ID] > 0 {
			vs = append(vs, fmt.Sprintf("%s %d", c.ID, counts[c.ID]))
		}
	}
	var codes []string
	for _, gp := range ev.Gaps {
		if blocking[gp.Code] {
			codes = append(codes, gp.Code)
		}
	}
	for k, v := range map[string]string{"verdicts": strings.Join(vs, " · "), "blocking": strings.Join(codes, " "),
		"payload": tally(sevs), "unmapped": strings.Join(unmapped, " ")} {
		if v != "" {
			sys.Attrs[k] = v
		}
	}
	if len(ev.Gaps) > 0 {
		sys.Attrs["gaps"] = strconv.Itoa(len(ev.Gaps))
	}
}

// routeSource names the source whose routes hold id.
func routeSource(id string, routes map[string][]routeRef) string {
	for src, rs := range routes {
		for _, r := range rs {
			if r.id == id {
				return src
			}
		}
	}
	return ""
}

// tally writes counts as "<n> <name>", most common first, ties by name.
func tally(m map[string]int) string {
	ks := sortedKeys(m)
	sort.SliceStable(ks, func(i, j int) bool { return m[ks[i]] > m[ks[j]] })
	out := make([]string, len(ks))
	for i, k := range ks {
		out[i] = fmt.Sprintf("%d %s", m[k], k)
	}
	return strings.Join(out, " · ")
}

func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return fmt.Sprintf("%d %ss", n, word)
}

func truncate(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}
