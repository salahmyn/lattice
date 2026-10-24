package validate

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
	"gopkg.in/yaml.v3"

	"github.com/salahmyn/lattice/pkg/lattice/config"
	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

// SeverityOff drops a finding entirely (validate.severity: <CODE>: off).
const SeverityOff = "off"

// defaultSeverity holds the overrides a workspace gets without configuring
// anything: rules that are opt-in.
var defaultSeverity = map[string]string{
	schema.CodeUnattributedModule: SeverityOff,
}

// rank orders severities from most to least serious.
func rank(s schema.Severity) int {
	switch s {
	case schema.SeverityError:
		return 0
	case schema.SeverityWarning:
		return 1
	case schema.SeverityInfo:
		return 2
	}
	return 3
}

// parseSeverity maps a configured name to a severity. ok is false for an
// unknown name; off reports the finding is dropped.
func parseSeverity(name string) (sev schema.Severity, off, ok bool) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "error":
		return schema.SeverityError, false, true
	case "warning", "warn":
		return schema.SeverityWarning, false, true
	case "info":
		return schema.SeverityInfo, false, true
	case SeverityOff:
		return "", true, true
	}
	return "", false, false
}

// applySeverity rewrites each finding's severity from the workspace's
// overrides (falling back to defaultSeverity) and drops findings set off.
// An unknown severity name is itself reported, and leaves the code as is.
func applySeverity(v []schema.Violation, overrides map[string]string) []schema.Violation {
	var out []schema.Violation
	for code, name := range overrides {
		if _, _, ok := parseSeverity(name); !ok {
			out = append(out, schema.Violation{
				Code: schema.CodeValidatePolicy, Severity: schema.SeverityError,
				Subject:  code,
				Message:  fmt.Sprintf("config.yaml validate.severity.%s: %q is not one of error, warning, info, off", code, name),
				Location: &schema.Location{File: "config.yaml"},
			})
		}
	}
	for _, f := range v {
		name, set := overrides[f.Code]
		if !set {
			name, set = defaultSeverity[f.Code]
		}
		if set {
			sev, off, ok := parseSeverity(name)
			if off {
				continue
			}
			if ok {
				f.Severity = sev
			}
		}
		out = append(out, f)
	}
	return out
}

// FailOn parses a fail-on threshold (error, warning, info); empty means error.
func FailOn(name string) (schema.Severity, error) {
	if strings.TrimSpace(name) == "" {
		return schema.SeverityError, nil
	}
	sev, off, ok := parseSeverity(name)
	if !ok || off {
		return "", fmt.Errorf("fail-on %q: want error, warning or info", name)
	}
	return sev, nil
}

// Fails reports whether a finding reaches the fail-on threshold.
func Fails(f schema.Violation, threshold schema.Severity) bool {
	return rank(f.Severity) <= rank(threshold)
}

// AllowEntry accepts one known finding. Code and Reason are required, and at
// least one selector (Subject, File, Feature) so an entry never silences a
// whole rule by accident; use subject: "**" to do that on purpose. Subject
// and File are doublestar globs. Kind classifies the entry (generated, debt,
// ...); ADR names the decision record behind it, a path relative to the
// directory holding lattice/ (or a URL), which must exist.
type AllowEntry struct {
	Code    string `yaml:"code" json:"code"`
	Subject string `yaml:"subject,omitempty" json:"subject,omitempty"`
	File    string `yaml:"file,omitempty" json:"file,omitempty"`
	Feature string `yaml:"feature,omitempty" json:"feature,omitempty"`
	Kind    string `yaml:"kind,omitempty" json:"kind,omitempty"`
	ADR     string `yaml:"adr,omitempty" json:"adr,omitempty"`
	Reason  string `yaml:"reason" json:"reason"`
	Note    string `yaml:"note,omitempty" json:"note,omitempty"`
}

// Allowlist is the parsed allowlist file.
type Allowlist struct {
	Path    string       `yaml:"-" json:"path"`
	Entries []AllowEntry `yaml:"entries" json:"entries"`
}

// allowlistKeys names the keys an allowlist accepts, for schema errors.
const allowlistKeys = "the file takes `entries:`; an entry takes code, subject, file, feature, kind, adr, reason, note"

