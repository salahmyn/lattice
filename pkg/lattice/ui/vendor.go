package ui

// @module-feature ui

import (
	"bytes"
	"compress/gzip"
	"embed"
	"io"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

// The server-rendered pages load nothing from the network. Their third-party
// code and fonts are vendored unmodified under assets/vendor, each with its
// licence (see THIRD_PARTY_NOTICES.md), and served from the binary at
// /vendor/. A large text file is stored gzipped as <name>.gz: serve sends
// those bytes as they are to a client that accepts gzip and decompresses them
// for one that does not.

const (
	vendorPrefix = "/vendor/"
	// mermaidPath is the diagram renderer the flow and journey pages load.
	// layout.html's "mermaid" template names it with the file's SHA-384.
	mermaidPath = vendorPrefix + "mermaid-10.9.8/mermaid.min.js"
)

//go:embed assets/vendor
var vendorFS embed.FS

// vendorTypes is the Content-Type of each served extension. A file with any
// other extension is not served.
var vendorTypes = map[string]string{
	".js":    "text/javascript; charset=utf-8",
	".css":   "text/css; charset=utf-8",
	".woff2": "font/woff2",
	".txt":   "text/plain; charset=utf-8",
	"":       "text/plain; charset=utf-8", // LICENSE
}

// vendorFile reads a vendored asset by its path under /vendor/. gz reports
// that data is the gzipped file.
func vendorFile(name string) (data []byte, gz bool, err error) {
	if !fs.ValidPath(name) || name == "." {
		return nil, false, fs.ErrNotExist
	}
	full := path.Join("assets/vendor", name)
	if data, err = vendorFS.ReadFile(full); err == nil {
		return data, false, nil
	}
	if data, err = vendorFS.ReadFile(full + ".gz"); err == nil {
		return data, true, nil
	}
	return nil, false, fs.ErrNotExist
}

// serveVendor serves a vendored asset from the binary, gzipped when the
// client accepts it.
// @enforces ui:INV-5
func (s *Server) serveVendor(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Path, vendorPrefix)
	ctype, ok := vendorTypes[path.Ext(name)]
	if !ok {
		http.NotFound(w, r)
		return
	}
	data, gz, err := vendorFile(name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	h := w.Header()
	h.Set("Content-Type", ctype)
	h.Set("Cache-Control", "private, max-age=86400")
	h.Set("X-Content-Type-Options", "nosniff")
	if gz {
		h.Set("Vary", "Accept-Encoding")
		if strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
			h.Set("Content-Encoding", "gzip")
		} else {
			zr, err := gzip.NewReader(bytes.NewReader(data))
			if err == nil {
				data, err = io.ReadAll(zr)
			}
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
		}
	}
	_, _ = w.Write(data)
}
