package config

// @module-feature confirm

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// Routes by which a decision may read human (`lattice confirm`).
const (
	RouteReview    = "review"
	RouteSignature = "signature"
)

// KnownRoutes lists every route confirm.routes accepts.
var KnownRoutes = []string{RouteReview, RouteSignature}

// Confirm is the `confirm:` block of config.yaml.
type Confirm struct {
	// Routes are the routes by which a decision may read human: review (an
	// approving review on the merged pull request) and signature (a
	// hardware-key signature). Empty means both. The list only narrows: a
	// decision confirmed by a route left out reads pending
	// (ROUTE_NOT_ACCEPTED), never human.
	Routes Routes `yaml:"routes,omitempty"`
}

// Routes is confirm.routes: a list of route names, or one name.
type Routes []string

// UnmarshalYAML accepts a sequence of names or a single name, so a
// one-route value written as a scalar still reads as that route instead of
// failing the whole file.
func (r *Routes) UnmarshalYAML(n *yaml.Node) error {
	switch n.Kind {
	case yaml.ScalarNode:
		if strings.TrimSpace(n.Value) == "" {
			*r = nil
			return nil
		}
		*r = Routes{n.Value}
		return nil
	case yaml.SequenceNode:
		var list []string
		if err := n.Decode(&list); err != nil {
			return fmt.Errorf("confirm.routes: want a list of %s: %w", strings.Join(KnownRoutes, ", "), err)
		}
		*r = list
		return nil
	}
	return fmt.Errorf("confirm.routes: want a list of %s", strings.Join(KnownRoutes, ", "))
}

func routeName(s string) string { return strings.ToLower(strings.TrimSpace(s)) }

// Accepts reports whether a decision confirmed by route may read human. With
// no routes listed, both are accepted. A value that names no known route
// accepts nothing, so a misspelt list narrows and never widens.
// @enforces confirm:INV-22
func (c Confirm) Accepts(route string) bool {
	if len(c.Routes) == 0 {
		return true
	}
	route = routeName(route)
	for _, r := range c.Routes {
		if routeName(r) == route {
			return true
		}
	}
	return false
}

// Unknown lists the values of confirm.routes that name no route.
func (c Confirm) Unknown() []string {
	var out []string
	for _, r := range c.Routes {
		known := false
		for _, k := range KnownRoutes {
			if routeName(r) == k {
				known = true
			}
		}
		if !known {
			out = append(out, r)
		}
	}
	return out
}

// Narrowed reports whether a route is left out, so some confirmation that
// would read human by default does not.
func (c Confirm) Narrowed() bool {
	for _, k := range KnownRoutes {
		if !c.Accepts(k) {
			return true
		}
	}
	return false
}

// Accepted names the accepted routes, for a reason text: "signature",
// "review, signature", or "none".
func (c Confirm) Accepted() string {
	var out []string
	for _, k := range KnownRoutes {
		if c.Accepts(k) {
			out = append(out, k)
		}
	}
	if len(out) == 0 {
		return "none"
	}
	return strings.Join(out, ", ")
}

// NotAccepted is the reason a confirmation by route reads pending: method
// says how it was made ("approved by ana on PR #12").
func (c Confirm) NotAccepted(method, route string) string {
	accepts := "accepts no route"
	if a := c.Accepted(); a != "none" {
		accepts = "accepts only " + a
	}
	if method == "" {
		method = "confirmed by " + route
	}
	return method + ", but confirm.routes in lattice/config.yaml " + accepts + ": a " + route +
		" is not a human confirmation in this workspace"
}

// Unreadable is the reason a confirmation reads unknown when config.yaml
// could not be read.
func Unreadable(err error) string {
	return "lattice/config.yaml could not be read, so which confirm.routes count is unknown: " + err.Error()
}

// Check reports a value of confirm.routes that names no route, as an error
// naming it and the routes there are; nil when every value is known.
func (c Confirm) Check() error {
	if bad := c.Unknown(); len(bad) > 0 {
		quoted := make([]string, len(bad))
		for i, b := range bad {
			quoted[i] = fmt.Sprintf("%q", b)
		}
		return fmt.Errorf("confirm.routes: %s is not a route (want %s)", strings.Join(quoted, ", "), strings.Join(KnownRoutes, ", "))
	}
	return nil
}

// LoadConfirm reads the confirm: block of latticeDir's config.yaml for a
// reader of confirm.json. A missing file gives the default, both routes. An
// unreadable file is an error: which routes the workspace accepts is then
// unknown, and the reader must not read any decision as human.
func LoadConfirm(latticeDir string) (Confirm, error) {
	cfg, err := Load(latticeDir)
	if err != nil {
		return Confirm{}, err
	}
	return cfg.Confirm, nil
}
