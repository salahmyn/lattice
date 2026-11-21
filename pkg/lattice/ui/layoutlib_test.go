package ui

import (
	"bytes"
	"compress/gzip"
	"crypto/sha512"
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/salahmyn/lattice/pkg/lattice/schema"
	"github.com/salahmyn/lattice/pkg/lattice/store"
	"github.com/salahmyn/lattice/pkg/lattice/workspace"
)

// elkSRI is the published Subresource Integrity hash of elkjs 0.9.3's
// lib/elk.bundled.js, the file the map lays out with.
const elkSRI = "sha384-KO35c6PFq7tFxAEx6FtATwL9vOBOmCzQYr7GM0iTr3oO22s7Jc9OkclOH4AV7jwK"

// remoteRef matches what makes a browser fetch from another host: a src,
// href or data attribute on an element that loads it, a module import, a CSS
// import or url(), and fetch/Worker/importScripts with an absolute or
// protocol-relative URL. A plain <a href> is a link, not a load.
var remoteRef = regexp.MustCompile(`(?i)` +
	`<(script|link|img|iframe|source|embed|object|audio|video|track|use|image)\b[^>]*\b(src|href|xlink:href|data|srcset)\s*=\s*["']?\s*(https?:)?//` +
	`|\bimport\s*\(?\s*["'` + "`" + `](https?:)?//` +
	`|\bfrom\s*["'` + "`" + `](https?:)?//` +
	`|@import\s+(url\()?\s*["']?(https?:)?//` +
	`|url\(\s*["']?(https?:)?//` +
	`|\b(fetch|Worker|SharedWorker|importScripts)\s*\(\s*["'` + "`" + `](https?:)?//`)

func remoteRefs(page []byte) []string {
	var out []string
	for _, m := range remoteRef.FindAll(page, -1) {
		out = append(out, string(m))
	}
	return out
}

func mapExport(t *testing.T) []byte {
	t.Helper()
	g := store.Graph{Blocks: []schema.Block{{ID: "sys.shop", Kind: "system", Title: "Shop", Truth: "declared"},
		{ID: "web:cart", Kind: "screen", Parent: "sys.shop", Title: "Cart", Truth: "declared"}}}
	page, err := MapHTML(g, "Shop")
	if err != nil {
		t.Fatal(err)
	}
	return page
}

func sri(b []byte) string {
	h := sha512.Sum384(b)
	return "sha384-" + base64.StdEncoding.EncodeToString(h[:])
}

// The exported map loads nothing from the network: no script, stylesheet,
// image, module or CSS import names another host.
// @verifies ui:INV-4
func TestMapExportLoadsNothingRemote(t *testing.T) {
	if refs := remoteRefs(mapExport(t)); len(refs) > 0 {
		t.Errorf("the exported map loads from the network: %q", refs)
	}
	if refs := remoteRefs([]byte(`<a href="https://example.com/docs">docs</a>`)); len(refs) != 0 {
		t.Errorf("a plain link counted as a load: %q", refs)
	}
	for _, load := range []string{`<script src="https://cdn.example/x.js">`, `<link rel="stylesheet" href="//cdn.example/x.css">`,
		`import x from "https://cdn.example/x.mjs"`, `@import url("https://cdn.example/x.css")`} {
		if len(remoteRefs([]byte(load))) == 0 {
			t.Errorf("not caught: %s", load)
		}
	}
}

// The exported map carries the layout library itself, so it lays out with
// ELK from a file:// URL rather than falling back to the grid.
// @verifies ui:INV-4
func TestMapExportInlinesTheLayoutLibrary(t *testing.T) {
	page := mapExport(t)
	for _, want := range []string{"g.ELK = f()", "org.eclipse.elk.layered", `id="layout-lib"`} {
		if !bytes.Contains(page, []byte(want)) {
			t.Errorf("the export lacks the layout library (%s)", want)
		}
	}
	if bytes.Contains(page, []byte("/*__LAYOUT_LIB__*/")) || bytes.Contains(page, []byte("/*__GRAPH__*/")) {
		t.Error("the export left a slot unfilled")
	}
	// the inlined code is byte for byte the published elk.bundled.js
	start := bytes.Index(page, []byte(`<script id="layout-lib"`))
	if start < 0 {
		t.Fatal("no layout-lib script")
	}
	body := page[start:]
	body = body[bytes.IndexByte(body, '>')+1:]
	body = body[:bytes.Index(body, []byte("</script>"))]
	if got := sri(body); got != elkSRI {
		t.Errorf("inlined layout library hash = %s, want %s", got, elkSRI)
	}
}

// serve sends the map without the library inlined, and the library itself
// from the binary, gzipped or not, under the published hash the page checks.
// @verifies ui:INV-4
func TestServeMapLayoutLibraryFromBinary(t *testing.T) {
	srv := New(&workspace.Workspace{LatticeDir: t.TempDir()}, Options{})
	get := func(path, enc string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodGet, path, nil)
		if enc != "" {
			r.Header.Set("Accept-Encoding", enc)
		}
		w := httptest.NewRecorder()
		srv.routes().ServeHTTP(w, r)
		return w
	}
	page := get("/map", "").Body.Bytes()
	if refs := remoteRefs(page); len(refs) > 0 {
		t.Errorf("/map loads from the network: %q", refs)
	}
	if !bytes.Contains(page, []byte(`data-src="map/elkjs-0.9.3.js" data-integrity="`+elkSRI+`"`)) || bytes.Contains(page, []byte("org.eclipse.elk")) {
		t.Error("/map should point at the served library, not inline it")
	}
	for _, enc := range []string{"gzip, deflate, br", ""} {
		w := get("/map/elkjs-0.9.3.js", enc)
		if w.Code != http.StatusOK || !strings.HasPrefix(w.Header().Get("Content-Type"), "text/javascript") {
			t.Fatalf("library (Accept-Encoding %q) = %d %s", enc, w.Code, w.Header().Get("Content-Type"))
		}
		body := w.Body.Bytes()
		if gz := w.Header().Get("Content-Encoding") == "gzip"; gz != (enc != "") {
			t.Errorf("Accept-Encoding %q: Content-Encoding %q", enc, w.Header().Get("Content-Encoding"))
		} else if gz {
			zr, err := gzip.NewReader(bytes.NewReader(body))
			if err != nil {
				t.Fatal(err)
			}
			if body, err = io.ReadAll(zr); err != nil {
				t.Fatal(err)
			}
		}
		if got := sri(body); got != elkSRI {
			t.Errorf("served library (Accept-Encoding %q) hash = %s, want %s", enc, got, elkSRI)
		}
	}
}
