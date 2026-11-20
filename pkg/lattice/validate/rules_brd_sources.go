package validate

// @module-feature validation

import (
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

// checkBRDSources checks the shape of every BRD source. It reads only the
// BRD: it never fetches a URL, and it never sets or infers a checked mark.
//
//	BRD_SOURCE_INVALID       (error) — unknown kind, empty ref, or a url
//	                                   that is not absolute http(s).
//	BRD_SOURCE_CHECK_INVALID (error) — checked_by without checked_at (or the
//	                                   reverse), or checked_at not a date or
//	                                   in the future.
//
// @enforces brds:INV-10
func (c *corpus) checkBRDSources() []schema.Violation {
	var v []schema.Violation
	today := time.Now().UTC().Format("2006-01-02")
	for i := range c.kg.BRDs {
		b := &c.kg.BRDs[i]
		loc := &schema.Location{File: b.SourcePath}
		for j, s := range b.Sources {
			at := fmt.Sprintf("sources[%d]", j)
			add := func(code, field, msg, detail string) {
				v = append(v, schema.Violation{
					Code: code, Severity: schema.SeverityError,
					Message:    fmt.Sprintf("BRD %q %s: %s", b.ID, at, msg),
					Subject:    b.ID + ":" + at,
					Location:   loc,
					NextAction: &schema.NextAction{Kind: "edit_brd", Field: at + "." + field, Detail: detail},
				})
			}
			if !validSourceKind(s.Kind) {
				add(schema.CodeBRDSourceInvalid, "kind", fmt.Sprintf("kind %q is not a source kind", s.Kind),
					"use one of ticket, roadmap, help_page, api_doc, adr")
			}
			if strings.TrimSpace(s.Ref) == "" {
				add(schema.CodeBRDSourceInvalid, "ref", "ref is empty",
					"name the ticket, roadmap item, page or ADR, e.g. WAL-12")
			}
			if s.URL != "" && !absoluteHTTP(s.URL) {
				add(schema.CodeBRDSourceInvalid, "url", fmt.Sprintf("url %q is not an absolute http(s) URL", s.URL),
					"use a full https:// URL, or remove url")
			}
			switch {
			case s.CheckedBy != "" && s.CheckedAt == "":
				add(schema.CodeBRDSourceCheckInvalid, "checked_at", "checked_by is set without checked_at",
					"the person who compared the BRD with the source adds the date (YYYY-MM-DD)")
			case s.CheckedAt != "" && s.CheckedBy == "":
				add(schema.CodeBRDSourceCheckInvalid, "checked_by", "checked_at is set without checked_by",
					"the person who compared the BRD with the source adds their name")
			}
			if s.CheckedAt != "" {
				if _, err := time.Parse("2006-01-02", s.CheckedAt); err != nil {
					add(schema.CodeBRDSourceCheckInvalid, "checked_at", fmt.Sprintf("checked_at %q is not a date (YYYY-MM-DD)", s.CheckedAt),
						"write the date as YYYY-MM-DD")
				} else if s.CheckedAt > today {
					add(schema.CodeBRDSourceCheckInvalid, "checked_at", fmt.Sprintf("checked_at %s is in the future", s.CheckedAt),
						"write the date the source was checked")
				}
			}
		}
	}
	return v
}

func validSourceKind(k schema.BRDSourceKind) bool {
	for _, ok := range schema.ValidBRDSourceKinds {
		if k == ok {
			return true
		}
	}
	return false
}

// absoluteHTTP reports whether s is an absolute http or https URL with a host.
func absoluteHTTP(s string) bool {
	u, err := url.Parse(s)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
}
