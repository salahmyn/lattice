package ir

// @module-feature reachability

import (
	"regexp"
	"strings"
)

var urlOrigin = regexp.MustCompile(`^(?:[A-Za-z][A-Za-z0-9+.-]*:)?//[^/?#]*`)

// HTTPKey is the key of a literal request: the upper-case method, a space,
// and the path of the URL with its origin, query string and fragment
// removed and a trailing slash collapsed ("GET /api/orders"). It returns ""
// when the URL names no path the key can stand for: an empty URL, or a
// relative one ("orders", "./orders", "?page=2"), which depends on the page
// that sends it.
// @enforces reachability:INV-11
func HTTPKey(method, rawURL string) string {
	method = strings.ToUpper(strings.TrimSpace(method))
	u := strings.TrimSpace(rawURL)
	if method == "" || u == "" {
		return ""
	}
	hadOrigin := false
	if loc := urlOrigin.FindStringIndex(u); loc != nil {
		u, hadOrigin = u[loc[1]:], true
	}
	if !hadOrigin && !strings.HasPrefix(u, "/") {
		return ""
	}
	if i := strings.IndexAny(u, "?#"); i >= 0 {
		u = u[:i]
	}
	if u == "" {
		u = "/"
	}
	if len(u) > 1 {
		u = strings.TrimRight(u, "/")
		if u == "" {
			u = "/"
		}
	}
	return method + " " + u
}

var originRe = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9+.-]*://[A-Za-z0-9.-]+(?::[0-9]+)?$`)

// ValidHTTPOrigin reports whether s is a scheme plus host, with an optional
// port and no path, query or trailing slash ("https://app.example.com").
func ValidHTTPOrigin(s string) bool { return originRe.MatchString(s) }

// SameHTTPOrigin compares two origins; scheme and host are case-insensitive.
func SameHTTPOrigin(a, b string) bool { return strings.EqualFold(a, b) }

// HTTPOrigin is the scheme and host a URL names ("https://api.example.com"),
// or "" for a path-only URL. A key drops it; a reference keeps it, so a call
// to another service is told apart from one to this system's own routes.
func HTTPOrigin(rawURL string) string {
	if loc := urlOrigin.FindStringIndex(strings.TrimSpace(rawURL)); loc != nil {
		return strings.TrimSpace(rawURL)[:loc[1]]
	}
	return ""
}