// LoadAllowlist reads the workspace's allowlist (validate.allowlist,
// relative to latticeDir). A missing file is an empty allowlist. An unknown
// key, or a malformed entry, comes back as an ALLOWLIST_SCHEMA error; bad
// entries are left out.
func LoadAllowlist(latticeDir string, p config.ValidatePolicy) (Allowlist, []schema.Violation) {
	rel := p.AllowlistPath()
	al := Allowlist{Path: rel}
	data, err := os.ReadFile(filepath.Join(latticeDir, rel))
	if os.IsNotExist(err) {
		return al, nil
	}
	loc := &schema.Location{File: rel}
	if err != nil {
		return al, []schema.Violation{{Code: schema.CodeAllowlistSchema, Severity: schema.SeverityError,
			Message: "cannot read allowlist: " + err.Error(), Location: loc}}
	}
	var raw Allowlist
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true) // a misspelt key must not load as an empty allowlist
	if err := dec.Decode(&raw); err != nil && err != io.EOF {
		hint := allowlistKeys
		if strings.Contains(err.Error(), "mapping values are not allowed") {
			// An unquoted `reason: see ADR: 12` is the usual cause, and it
			// drops every entry at once.
			hint = "quote a value that contains \": \", e.g. reason: \"enforced in SQL: see ADR-12\""
		}
		return al, []schema.Violation{{Code: schema.CodeAllowlistSchema, Severity: schema.SeverityError,
			Message: "cannot parse allowlist: " + err.Error() + " (" + hint + ")", Location: loc}}
	}
	root := filepath.Dir(latticeDir)
	var bad []schema.Violation
	for i, e := range raw.Entries {
		var why string
		kind, known := p.AllowlistKinds[e.Kind]
		switch {
		case strings.TrimSpace(e.Code) == "":
			why = "has no code"
		case strings.TrimSpace(e.Reason) == "":
			why = "has no reason"
		case e.Subject == "" && e.File == "" && e.Feature == "":
			why = "has no subject, file or feature (use subject: \"**\" to accept every finding of the code)"
		case e.Subject != "" && !doublestar.ValidatePattern(e.Subject):
			why = fmt.Sprintf("subject %q is not a valid glob", e.Subject)
		case e.File != "" && !doublestar.ValidatePattern(e.File):
			why = fmt.Sprintf("file %q is not a valid glob", e.File)
		case len(p.AllowlistKinds) > 0 && !known:
			why = fmt.Sprintf("has kind %q, not one of validate.allowlist_kinds (%s)", e.Kind, strings.Join(sortedKeys(p.AllowlistKinds), ", "))
		case kind.RequireADR && strings.TrimSpace(e.ADR) == "":
			why = fmt.Sprintf("has kind %q, which requires an adr", e.Kind)
		case e.ADR != "" && !isURL(e.ADR) && !fileExists(filepath.Join(root, filepath.FromSlash(e.ADR))):
			why = fmt.Sprintf("names adr %q, which does not exist", e.ADR)
		}
		if why != "" {
			bad = append(bad, schema.Violation{Code: schema.CodeAllowlistSchema, Severity: schema.SeverityError,
				Subject:  fmt.Sprintf("entries[%d]", i),
				Message:  fmt.Sprintf("allowlist entry %d (%s) %s", i+1, e.Code, why),
				Location: loc})
			continue
		}
		al.Entries = append(al.Entries, e)
	}
	return al, bad
}

func (e AllowEntry) matches(f schema.Violation) bool {
	if e.Code != f.Code {
		return false
	}
	if e.Subject != "" {
		if ok, _ := doublestar.Match(e.Subject, f.Subject); !ok {
			return false
		}
	}
	if e.File != "" {
		file := ""
		if f.Location != nil {
			file = f.Location.File
		}
		if ok, _ := doublestar.Match(e.File, file); !ok {
			return false
		}
	}
	return e.Feature == "" || e.Feature == f.FeatureID
}

func sortedKeys(m map[string]config.AllowlistKind) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func isURL(s string) bool {
	return strings.HasPrefix(s, "https://") || strings.HasPrefix(s, "http://")
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// Accepted is a finding an allowlist entry accepted, with the entry's
// reason, kind and decision record.
type Accepted struct {
	schema.Violation
	Reason string `json:"reason"`
	Kind   string `json:"kind,omitempty"`
	ADR    string `json:"adr,omitempty"`
}

// Apply splits findings into those still open and those an entry accepts.
// An entry that accepts nothing is stale: the gap it excused is gone, so it
// comes back as an ALLOWLIST_STALE error and the list only ever shrinks.
// Allowlist findings themselves are never accepted.
func (al Allowlist) Apply(v []schema.Violation) (open []schema.Violation, accepted []Accepted) {
	used := make([]bool, len(al.Entries))
	for _, f := range v {
		hit := -1
		if f.Code != schema.CodeAllowlistStale && f.Code != schema.CodeAllowlistSchema {
			for i, e := range al.Entries {
				if e.matches(f) {
					hit = i
					break
				}
			}
		}
		if hit < 0 {
			open = append(open, f)
			continue
		}
		used[hit] = true
		e := al.Entries[hit]
		accepted = append(accepted, Accepted{Violation: f, Reason: e.Reason, Kind: e.Kind, ADR: e.ADR})
	}
	for i, e := range al.Entries {
		if used[i] {
			continue
		}
		sel := e.Subject
		if sel == "" {
			sel = e.File
		}
		if sel == "" {
			sel = e.Feature
		}
		open = append(open, schema.Violation{
			Code: schema.CodeAllowlistStale, Severity: schema.SeverityError,
			Subject:  e.Code + " " + sel,
			Message:  fmt.Sprintf("allowlist entry %s %s (%s) matches no finding: the gap is closed, remove the entry", e.Code, sel, e.Reason),
			Location: &schema.Location{File: al.Path},
			NextAction: &schema.NextAction{Kind: "edit_allowlist",
				Detail: "delete the entry so the allowlist only holds open gaps"},
		})
	}
	return open, accepted
}
