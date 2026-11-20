package cli

// @module-feature cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/salahmyn/lattice/pkg/lattice/brd"
	"github.com/salahmyn/lattice/pkg/lattice/confirm"
	"github.com/salahmyn/lattice/pkg/lattice/schema"
	"github.com/salahmyn/lattice/pkg/lattice/workspace"
)

// Link changes `lattice brd link` reports.
const (
	linkReconciled = "reconciled" // completes a link the other half already declares
	linkScope      = "scope"      // a feature enters or leaves a BRD's scope
	linkNone       = "none"       // both halves already agree
	linkSkipped    = "skipped"    // --reconcile leaves it: not a mirrored link
)

// linkChange is one change `lattice brd link` made, or left.
type linkChange struct {
	BRD     string `json:"brd"`
	Feature string `json:"feature"`
	Change  string `json:"change"`
	Version int    `json:"version"`
	Bumped  bool   `json:"bumped,omitempty"`
	// Left is the BRD the feature named before; its scope lost the feature.
	Left       string `json:"left,omitempty"`
	LeftBumped bool   `json:"left_bumped,omitempty"`
	Note       string `json:"note,omitempty"`
}

func (c linkChange) text() string {
	switch c.Change {
	case linkNone:
		return fmt.Sprintf("%s ↔ %s: already linked on both sides, nothing written", c.BRD, c.Feature)
	case linkSkipped:
		return fmt.Sprintf("skipped %s ↔ %s: %s", c.BRD, c.Feature, c.Note)
	case linkReconciled:
		s := fmt.Sprintf("linked %s ↔ %s: reconciled an existing link: approval kept (v%d unchanged)", c.BRD, c.Feature, c.Version)
		if c.Left != "" {
			s += "\n  " + leftText(c)
		}
		return s
	}
	s := fmt.Sprintf("linked %s ↔ %s: scope changed: ", c.BRD, c.Feature)
	if c.Bumped {
		s += fmt.Sprintf("version bumped to %d, approval voided", c.Version)
	} else {
		s += fmt.Sprintf("v%d was never reviewed, version kept", c.Version)
	}
	if c.Left != "" {
		s += "\n  " + leftText(c)
	}
	return s
}

func leftText(c linkChange) string {
	if c.LeftBumped {
		return fmt.Sprintf("%s no longer has %s in scope: its version was bumped, approval voided", c.Left, c.Feature)
	}
	return fmt.Sprintf("%s no longer has %s in scope", c.Left, c.Feature)
}

// linker applies link changes to the graph's BRDs and features in memory and
// writes each touched file once.
type linker struct {
	ws        *workspace.Workspace
	kg        schema.KnowledgeGraph
	cache     *confirm.File
	dirtyBRD  map[string]bool
	dirtyFeat map[string]bool
	bumped    map[string]bool
}

func newLinker(ws *workspace.Workspace, kg schema.KnowledgeGraph) *linker {
	return &linker{ws: ws, kg: kg, cache: confirm.LoadCache(ws.LatticeDir),
		dirtyBRD: map[string]bool{}, dirtyFeat: map[string]bool{}, bumped: map[string]bool{}}
}

func (l *linker) feature(id string) *schema.Manifest {
	for i := range l.kg.Features {
		if l.kg.Features[i].ID == id {
			return &l.kg.Features[i]
		}
	}
	return nil
}

// reviewed reports whether anyone has reviewed the BRD's current version: it
// is past draft, carries an approval, holds council votes, or has a decision
// on record in confirm.json. A scope change to such a version is a new version.
func (l *linker) reviewed(b *schema.BRD) bool {
	if (b.Status != "" && b.Status != schema.BRDDraft) || b.Approval != nil {
		return true
	}
	if votes, _ := brd.LoadVotes(l.ws.LatticeDir, b.ID, b.Version); len(votes) > 0 {
		return true
	}
	if l.cache != nil {
		for _, r := range l.cache.Records {
			if r.ID == b.ID && r.Version == b.Version {
				return true
			}
		}
	}
	return false
}

