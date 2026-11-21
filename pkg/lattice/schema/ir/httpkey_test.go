package ir

import "testing"

// @verifies reachability:INV-11
func TestHTTPKey(t *testing.T) {
	for _, c := range []struct{ method, url, want string }{
		{"get", "/api/orders", "GET /api/orders"},
		{"GET", "/api/orders/", "GET /api/orders"},
		{"GET", "/api/orders?page=2#top", "GET /api/orders"},
		{"post", "https://api.example.com/api/orders", "POST /api/orders"},
		{"GET", "//cdn.example.com/a/b/", "GET /a/b"},
		{"GET", "http://localhost:3000", "GET /"},
		{"GET", "/", "GET /"},
		{"GET", "/orders/:id", "GET /orders/:id"},
		{"GET", " /api/orders ", "GET /api/orders"},
		{"GET", "api/orders", ""}, // relative to the page
		{"GET", "./orders", ""},
		{"GET", "?page=2", ""},
		{"GET", "", ""},
		{"", "/api/orders", ""},
	} {
		if got := HTTPKey(c.method, c.url); got != c.want {
			t.Errorf("HTTPKey(%q, %q) = %q, want %q", c.method, c.url, got, c.want)
		}
	}
	if got := HTTPOrigin("https://api.example.com/x"); got != "https://api.example.com" {
		t.Errorf("HTTPOrigin = %q", got)
	}
	if got := HTTPOrigin("/x"); got != "" {
		t.Errorf("HTTPOrigin of a path = %q", got)
	}
}
