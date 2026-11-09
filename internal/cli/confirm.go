package cli

// @module-feature confirm

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/salahmyn/lattice/pkg/lattice/audit"
	"github.com/salahmyn/lattice/pkg/lattice/brd"
	"github.com/salahmyn/lattice/pkg/lattice/confirm"
	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

// newConfirmCommand is `lattice confirm`: it computes CONFIRMED from the
// platform's approvals on the merged decision pull requests.
func newConfirmCommand(io *IO) *cobra.Command {
	var platform, branch string
	var offline, refresh bool
	cmd := &cobra.Command{
		Use:   "confirm [--platform github] [--offline]",
		Short: "Compute which decisions a human confirmed, from the platform's approvals",
		Long: `For each decision file on the default branch, finds the commit that added it,
the pull request that merged it, and that request's approving reviews from the
platform (GitHub REST, via GITHUB_TOKEN or GH_TOKEN). A decision counts only
if an approving review exists from a user other than the author, that user is
not a bot, the review is on the head that was merged, and the decision's packet
hash still matches the BRD, its promises and the diff since the last
confirmed version (otherwise it reads stale).

Writes lattice/.cache/confirm.json (lattice.confirm/1), derived and never
committed, cached by commit. 'lattice audit' reads the confirmed signal from
that file only. --offline reads the cache and nothing else: missing data reads
unknown, never confirmed. Run it in CI on the default branch.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if platform != "github" {
				return io.fail("CONFIRM_PLATFORM", "platform "+platform+" is not supported (github)", nil)
			}
			ws, err := openWorkspace(io)
			if err != nil {
				return io.fail("NO_WORKSPACE", err.Error(), nil)
			}
			g := confirm.Git{Dir: filepath.Dir(ws.LatticeDir)}
			head, _ := g.Head()
			cache := confirm.LoadCache(ws.LatticeDir)
			if offline {
				return printConfirm(io, confirm.Offline(cache, head))
			}

			top, err := g.Top()
			if err != nil {
				return io.fail("NOT_A_REPO", err.Error(), nil)
			}
			rel, err := filepath.Rel(evalLinks(top), evalLinks(ws.LatticeDir))
			if err != nil {
				return io.fail("NOT_A_REPO", err.Error(), nil)
			}
			rel = filepath.ToSlash(rel)
			if branch == "" {
				branch = confirm.DefaultBranch(g)
			}
			found, warnings, err := confirm.Scan(g, branch, rel)
			if err != nil {
				return io.fail("CONFIRM_SCAN", err.Error(), nil)
			}
			kg, _, err := graphFor(io, cmd, false)
			if err != nil {
				return io.fail("EXTRACT_FAILED", err.Error(), nil)
			}
			dirty, _ := gitDirty(filepath.Dir(ws.LatticeDir))
			rows := audit.Compute(audit.Collect(ws.LatticeDir, kg, dirty)).Rows

			packet := func(d confirm.Decision) (string, bool) {
				if d.Action == confirm.ActionWaiverAdd {
					return waiverPacket(g, rel, branch, d)
				}
				b := brd.Find(kg.BRDs, d.ID)
				if b == nil || b.Version != d.Version {
					return "", false
				}
				var prev *schema.BRD
				if d.Since != nil {
					prev, _ = confirm.BRDAt(g, rel, d.Since.SHA, d.ID)
				}
				extra := ""
				if d.Action == confirm.ActionAck || d.Action == confirm.ActionOverturn {
					votes, _ := brd.LoadVotes(ws.LatticeDir, d.ID, d.Version)
					extra = confirm.VotesHash(votes)
				}
				return confirm.PacketHash(confirm.PacketInput{
					BRD: *b, Promises: confirm.FactsFor(rows, confirm.PromisesOf(*b)),
					Diff: confirm.StructuralDiff(prev, *b), Extra: extra,
				}), true
			}

			plat := githubFor(g)
			f := confirm.Compute(cmd.Context(), confirm.Input{
				Commit: head, Branch: branch, Found: found, Platform: plat, Packet: packet,
				Cache: cache, Refresh: refresh,
			})
			f.Warnings = warnings
			if plat != nil {
				if err := confirm.SaveCache(ws.LatticeDir, f); err != nil {
					return io.fail("CONFIRM_WRITE", err.Error(), nil)
				}
			}
			return printConfirm(io, f)
		},
	}
	cmd.Flags().StringVar(&platform, "platform", "github", "the code host")
	cmd.Flags().BoolVar(&offline, "offline", false, "read the cache only; missing data reads unknown")
	cmd.Flags().BoolVar(&refresh, "refresh", false, "ask the platform again for every decision")
	cmd.Flags().StringVar(&branch, "branch", "", "the default branch (default: origin's HEAD, main or master)")
	return cmd
}

func printConfirm(io *IO, f confirm.File) error {
	if io.JSON {
		return io.printJSON(f)
	}
	io.printf("%s", f.Text())
	return nil
}

func evalLinks(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return p
}

// githubFor builds the GitHub client from the environment, or nil when there
// is no token or no repository to ask about.
func githubFor(g confirm.Git) confirm.Platform {
	token := os.Getenv("GITHUB_TOKEN")
	if token == "" {
		token = os.Getenv("GH_TOKEN")
	}
	if token == "" {
		return nil
	}
	slug := os.Getenv("GITHUB_REPOSITORY")
	owner, repo, ok := strings.Cut(slug, "/")
	if !ok {
		remote, err := g.Line("remote", "get-url", "origin")
		if err != nil {
			return nil
		}
		if owner, repo, ok = confirm.ParseGitHubRemote(remote); !ok {
			return nil
		}
	}
	return confirm.GitHub{Token: token, Owner: owner, Repo: repo, BaseURL: os.Getenv("GITHUB_API_URL")}
}

// waiverPacket recomputes a waiver's hash from the entry now on the default
// branch. A waiver missing from the file reads as a mismatch.
func waiverPacket(g confirm.Git, rel, branch string, d confirm.Decision) (string, bool) {
	if d.Waiver == nil {
		return "", true
	}
	data, err := g.Show(branch, filepath.ToSlash(filepath.Join(rel, confirm.WaiversFile)))
	if err != nil {
		return "missing", true
	}
	var w confirm.Waivers
	if yaml.Unmarshal(data, &w) != nil {
		return "unreadable", true
	}
	for _, e := range w.Waivers {
		if e.Code == d.Waiver.Code && e.Subject == d.Waiver.Subject {
			e.AddedBy, e.AddedAt = "", ""
			return confirm.WaiverHash(e), true
		}
	}
	return "missing", true
}