// scopeChanged bumps a reviewed BRD's version once per run, so the approval,
// the council votes and the confirmation of the old version no longer cover
// the new scope (BRD_DRIFT). It reports whether the version moved.
// @enforces brds:INV-10
func (l *linker) scopeChanged(b *schema.BRD) bool {
	if l.bumped[b.ID] {
		return true
	}
	if !l.reviewed(b) {
		return false
	}
	b.Version++
	l.bumped[b.ID] = true
	l.dirtyBRD[b.ID] = true
	return true
}

// link writes both halves of the link b ↔ f. Completing a half the other side
// already declares is a reconcile: bookkeeping that keeps the BRD's version,
// so its approval, council decision and confirmation stand (the packet hashes
// the scope, confirm.Scope, which a reconcile leaves as it was). Adding a
// feature neither half named is a scope change.
// @enforces brds:INV-10
func (l *linker) link(b *schema.BRD, f *schema.Manifest) linkChange {
	c := linkChange{BRD: b.ID, Feature: f.ID}
	inVia, declares := containsString(b.ImplementsVia, f.ID), f.ImplementsBRD == b.ID
	switch {
	case inVia && declares:
		c.Change = linkNone
	case inVia || declares:
		c.Change = linkReconciled
	default:
		c.Change = linkScope
		c.Bumped = l.scopeChanged(b)
	}
	if !inVia {
		// Appended, never re-sorted: the diff a reviewer reads shows only
		// the ids that were added.
		b.ImplementsVia = append(b.ImplementsVia, f.ID)
		l.dirtyBRD[b.ID] = true
	}
	if !declares {
		// The feature leaves the BRD it named: a scope change there unless
		// that BRD keeps it listed in its own implements_via.
		if prev := brd.Find(l.kg.BRDs, f.ImplementsBRD); prev != nil && !containsString(prev.ImplementsVia, f.ID) {
			c.Left = prev.ID
			c.LeftBumped = l.scopeChanged(prev)
		}
		f.ImplementsBRD = b.ID
		l.dirtyFeat[f.ID] = true
	}
	c.Version = b.Version
	return c
}

// reconcile completes every one-sided link whose other half is declared and
// uncontested, and skips the rest (BRD_LINK_ONE_SIDED keeps naming them).
// @enforces brds:INV-10
func (l *linker) reconcile() []linkChange {
	var out []linkChange
	// A feature that names a BRD that leaves it out of implements_via.
	for i := range l.kg.Features {
		f := &l.kg.Features[i]
		if b := brd.Find(l.kg.BRDs, f.ImplementsBRD); b != nil && !containsString(b.ImplementsVia, f.ID) {
			out = append(out, l.link(b, f))
		}
	}
	// A BRD that lists a feature that names no BRD. Two BRDs listing the
	// same such feature contest it: a feature has at most one BRD.
	listedBy := map[string][]string{}
	for _, b := range l.kg.BRDs {
		for _, fid := range b.ImplementsVia {
			listedBy[fid] = append(listedBy[fid], b.ID)
		}
	}
	for _, b := range l.kg.BRDs {
		for _, fid := range b.ImplementsVia {
			f := l.feature(fid)
			switch {
			case f == nil, f.ImplementsBRD == b.ID:
			case f.ImplementsBRD != "":
				out = append(out, linkChange{BRD: b.ID, Feature: fid, Change: linkSkipped, Version: b.Version,
					Note: fmt.Sprintf("the feature names %s: decide which BRD it belongs to, then `lattice brd link`", f.ImplementsBRD)})
			case len(listedBy[fid]) > 1:
				out = append(out, linkChange{BRD: b.ID, Feature: fid, Change: linkSkipped, Version: b.Version,
					Note: fmt.Sprintf("listed by %v: decide which BRD it belongs to, then `lattice brd link`", listedBy[fid])})
			default:
				out = append(out, l.link(brd.Find(l.kg.BRDs, b.ID), f))
			}
		}
	}
	return out
}

