// Package lease implements the v0.8 §5 work-claim protocol: a
// lightweight, file-backed way for multiple autonomous agents to claim a
// slice (a feature, BRD, or scenario) before editing it, so a fleet acts
// in parallel without colliding.
//
// Leases are files under lattice/.leases/, claimed and released through
// the same git the agents commit to — there is no daemon and no lock
// server, so the coordination state is itself versioned and auditable.
// Leases are advisory-but-visible: `lattice next` will not hand a leased
// slice to a second agent, and they expire on a TTL so a dead agent never
// wedges the fleet.
package lease

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// dir is the leases directory relative to the lattice/ directory.
const dir = ".leases"

// Lease is one agent's claim on a unit of work.
type Lease struct {
	// Unit is the claimed slice id (feature id, brd id, or scenario ref).
	Unit string `yaml:"unit"`
	// Actor is the agent identity holding the lease.
	Actor string `yaml:"actor"`
	// AcquiredCommit records the commit the lease was taken against.
	AcquiredCommit string `yaml:"acquired_commit,omitempty"`
	// AcquiredAt / Expires are RFC3339 timestamps. A lease past Expires is
	// stale and ignored by IsActive.
	AcquiredAt string `yaml:"acquired_at"`
	Expires    string `yaml:"expires"`
	// Scope lists the repo-relative path prefixes the lease covers — the
	// blast radius two leases are checked for overlap against.
	Scope []string `yaml:"scope,omitempty"`
}

// IsActive reports whether the lease has not yet expired, as of now.
func (l Lease) IsActive(now time.Time) bool {
	if l.Expires == "" {
		return true
	}
	exp, err := time.Parse(time.RFC3339, l.Expires)
	if err != nil {
		return true // unparseable expiry → treat as active, surfaced elsewhere
	}
	return now.Before(exp)
}

// fileName is the on-disk name for a unit's lease. The unit id is
// path-sanitized so "todo.add" and "brd.x" become flat filenames.
func fileName(unit string) string {
	safe := strings.NewReplacer("/", "_", ":", "_", string(filepath.Separator), "_").Replace(unit)
	return safe + ".yaml"
}

// Acquire writes a lease for unit held by actor with the given TTL and
// scope. It fails if a *different* actor already holds an active lease on
// the unit; the same actor re-acquiring refreshes the TTL.
func Acquire(latticeDir, unit, actor, commit string, ttl time.Duration, scope []string, now time.Time) (Lease, error) {
	if strings.TrimSpace(unit) == "" {
		return Lease{}, fmt.Errorf("lease: unit is required")
	}
	if strings.TrimSpace(actor) == "" {
		return Lease{}, fmt.Errorf("lease: actor is required (pass --actor or set LATTICE_ACTOR)")
	}
	if existing, ok := find(latticeDir, unit); ok && existing.IsActive(now) && existing.Actor != actor {
		return Lease{}, fmt.Errorf("lease on %q is held by %s until %s", unit, existing.Actor, existing.Expires)
	}
	l := Lease{
		Unit:           unit,
		Actor:          actor,
		AcquiredCommit: commit,
		AcquiredAt:     now.UTC().Format(time.RFC3339),
		Expires:        now.UTC().Add(ttl).Format(time.RFC3339),
		Scope:          scope,
	}
	if err := save(latticeDir, l); err != nil {
		return Lease{}, err
	}
	return l, nil
}

// Release removes a unit's lease. Releasing a lease held by a different
// actor fails unless force is set.
func Release(latticeDir, unit, actor string, force bool) error {
	existing, ok := find(latticeDir, unit)
	if !ok {
		return nil // already released — idempotent
	}
	if !force && actor != "" && existing.Actor != actor {
		return fmt.Errorf("lease on %q is held by %s, not %s (use --force to override)", unit, existing.Actor, actor)
	}
	return os.Remove(filepath.Join(latticeDir, dir, fileName(unit)))
}

// List returns every lease on disk, sorted by unit. Stale (expired)
// leases are included so callers can report and prune them.
func List(latticeDir string) ([]Lease, error) {
	root := filepath.Join(latticeDir, dir)
	entries, err := os.ReadDir(root)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []Lease
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(root, e.Name()))
		if err != nil {
			continue
		}
		var l Lease
		if yaml.Unmarshal(data, &l) == nil && l.Unit != "" {
			out = append(out, l)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Unit < out[j].Unit })
	return out, nil
}

// Active returns only the leases that have not expired as of now.
func Active(latticeDir string, now time.Time) ([]Lease, error) {
	all, err := List(latticeDir)
	if err != nil {
		return nil, err
	}
	var out []Lease
	for _, l := range all {
		if l.IsActive(now) {
			out = append(out, l)
		}
	}
	return out, nil
}

// Overlap is a pair of active leases whose Scope paths intersect — the
// signal CONCURRENT work may collide. PathPrefix is the overlapping
// prefix that triggered it.
type Overlap struct {
	A, B       Lease
	PathPrefix string
}

// Overlaps returns every pair of active leases held by *different* actors
// whose scopes intersect. Two leases by the same actor never conflict.
func Overlaps(leases []Lease) []Overlap {
	var out []Overlap
	for i := 0; i < len(leases); i++ {
		for j := i + 1; j < len(leases); j++ {
			a, b := leases[i], leases[j]
			if a.Actor == b.Actor {
				continue
			}
			if p, ok := scopesOverlap(a.Scope, b.Scope); ok {
				out = append(out, Overlap{A: a, B: b, PathPrefix: p})
			}
		}
	}
	return out
}

// scopesOverlap reports whether any path in a is a prefix of (or equal
// to) any path in b, or vice-versa.
func scopesOverlap(a, b []string) (string, bool) {
	for _, pa := range a {
		pa = strings.TrimSuffix(pa, "/")
		for _, pb := range b {
			pb = strings.TrimSuffix(pb, "/")
			if pa == "" || pb == "" {
				continue
			}
			if pa == pb || strings.HasPrefix(pb+"/", pa+"/") || strings.HasPrefix(pa+"/", pb+"/") {
				if len(pa) <= len(pb) {
					return pa, true
				}
				return pb, true
			}
		}
	}
	return "", false
}

func find(latticeDir, unit string) (Lease, bool) {
	data, err := os.ReadFile(filepath.Join(latticeDir, dir, fileName(unit)))
	if err != nil {
		return Lease{}, false
	}
	var l Lease
	if yaml.Unmarshal(data, &l) != nil || l.Unit == "" {
		return Lease{}, false
	}
	return l, true
}

func save(latticeDir string, l Lease) error {
	root := filepath.Join(latticeDir, dir)
	if err := os.MkdirAll(root, 0o755); err != nil {
		return err
	}
	data, err := yaml.Marshal(l)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(root, fileName(l.Unit)), data, 0o644)
}
