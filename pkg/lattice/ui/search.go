package ui

import (
	"context"
	"net/http"
	"sort"
	"strings"

	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

// SearchHit is one search result, lean enough that 50 of them fit in a
// dropdown without overflowing.
type SearchHit struct {
	Type  string `json:"type"`  // feature | capability | invariant | entry_point | symbol
	ID    string `json:"id"`    // owner ID (feature/EP id, or feature:capability)
	Label string `json:"label"` // human-readable summary
	Href  string `json:"href"`  // page that opens this hit
	Score int    `json:"score"` // higher = better match
}

// Search runs a substring search across every artefact type. Case-
// insensitive; exact-id matches bubble to the top, then prefix matches,
// then substring matches. The result set is capped at 50 — the UI is a
// dropdown, not a full search engine.
func Search(kg schema.KnowledgeGraph, q string) []SearchHit {
	q = strings.TrimSpace(q)
	if q == "" {
		return nil
	}
	lower := strings.ToLower(q)
	var hits []SearchHit
	push := func(h SearchHit) { hits = append(hits, h) }

	for _, f := range kg.Features {
		if s := scoreMatch(f.ID, lower); s > 0 {
			push(SearchHit{Type: "feature", ID: f.ID, Label: f.ID, Href: "/features/" + f.ID, Score: s + 5})
		} else if s := scoreMatch(string(f.Purpose), lower); s > 0 {
			push(SearchHit{Type: "feature", ID: f.ID, Label: f.ID + " — " + truncate(string(f.Purpose), 60), Href: "/features/" + f.ID, Score: s})
		}
		for _, c := range f.Capabilities {
			if s := scoreMatch(c.ID, lower); s > 0 {
				push(SearchHit{
					Type: "capability", ID: f.ID + ":" + c.ID,
					Label: f.ID + " · " + c.ID, Href: "/features/" + f.ID, Score: s + 3,
				})
			} else if s := scoreMatch(string(c.Summary), lower); s > 0 {
				push(SearchHit{
					Type: "capability", ID: f.ID + ":" + c.ID,
					Label: f.ID + " · " + c.ID + " — " + truncate(string(c.Summary), 50),
					Href:  "/features/" + f.ID, Score: s,
				})
			}
		}
		for _, inv := range f.Invariants {
			if s := scoreMatch(inv.ID+" "+string(inv.Statement), lower); s > 0 {
				push(SearchHit{
					Type: "invariant", ID: f.ID + ":" + inv.ID,
					Label: f.ID + " · " + inv.ID + " — " + truncate(string(inv.Statement), 50),
					Href:  "/features/" + f.ID, Score: s + 2,
				})
			}
		}
	}
	for _, ep := range kg.EntryPoints {
		if s := scoreMatch(ep.ID, lower); s > 0 {
			push(SearchHit{Type: "entry_point", ID: ep.ID, Label: ep.ID, Href: "/entry-points/" + ep.ID, Score: s + 5})
		} else if s := scoreMatch(ep.Handler.Symbol+" "+ep.Trigger.Path+" "+ep.Trigger.Command+" "+ep.Trigger.Queue, lower); s > 0 {
			push(SearchHit{
				Type: "entry_point", ID: ep.ID,
				Label: ep.Kind + " " + triggerLabel(ep), Href: "/entry-points/" + ep.ID, Score: s,
			})
		}
	}
	for _, sym := range kg.Symbols {
		if s := scoreMatch(sym.FQN, lower); s > 0 && sym.Feature != "" {
			push(SearchHit{
				Type: "symbol", ID: sym.FQN, Label: sym.FQN + " → " + sym.Feature,
				Href: "/features/" + sym.Feature, Score: s,
			})
		}
	}

	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].Score != hits[j].Score {
			return hits[i].Score > hits[j].Score
		}
		return hits[i].Label < hits[j].Label
	})
	if len(hits) > 50 {
		hits = hits[:50]
	}
	return hits
}

// scoreMatch awards a higher score for exact > prefix > substring.
// Zero means no match.
func scoreMatch(haystack, needle string) int {
	if haystack == "" || needle == "" {
		return 0
	}
	h := strings.ToLower(haystack)
	switch {
	case h == needle:
		return 100
	case strings.HasPrefix(h, needle):
		return 50
	case strings.Contains(h, needle):
		return 20
	}
	return 0
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func triggerLabel(ep schema.EntryPoint) string {
	switch ep.Kind {
	case schema.EntryPointKindHTTP:
		return ep.Trigger.Method + " " + ep.Trigger.Path
	case schema.EntryPointKindCron:
		return ep.Trigger.Schedule
	case schema.EntryPointKindCLI:
		return ep.Trigger.Command
	case schema.EntryPointKindQueue:
		return ep.Trigger.Queue
	}
	return ep.ID
}

// --- HTTP wiring ---

func (s *Server) apiSearch(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	kg, err := s.graph(r.Context())
	if err != nil {
		writeJSONError(w, err, http.StatusInternalServerError)
		return
	}
	writeJSON(w, Search(kg, q))
}

func (s *Server) pageSearch(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	var hits []SearchHit
	if q != "" {
		kg, err := s.graphSafe(r.Context())
		if err == nil {
			hits = Search(kg, q)
		}
	}
	s.render(w, "search.html", pageData{
		Title:    "Search",
		Active:   "search",
		JSONHref: "/api/v1/search?q=" + q,
		Breadcrumbs: []crumb{{Label: "Overview", Href: "/"}, {Label: "Search"}},
		Body:     map[string]interface{}{"Query": q, "Hits": hits},
	})
}

// graphSafe is graph() that swallows errors — used by pages that should
// still render a page even when the graph is missing (the search page
// without a built graph shouldn't 500, it should show "no results").
func (s *Server) graphSafe(ctx context.Context) (schema.KnowledgeGraph, error) {
	return s.graph(ctx)
}
