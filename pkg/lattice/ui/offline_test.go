package ui

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha512"
	"encoding/base64"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/salahmyn/lattice/pkg/lattice/importer"
	"github.com/salahmyn/lattice/pkg/lattice/onboarding"
	"github.com/salahmyn/lattice/pkg/lattice/schema"
	"github.com/salahmyn/lattice/pkg/lattice/workspace"
)

// offlineServer is a server over a small workspace in which every page,
// detail pages included, has something to render.
func offlineServer(t *testing.T) *Server {
	t.Helper()
	dir := t.TempDir()
	ctxYAML := "actors:\n  - id: shopper\n    name: Shopper\n    uses: [orders]\n"
	if err := os.WriteFile(filepath.Join(dir, "context.yaml"), []byte(ctxYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := onboarding.Save(dir, onboarding.State{Step: onboarding.StepConfirmRoots}); err != nil {
		t.Fatal(err)
	}
	ws := &workspace.Workspace{LatticeDir: dir}
	if err := os.MkdirAll(ws.ImportDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	cf := importer.CandidatesFile{Version: 1, Candidates: []importer.Candidate{{
		ID: "cand-orders", Package: "internal/orders", Language: "go",
		Symbols: []string{"orders.Place"}, Files: []string{"internal/orders/place.go"}, Confidence: 0.9,
	}}}
	if err := importer.Write(filepath.Join(ws.ImportDir(), importer.CandidatesFileName), cf); err != nil {
		t.Fatal(err)
	}
	kg := schema.KnowledgeGraph{
		BRDs: []schema.BRD{{ID: "brd.orders", Version: 1, Status: schema.BRDStatus("draft"), Title: "Orders",
			BusinessProblem: "Shoppers place orders.", ImplementsVia: []string{"orders"}}},
		Features: []schema.Manifest{{ID: "orders", Version: 1, Status: schema.StatusProduction, ImplementsBRD: "brd.orders",
			Invariants: []schema.Invariant{{ID: "INV-1", Statement: "an order has a line"}}}},
		EntryPoints: []schema.EntryPoint{{ID: "ep.orders.place", Version: 1, Status: schema.StatusProduction,
			Kind: schema.EntryPointKindHTTP, Trigger: schema.Trigger{Method: "POST", Path: "/orders"},
			Handler: schema.Handler{Symbol: "orders.Place"}, Flow: []schema.FlowStep{{Feature: "orders"}}}},
		Symbols: []schema.GraphSymbol{{FQN: "orders.Place", Feature: "orders", EnforcesInvariants: []string{"INV-1"}}},
		Tests:   []schema.GraphSymbol{{FQN: "orders.TestPlace", Feature: "orders", IsTest: true, Verifies: []string{"INV-1"}}},
	}
	return New(ws, Options{GraphBuilder: func(context.Context) (schema.KnowledgeGraph, error) { return kg, nil }})
}

// getPage serves one request, with optional header name/value pairs.
func getPage(t *testing.T, srv *Server, target string, header ...string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, target, nil)
	for i := 0; i+1 < len(header); i += 2 {
		req.Header.Set(header[i], header[i+1])
	}
	w := httptest.NewRecorder()
	srv.routes().ServeHTTP(w, req)
	return w
}

// serverPages is every server-rendered page and the template it renders.
// The /views page is not a template. The block map (/map), a standalone
// page with its own layout library, is not covered here.
var serverPages = map[string]string{
	"/":                             "overview.html",
	"/brds":                         "brds.html",
	"/brds/brd.orders":              "brd.html",
	"/journeys/brd.orders":          "journey.html",
	"/actors":                       "actors.html",
	"/actors/shopper":               "actor.html",
	"/features":                     "features.html",
	"/features/orders":              "feature.html",
	"/entry-points":                 "entrypoints.html",
	"/entry-points/ep.orders.place": "entrypoint.html",
	"/flows/ep.orders.place":        "flow.html",
	"/coverage":                     "coverage.html",
	"/rtm":                          "rtm.html",
	"/audit":                        "audit.html",
	"/validation":                   "validation.html",
	"/search?q=orders":              "search.html",
	"/import":                       "import.html",
	"/import/cand-orders":           "import-candidate.html",
	"/config":                       "config.html",
	"/onboarding":                   "onboarding.html",
	"/views":                        "",
}

// remoteRef finds a reference that makes the browser fetch from another
// origin: a script src, a stylesheet or preconnect link href, a module
// import, a CSS @import or url(). Plain anchor links are not fetches.
var remoteRef = regexp.MustCompile(`(?is)<script\b[^>]*\bsrc\s*=\s*["']?\s*(?:https?:)?//[^"'\s>]*` +
	`|<link\b[^>]*\bhref\s*=\s*["']?\s*(?:https?:)?//[^"'\s>]*` +
	`|\bimport\s*(?:[\w{}*\s,]+\bfrom\s*)?\(?\s*["'](?:https?:)?//[^"']*` +
	`|@import\s+(?:url\(\s*)?["']?\s*(?:https?:)?//[^"')\s;]*` +
	`|url\(\s*["']?\s*(?:https?:)?//[^"')\s]*`)

// localRef finds what a page or stylesheet loads from its own origin.
var localRef = regexp.MustCompile(`(?is)<script\b[^>]*\bsrc\s*=\s*["']?(/[^/"'\s>][^"'\s>]*)` +
	`|<link\b[^>]*\bhref\s*=\s*["']?(/[^/"'\s>][^"'\s>]*)` +
	`|url\(\s*["']?(/[^/"')\s][^"')\s]*)`)

func localRefs(body string) []string {
	var out []string
	for _, m := range localRef.FindAllStringSubmatch(body, -1) {
		for _, g := range m[1:] {
			if g != "" {
				out = append(out, g)
			}
		}
	}
	return out
}

// Every server-rendered page, and every stylesheet it loads, references
// nothing on another origin, and everything it does load is served.
// @verifies ui:INV-5
func TestPagesLoadNothingFromNetwork(t *testing.T) {
	entries, err := assetsFS.ReadDir("assets/templates")
	if err != nil {
		t.Fatal(err)
	}
	covered := map[string]bool{}
	for _, tpl := range serverPages {
		covered[tpl] = true
	}
	for _, e := range entries {
		if e.Name() != "layout.html" && !covered[e.Name()] {
			t.Errorf("template %s has no page in serverPages: add one so its references are checked", e.Name())
		}
	}

	srv := offlineServer(t)
	checked := map[string]bool{}
	var check func(from, target string)
	check = func(from, target string) {
		if checked[target] {
			return
		}
		checked[target] = true
		w := getPage(t, srv, target)
		if w.Code != http.StatusOK {
			t.Errorf("%s loads %s: status %d", from, target, w.Code)
			return
		}
		ct := w.Header().Get("Content-Type")
		if !strings.HasPrefix(ct, "text/html") && !strings.HasPrefix(ct, "text/css") {
			return
		}
		body := w.Body.String()
		for _, ref := range remoteRef.FindAllString(body, -1) {
			t.Errorf("%s loads from the network: %s", target, ref)
		}
		for _, ref := range localRefs(body) {
			check(target, strings.SplitN(ref, "#", 2)[0])
		}
	}
	targets := make([]string, 0, len(serverPages))
	for p := range serverPages {
		targets = append(targets, p)
	}
	sort.Strings(targets)
	for _, p := range targets {
		check("the server", p)
	}
	for _, want := range []string{"/static/tailwind.css", "/static/styles.css", mermaidPath, "/vendor/fonts/geist-v5-latin.woff2"} {
		if !checked[want] {
			t.Errorf("no page loads %s", want)
		}
	}
}

// The vendored assets are served from the binary with their content types;
// the large ones gzipped when the client accepts it, and the diagram
// renderer is byte for byte the file its integrity hash names.
// @verifies ui:INV-5
func TestVendoredAssetsServed(t *testing.T) {
	srv := offlineServer(t)
	for target, ctype := range map[string]string{
		"/static/tailwind.css":                             "text/css",
		"/static/styles.css":                               "text/css",
		"/vendor/fonts/geist-v5-latin.woff2":               "font/woff2",
		"/vendor/fonts/geist-v5-latin-ext.woff2":           "font/woff2",
		"/vendor/fonts/jetbrains-mono-v24-latin.woff2":     "font/woff2",
		"/vendor/fonts/jetbrains-mono-v24-latin-ext.woff2": "font/woff2",
		"/vendor/fonts/ibm-plex-serif-v20-400-latin.woff2": "font/woff2",
		"/vendor/fonts/OFL-Geist.txt":                      "text/plain",
		"/vendor/mermaid-10.9.8/LICENSE":                   "text/plain",
	} {
		w := getPage(t, srv, target)
		if w.Code != http.StatusOK || !strings.HasPrefix(w.Header().Get("Content-Type"), ctype) {
			t.Errorf("%s = %d %q, want 200 %s", target, w.Code, w.Header().Get("Content-Type"), ctype)
		}
		if ctype == "font/woff2" && !bytes.HasPrefix(w.Body.Bytes(), []byte("wOF2")) {
			t.Errorf("%s is not a WOFF2 file", target)
		}
	}

	page := getPage(t, srv, "/flows/ep.orders.place").Body.String()
	m := regexp.MustCompile(`<script src="` + regexp.QuoteMeta(mermaidPath) + `" integrity="sha384-([^"]+)"`).FindStringSubmatch(page)
	if m == nil {
		t.Fatalf("the flow page does not load %s with an integrity hash", mermaidPath)
	}
	sri := func(b []byte) string { h := sha512.Sum384(b); return base64.StdEncoding.EncodeToString(h[:]) }

	gz := getPage(t, srv, mermaidPath, "Accept-Encoding", "gzip")
	if gz.Code != http.StatusOK || gz.Header().Get("Content-Encoding") != "gzip" ||
		!strings.HasPrefix(gz.Header().Get("Content-Type"), "text/javascript") {
		t.Fatalf("mermaid with gzip = %d %q %q", gz.Code, gz.Header().Get("Content-Encoding"), gz.Header().Get("Content-Type"))
	}
	zr, err := gzip.NewReader(gz.Body)
	if err != nil {
		t.Fatal(err)
	}
	js, err := io.ReadAll(zr)
	if err != nil {
		t.Fatal(err)
	}
	if got := sri(js); got != m[1] {
		t.Errorf("served mermaid has sha384-%s, the page expects sha384-%s", got, m[1])
	}
	plain := getPage(t, srv, mermaidPath)
	if plain.Header().Get("Content-Encoding") != "" || !bytes.Equal(plain.Body.Bytes(), js) {
		t.Errorf("without gzip, mermaid must be sent decompressed (encoding %q, %d bytes)", plain.Header().Get("Content-Encoding"), plain.Body.Len())
	}

	for _, target := range []string{"/vendor/mermaid-10.9.8/mermaid.min.js.gz", "/vendor/fonts", "/vendor/", "/vendor/fonts/missing.woff2"} {
		if w := getPage(t, srv, target); w.Code != http.StatusNotFound {
			t.Errorf("%s = %d, want 404", target, w.Code)
		}
	}
	// Outside the vendored tree: the mux cleans the path and redirects.
	if w := getPage(t, srv, "/vendor/../server.go"); w.Code == http.StatusOK {
		t.Errorf("/vendor/../server.go = 200")
	}
}

// Each vendored file is listed in THIRD_PARTY_NOTICES.md.
func TestVendoredFilesHaveNotices(t *testing.T) {
	notices, err := os.ReadFile(filepath.Join("..", "..", "..", "THIRD_PARTY_NOTICES.md"))
	if err != nil {
		t.Fatal(err)
	}
	err = fs.WalkDir(vendorFS, "assets/vendor", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel := strings.TrimPrefix(p, "assets/vendor/")
		if !bytes.Contains(notices, []byte(rel)) {
			t.Errorf("THIRD_PARTY_NOTICES.md does not list %s", rel)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// hookClasses are class names the templates use as script or test hooks,
// with no style of their own.
var hookClasses = map[string]bool{"crystal": true, "audit-cases": true, "audit-enforcers": true, "root-checkbox": true}

// tailwind.css is generated once, not in the browser: a template that uses a
// utility class the committed stylesheet lacks would render it unstyled.
func TestTemplateClassesHaveStyles(t *testing.T) {
	var css strings.Builder
	for _, name := range []string{"assets/static/tailwind.css", "assets/static/styles.css"} {
		b, err := assetsFS.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		css.Write(b)
	}
	action := regexp.MustCompile(`(?s)\{\{.*?\}\}`)
	inlineStyle := regexp.MustCompile(`(?is)<style[^>]*>(.*?)</style>`)
	classLists := []*regexp.Regexp{
		regexp.MustCompile(`\bclass="([^"]*)"`),
		regexp.MustCompile(`\bclassName\s*=\s*'([^']*)'`),
		regexp.MustCompile(`\bclassList\.(?:add|remove|toggle)\('([^']*)'`),
	}
	used := map[string][]string{}
	entries, err := assetsFS.ReadDir("assets/templates")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		b, err := assetsFS.ReadFile(path.Join("assets/templates", e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		src := action.ReplaceAllString(string(b), " ")
		for _, m := range inlineStyle.FindAllStringSubmatch(src, -1) {
			css.WriteString(m[1])
		}
		for _, re := range classLists {
			for _, m := range re.FindAllStringSubmatch(src, -1) {
				for _, c := range strings.Fields(m[1]) {
					used[c] = append(used[c], e.Name())
				}
			}
		}
	}
	escape := strings.NewReplacer(":", `\:`, "/", `\/`, ".", `\.`, "[", `\[`, "]", `\]`, "%", `\%`, "#", `\#`)
	styles := css.String()
	for c, in := range used {
		if hookClasses[c] {
			continue
		}
		sel := regexp.MustCompile(`\.` + regexp.QuoteMeta(escape.Replace(c)) + `(?:[^\w-]|$)`)
		if !sel.MatchString(styles) {
			t.Errorf("class %q (in %s) has no rule: regenerate tailwind.css (see scripts/tailwind/tailwind.config.js)", c, in[0])
		}
	}
	if len(used) < 100 {
		t.Fatalf("found only %d classes: the template scan is broken", len(used))
	}
}

// TestPagesBrowserSmoke opens the overview, a flow page, a journey page, a
// feature page and the audit page in Chromium with scripts/pages-smoke.js, every request to
// another origin blocked: each must render styled, with its fonts, with no
// console error, and draw its diagram. It skips in -short mode and when node
// or the playwright package is missing; it never installs browsers.
// @verifies ui:INV-5
func TestPagesBrowserSmoke(t *testing.T) {
	if testing.Short() {
		t.Skip("-short: no browser")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not found")
	}
	if err := exec.Command(node, "-e", "require('playwright')").Run(); err != nil {
		t.Skip("the playwright package is not installed")
	}
	srv := offlineServer(t)
	srv.installEventBroker(t.Context())
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	paths := []string{"/", "/flows/ep.orders.place", "/journeys/brd.orders", "/features/orders", "/audit"}
	args := append([]string{filepath.Join("..", "..", "..", "scripts", "pages-smoke.js"), ts.URL}, paths...)
	if dir := os.Getenv("LATTICE_PAGES_SHOTS"); dir != "" {
		args = append(args, "--shots", dir)
	}
	out, err := exec.Command(node, args...).CombinedOutput()
	if err != nil {
		if strings.Contains(string(out), "Executable doesn't exist") {
			t.Skip("playwright has no browser installed")
		}
		t.Fatalf("smoke test failed: %v\n%s", err, out)
	}
	for _, want := range []string{
		"ok   /: styled",
		"ok   /: Geist loaded from the server",
		"ok   /flows/ep.orders.place: mermaid draws the diagram",
		"ok   /journeys/brd.orders: mermaid draws the diagram",
		"ok   /flows/ep.orders.place: no request to another origin",
		"ok   /flows/ep.orders.place: no console error",
		"all checks passed",
	} {
		if !strings.Contains(string(out), want) {
			t.Errorf("smoke output lacks %q:\n%s", want, out)
		}
	}
}
