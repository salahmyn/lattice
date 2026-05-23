package ui

import (
	"bytes"
	"net/http"
	"sort"
	"strings"

	"github.com/salahmyn/lattice/pkg/lattice/schema"
	"github.com/salahmyn/lattice/pkg/lattice/views"
)

// pageData is what every template receives via the layout. Title and
// Breadcrumbs vary per page; Active is the nav item to highlight; Body
// is the rendered body block name.
type pageData struct {
	Title       string
	Active      string
	Breadcrumbs []crumb
	Body        interface{}
	JSONHref    string // canonical URL of the same content as JSON
}

type crumb struct {
	Label string
	Href  string // empty for current page
}

func (s *Server) render(w http.ResponseWriter, name string, data pageData) {
	tpls, err := s.templates()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	tpl, ok := tpls[name]
	if !ok {
		http.Error(w, "unknown template: "+name, http.StatusInternalServerError)
		return
	}
	// Execute into a buffer first so a template error returns a clean 500
	// rather than half a page plus a superfluous WriteHeader log line.
	var buf bytes.Buffer
	if err := tpl.ExecuteTemplate(&buf, name, data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = buf.WriteTo(w)
}

// --- Pages ---

func (s *Server) pageOverview(w http.ResponseWriter, r *http.Request) {
	kg, err := s.graph(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	byKind := map[string]int{}
	for _, ep := range kg.EntryPoints {
		byKind[ep.Kind]++
	}
	s.render(w, "overview.html", pageData{
		Title:    "Overview",
		Active:   "overview",
		JSONHref: "/api/v1/overview",
		Body: map[string]interface{}{
			"Workspace":  s.ws,
			"KG":         kg,
			"EPsByKind":  byKind,
			"OrderedKinds": orderedKinds(byKind),
		},
	})
}

func (s *Server) pageFeatures(w http.ResponseWriter, r *http.Request) {
	kg, err := s.graph(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	features := append([]schema.Manifest(nil), kg.Features...)
	sort.Slice(features, func(i, j int) bool { return features[i].ID < features[j].ID })
	s.render(w, "features.html", pageData{
		Title:    "Features",
		Active:   "features",
		JSONHref: "/api/v1/features",
		Breadcrumbs: []crumb{{Label: "Overview", Href: "/"}, {Label: "Features"}},
		Body:     features,
	})
}

func (s *Server) pageFeature(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	kg, err := s.graph(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	var feat schema.Manifest
	found := false
	for _, m := range kg.Features {
		if m.ID == id {
			feat = m
			found = true
			break
		}
	}
	if !found {
		http.NotFound(w, r)
		return
	}
	reached := featuresReachedBy(id, kg.EntryPoints)
	s.render(w, "feature.html", pageData{
		Title:    feat.ID,
		Active:   "features",
		JSONHref: "/api/v1/features/" + id,
		Breadcrumbs: []crumb{
			{Label: "Overview", Href: "/"},
			{Label: "Features", Href: "/features"},
			{Label: feat.ID},
		},
		Body: map[string]interface{}{
			"Feature":   feat,
			"ReachedBy": reached,
		},
	})
}

func (s *Server) pageEntryPoints(w http.ResponseWriter, r *http.Request) {
	kg, err := s.graph(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	byKind := map[string][]schema.EntryPoint{}
	for _, ep := range kg.EntryPoints {
		byKind[ep.Kind] = append(byKind[ep.Kind], ep)
	}
	for k := range byKind {
		sort.Slice(byKind[k], func(i, j int) bool { return byKind[k][i].ID < byKind[k][j].ID })
	}
	s.render(w, "entrypoints.html", pageData{
		Title:    "Entry points",
		Active:   "entry-points",
		JSONHref: "/api/v1/entry-points",
		Breadcrumbs: []crumb{{Label: "Overview", Href: "/"}, {Label: "Entry points"}},
		Body: map[string]interface{}{
			"ByKind":       byKind,
			"OrderedKinds": orderedKinds(countsByKind(kg.EntryPoints)),
		},
	})
}

func (s *Server) pageEntryPoint(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	kg, err := s.graph(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	var ep schema.EntryPoint
	found := false
	for _, x := range kg.EntryPoints {
		if x.ID == id {
			ep = x
			found = true
			break
		}
	}
	if !found {
		http.NotFound(w, r)
		return
	}
	s.render(w, "entrypoint.html", pageData{
		Title:    ep.ID,
		Active:   "entry-points",
		JSONHref: "/api/v1/entry-points/" + id,
		Breadcrumbs: []crumb{
			{Label: "Overview", Href: "/"},
			{Label: "Entry points", Href: "/entry-points"},
			{Label: ep.ID},
		},
		Body: ep,
	})
}

func (s *Server) pageFlow(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	kg, err := s.graph(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	mermaid := views.RenderFlows(kg, id)
	// Strip the markdown wrapper so the template can embed the raw
	// fenced block however it likes.
	mermaid = stripMermaidFence(mermaid)
	s.render(w, "flow.html", pageData{
		Title:    "Flow: " + id,
		Active:   "entry-points",
		JSONHref: "/api/v1/entry-points/" + id,
		Breadcrumbs: []crumb{
			{Label: "Overview", Href: "/"},
			{Label: "Entry points", Href: "/entry-points"},
			{Label: id, Href: "/entry-points/" + id},
			{Label: "Flow"},
		},
		Body: map[string]interface{}{"ID": id, "Mermaid": mermaid},
	})
}

// stripMermaidFence pulls the raw flowchart block out of the markdown
// the CLI renderer emits so the template can wrap it with the mermaid
// runtime element directly.
func stripMermaidFence(md string) string {
	const start = "```mermaid\n"
	const end = "\n```"
	i := strings.Index(md, start)
	if i < 0 {
		return md
	}
	body := md[i+len(start):]
	if j := strings.Index(body, end); j >= 0 {
		body = body[:j]
	}
	return body
}

func countsByKind(eps []schema.EntryPoint) map[string]int {
	out := map[string]int{}
	for _, ep := range eps {
		out[ep.Kind]++
	}
	return out
}

func orderedKinds(byKind map[string]int) []string {
	preferred := []string{
		schema.EntryPointKindHTTP,
		schema.EntryPointKindGRPC,
		schema.EntryPointKindCLI,
		schema.EntryPointKindCron,
		schema.EntryPointKindQueue,
		schema.EntryPointKindWebhook,
		schema.EntryPointKindEventConsumer,
	}
	seen := map[string]bool{}
	out := []string{}
	for _, k := range preferred {
		if _, ok := byKind[k]; ok {
			out = append(out, k)
			seen[k] = true
		}
	}
	for k := range byKind {
		if !seen[k] {
			out = append(out, k)
		}
	}
	return out
}