// save writes every BRD and feature manifest a link touched.
func (l *linker) save() error {
	for _, b := range l.kg.BRDs {
		if !l.dirtyBRD[b.ID] {
			continue
		}
		if _, err := brd.SaveForce(l.ws.BRDsDir(), b); err != nil {
			return fmt.Errorf("BRD_SAVE_FAILED: %w", err)
		}
	}
	for _, f := range l.kg.Features {
		if !l.dirtyFeat[f.ID] {
			continue
		}
		// Load the manifest fresh so the write keeps fields the graph
		// builder strips.
		path := joinLattice(l.ws.LatticeDir, f.SourcePath)
		loaded, err := schema.LoadManifest(path)
		if err != nil {
			return fmt.Errorf("FEATURE_LOAD_FAILED: %w", err)
		}
		loaded.ImplementsBRD = f.ImplementsBRD
		if err := schema.SaveCanonical(path, *loaded); err != nil {
			return fmt.Errorf("FEATURE_SAVE_FAILED: %w", err)
		}
	}
	return nil
}

func newBRDLinkCommand(io *IO) *cobra.Command {
	var reconcile bool
	cmd := &cobra.Command{
		Use:   "link <brd-id> <feature-id> | --reconcile",
		Short: "Link a BRD to a feature (writes back to both files)",
		Long: `Records the BRD ↔ Feature relationship on both sides of the link:
adds the feature id to brd.implements_via and sets feature.implements_brd.

The double-write is intentional — either field is enough to drive the
validator and the UI, but having both on disk keeps a single-file grep
discoverable in either direction.

A BRD's scope is every feature on either half of a link. Completing a link
the other half already declares (BRD_LINK_ONE_SIDED) leaves the scope as it
was: the command reports "reconciled", keeps the version, and the approval,
council decision and confirmation stand. Adding a feature neither half named
changes the scope: a reviewed version is bumped, which voids its approval
(BRD_DRIFT) and sends it back for review; a draft nobody reviewed keeps its
version.

--reconcile completes every one-sided link whose other half is declared and
uncontested, and prints what it changed and what it left.`,
		Args: func(cmd *cobra.Command, args []string) error {
			if reconcile {
				return cobra.NoArgs(cmd, args)
			}
			return cobra.ExactArgs(2)(cmd, args)
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := openWorkspace(io)
			if err != nil {
				return io.fail("NO_WORKSPACE", err.Error(), nil)
			}
			kg, _, err := graphFor(io, cmd, false)
			if err != nil {
				return io.fail("EXTRACT_FAILED", err.Error(), nil)
			}
			l := newLinker(ws, kg)
			var changes []linkChange
			if reconcile {
				changes = l.reconcile()
			} else {
				b := brd.Find(l.kg.BRDs, args[0])
				if b == nil {
					return io.fail("BRD_NOT_FOUND", "BRD not found: "+args[0], nil)
				}
				f := l.feature(args[1])
				if f == nil {
					return io.fail("FEATURE_NOT_FOUND", "feature not found: "+args[1], nil)
				}
				changes = []linkChange{l.link(b, f)}
			}
			if err := l.save(); err != nil {
				return io.fail("LINK_SAVE_FAILED", err.Error(), nil)
			}
			if io.JSON {
				if !reconcile {
					c := changes[0]
					return io.printJSON(struct {
						Status string `json:"status"`
						linkChange
					}{"linked", c})
				}
				if changes == nil {
					changes = []linkChange{}
				}
				return io.printJSON(map[string]interface{}{"changes": changes})
			}
			if len(changes) == 0 {
				io.printf("no one-sided links to reconcile\n")
			}
			for _, c := range changes {
				io.printf("%s\n", c.text())
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&reconcile, "reconcile", false, "complete every one-sided link whose other half is declared (keeps versions and approvals)")
	return cmd
}
