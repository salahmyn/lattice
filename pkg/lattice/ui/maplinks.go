package ui

// @module-feature ui

import (
	"context"
	"net/url"
	"sort"

	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

// mapLink is a "View on map" link from a feature, BRD or entry point page to
// the block that stands for it, opened in the map's Focus tab.
type mapLink struct {
	ID    string
	Title string
	Href  string
}

// maxMapLinks bounds the links one page shows; a BRD can reach many features.
const maxMapLinks = 6

// focusHref is the map URL that focuses block id.
func focusHref(id string) string {
	return "/map#tab=focus&id=" + url.QueryEscape(id)
}

// mapLinks returns links to the stored graph's blocks that correspond to a
// page: the blocks whose id is in ids, in that order, then the blocks match
// accepts, by id. No store, an unreadable graph or no match gives no links.
func (s *Server) mapLinks(ctx context.Context, ids []string, match func(schema.Block) bool) []mapLink {
	if s.opts.Store == nil {
		return nil
	}
	s.storeMu.Lock()
	g, err := s.opts.Store.Graph(ctx)
	s.storeMu.Unlock()
	if err != nil {
		return nil
	}
	byID := make(map[string]schema.Block, len(g.Blocks))
	for _, b := range g.Blocks {
		byID[b.ID] = b
	}
	seen := map[string]bool{}
	var out []mapLink
	add := func(b schema.Block) {
		if seen[b.ID] || len(out) >= maxMapLinks {
			return
		}
		seen[b.ID] = true
		t := b.Title
		if t == "" {
			t = b.ID
		}
		out = append(out, mapLink{ID: b.ID, Title: t, Href: focusHref(b.ID)})
	}
	for _, id := range ids {
		if b, ok := byID[id]; ok {
			add(b)
		}
	}
	if match != nil {
		var hits []schema.Block
		for _, b := range g.Blocks {
			if match(b) {
				hits = append(hits, b)
			}
		}
		sort.Slice(hits, func(i, j int) bool { return hits[i].ID < hits[j].ID })
		for _, b := range hits {
			add(b)
		}
	}
	return out
}

// brdMapLinks are a BRD's blocks: the feature blocks that implement it, then
// the blocks the BRD names.
func (s *Server) brdMapLinks(ctx context.Context, b schema.BRD) []mapLink {
	ids := make([]string, 0, len(b.ImplementsVia)+len(b.Blocks))
	for _, f := range b.ImplementsVia {
		ids = append(ids, "feat:"+f)
	}
	ids = append(ids, b.Blocks...)
	return s.mapLinks(ctx, ids, func(x schema.Block) bool { return x.BRD == b.ID })
}
