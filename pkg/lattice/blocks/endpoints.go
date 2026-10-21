package blocks

import (
	"sort"
	"strings"

	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

// EndpointGrouping groups an area's loose blocks by the endpoint whose request
// runs them (a fact: the route at the top of the catalogue parent chain), and the
// endpoints by topic (the first meaningful segment of their path).
type EndpointGrouping struct {
	SkipAreas    []string          // areas left flat, e.g. the routes area itself
	SkipSegments []string          // path segments that never name a topic
	PathPrefixes map[string]string // path prefix -> topic id prefix, e.g. "/api/" -> "api-"
}

// groupByEndpoint adds group, topic and endpoint blocks to im, moves loose blocks
// under them, and returns the request-order pairs ("then" edges) by block id.
func groupByEndpoint(im *imported, src SourceInfo) [][2]string {
	opt := src.Endpoints
	if opt == nil {
		return nil
	}
	var pairs [][2]string
	for _, area := range im.areas {
		if contains(opt.SkipAreas, area) {
			continue
		}
		akey := src.Prefix + ":" + area
		var loose []string
		for id, n := range im.nodes {
			if n.Parent != "" && im.area[id] == area && im.blocks[im.keys[id]].Parent == akey {
				loose = append(loose, id)
			}
		}
		sort.Slice(loose, func(a, b int) bool { return im.order[loose[a]] < im.order[loose[b]] })

		type group struct {
			kind, route string
			ids         []string
		}
		groups := map[string]*group{}
		add := func(key, kind, route, id string) {
			if groups[key] == nil {
				groups[key] = &group{kind: kind, route: route}
			}
			groups[key].ids = append(groups[key].ids, id)
		}
		for _, id := range loose {
			switch r := entryRoute(im, id); {
			case im.nodes[id].Kind == "config":
				add("config", "config", "", id)
			case r != "":
				add("route:"+r, "route", r, id)
			default:
				add("shared", "shared", "", id)
			}
		}
		if len(groups) < 2 && groups["config"] == nil {
			continue
		}

		base := im.blocks[akey]
		mk := func(id, kind, parent, title, desc string) *schema.Block {
			b := &schema.Block{ID: id, Kind: kind, Parent: parent, Title: title, Description: desc,
				Tier: base.Tier, System: base.System, Truth: schema.TruthDeclared, Provenance: base.Provenance}
			im.blocks[id] = b
			return b
		}
		for _, key := range sortedKeys(groups) {
			gr := groups[key]
			var gid string
			switch gr.kind {
			case "config":
				gid = "grp:" + area + ":config"
				mk(gid, "group", akey, "Configuration inputs", "Settings and environment values this area reads.")
			case "shared":
				gid = "grp:" + area + ":shared"
				mk(gid, "group", akey, "Shared logic",
					"Blocks not reached from a single endpoint: called from several places, or from jobs and listeners.")
			default:
				r := im.nodes[gr.route]
				http := r.HTTP.Method + " " + r.HTTP.Path
				t := topicOf(r.HTTP.Path, opt)
				tid := "grp:" + area + ":topic:" + t
				if im.blocks[tid] == nil {
					mk(tid, "topic", akey, topicTitle(t), "Endpoints under /"+t+" handled in this area.")
				}
				gid = "grp:" + area + ":ep:" + gr.route
				mk(gid, "endpoint", tid, http, r.Detail).Attrs = map[string]string{"http": http, "route_id": gr.route}
				// request order: validation gates run before the handler logic
				var ordered []string
				for _, gate := range []bool{true, false} {
					for _, id := range gr.ids {
						if (im.nodes[id].Kind == "gate") == gate {
							ordered = append(ordered, id)
						}
					}
				}
				gr.ids = ordered
				for i := 1; i < len(ordered); i++ {
					pairs = append(pairs, [2]string{im.keys[ordered[i-1]], im.keys[ordered[i]]})
				}
			}
			for _, id := range gr.ids {
				im.blocks[im.keys[id]].Parent = gid
			}
		}
	}
	return pairs
}

// entryRoute returns the catalogue id of the first route (with http) up a node's
// catalogue parent chain, or "".
func entryRoute(im *imported, id string) string {
	seen := map[string]bool{}
	for p := im.nodes[id].Parent; p != "" && !seen[p]; {
		pn, ok := im.nodes[p]
		if !ok {
			return ""
		}
		seen[p] = true
		if pn.Kind == "route" && pn.HTTP != nil {
			return p
		}
		p = pn.Parent
	}
	return ""
}

func topicOf(p string, opt *EndpointGrouping) string {
	t := "other"
	for _, s := range strings.Split(p, "/") {
		if s != "" && !strings.HasPrefix(s, "{") && !contains(opt.SkipSegments, s) {
			t = s
			break
		}
	}
	for _, pre := range sortedKeys(opt.PathPrefixes) {
		if strings.HasPrefix(p, pre) {
			return opt.PathPrefixes[pre] + t
		}
	}
	return t
}

func topicTitle(t string) string {
	s := strings.ReplaceAll(t, "-", " ")
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + strings.ToLower(s[1:])
}
