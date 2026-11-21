package ui

// @module-feature ui

import (
	"bytes"
	"compress/gzip"
	_ "embed"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
)

// The block map lays its diagram out with elkjs (Eclipse Layout Kernel,
// EPL-2.0), vendored unmodified under assets/map/third_party/elkjs with its
// licence. It is stored gzipped: serve sends those bytes as they are, and
// export map inlines the decompressed script, so the map never needs the
// network.

const (
	// layoutLibPath is where serve serves the script; map.html's layout-lib
	// element names it (data-src) with the published hash (data-integrity).
	layoutLibPath = "/map/elkjs-0.9.3.js"
	layoutSlot    = "/*__LAYOUT_LIB__*/"
)

//go:embed assets/map/third_party/elkjs/elk.bundled.js.gz
var layoutLibGz []byte

// layoutLib returns the decompressed elk.bundled.js, ready to inline in a
// <script> element.
var layoutLib = sync.OnceValues(func() ([]byte, error) {
	zr, err := gzip.NewReader(bytes.NewReader(layoutLibGz))
	if err != nil {
		return nil, err
	}
	js, err := io.ReadAll(zr)
	if err != nil {
		return nil, err
	}
	if l := bytes.ToLower(js); bytes.Contains(l, []byte("</script")) || bytes.Contains(l, []byte("<!--")) {
		return nil, errors.New("the vendored layout library cannot be inlined: it contains </script or <!--")
	}
	return js, nil
})

// inlineLayoutLib puts the layout library's code into the page's layout
// slot, so the exported page lays out without fetching anything.
// @enforces ui:INV-4
func inlineLayoutLib(page []byte) ([]byte, error) {
	js, err := layoutLib()
	if err != nil {
		return nil, err
	}
	return bytes.Replace(page, []byte(layoutSlot), js, 1), nil
}

// serveLayoutLib serves the layout library from the binary, gzipped when the
// client accepts it.
// @enforces ui:INV-4
func (s *Server) serveLayoutLib(w http.ResponseWriter, r *http.Request) {
	h := w.Header()
	h.Set("Content-Type", "text/javascript; charset=utf-8")
	h.Set("Cache-Control", "private, max-age=86400")
	h.Set("Vary", "Accept-Encoding")
	if strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
		h.Set("Content-Encoding", "gzip")
		_, _ = w.Write(layoutLibGz)
		return
	}
	js, err := layoutLib()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	_, _ = w.Write(js)
}
