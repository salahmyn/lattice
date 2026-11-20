package cli

// @module-feature confirm

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/crypto/ssh"

	"github.com/salahmyn/lattice/pkg/lattice/confirm"
	"github.com/salahmyn/lattice/pkg/lattice/confirm/signature"
	"github.com/salahmyn/lattice/pkg/lattice/results"
	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

// Hooks for tests: whether the process runs at a terminal, and the
// ssh-keygen to run.
var (
	signTerminal = func() bool { return charDevice(os.Stdin) && charDevice(os.Stdout) }
	sshKeygen    = "ssh-keygen"
)

// ciMarkers are environment variables CI systems set, beyond the run ids
// results.CIRunID reads.
var ciMarkers = []string{"CI", "GITHUB_ACTIONS", "GITLAB_CI", "BUILDKITE", "CIRCLECI", "TF_BUILD", "JENKINS_URL", "TEAMCITY_VERSION"}

// newSignCommand is `lattice sign <decision>`: a human signs a decision with
// a hardware security key. It is the one command that calls a program
// outside lattice (ssh-keygen), and it is kept apart from `confirm`, which
// agents and CI run, so that it is easy to deny to them.
func newSignCommand(io *IO) *cobra.Command {
	var keyPath, branch string
	var signers bool
	cmd := &cobra.Command{
		Use:   "sign <decision> | sign --signers",
		Short: "Sign a decision with your hardware security key (a human at a terminal only)",
		Long: `Signs a decision file with an OpenSSH security key (ssh-keygen -t ed25519-sk
or ecdsa-sk) and writes <stem>.sig beside it. <decision> is the decision file,
its stem, or its decision/<stem> branch name; check the branch out first, since
the signature is written beside the file in your working tree.

The command prints the decision (action, BRD, version, packet hash, promises,
repository) and the exact message, then runs
'ssh-keygen -Y sign -n lattice-decision -f <key>': touch the key when it
blinks. The message is lattice-decision/v1 with the repository, decision id,
version, action, packet hash and rows; it is computed, never stored, and
'lattice confirm' and 'lattice check' compute it again to verify.

--key is the private key file (its .pub must sit beside it). The default is
the first ~/.ssh/id_ed25519_sk* or ~/.ssh/id_ecdsa_sk*. A key that is not a
security-key type is refused (SOFTWARE_KEY): nothing can tell a passphrase a
human typed from a key file an agent read.

sign refuses to run without a terminal (SIGN_NO_TTY), under CI (SIGN_CI) or
for an agent actor (SIGN_AGENT). It is not offered through MCP or the shipped
skills. Commit the .sig with the decision and merge it; the key must already
be listed for your GitHub login in lattice/allowed_signers on the default
branch, and be one of your GitHub SSH signing keys.

--signers endorses the change to lattice/allowed_signers in your working tree
instead, and writes lattice/allowed_signers.sig. A change that adds a signer
counts only with this endorsement, made by a key allowed_signers already
trusts; removing a signer needs none. It prints the signers added and removed
against the default branch (--branch) and the exact message, which names the
commit that last changed allowed_signers there: if the file changes on the
default branch before you merge, sign again. Commit the .sig in the same commit
as the change, or merge with a merge commit or a squash. The same refusals
apply.`,
		Args: func(cmd *cobra.Command, args []string) error {
			if signers {
				return cobra.NoArgs(cmd, args)
			}
			return cobra.ExactArgs(1)(cmd, args)
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if code, why := signRefusal(io); code != "" {
				return io.fail(code, why, "run `lattice sign` yourself, at your own terminal, with your security key")
			}
			ws, err := openWorkspace(io)
			if err != nil {
				return io.fail("NO_WORKSPACE", err.Error(), nil)
			}
			g := confirm.Git{Dir: filepath.Dir(ws.LatticeDir)}
			top, err := g.Top()
			if err != nil {
				return io.fail("NOT_A_REPO", err.Error(), nil)
			}
			if signers {
				return signSigners(io, g, top, ws.LatticeDir, keyPath, branch)
			}
			file, d, err := findDecision(top, ws.LatticeDir, args[0])
			if err != nil {
				return io.fail("SIGN_NO_DECISION", err.Error(), "check out the decision branch (git switch decision/<stem>), then sign")
			}
			key, pub, err := signingKey(keyPath)
			if err != nil {
				return io.fail("SIGN_NO_KEY", err.Error(), "create one with `ssh-keygen -t ed25519-sk`, or pass --key")
			}
			fp := ssh.FingerprintSHA256(pub)
			if !signature.HardwareKey(pub.Type()) {
				return io.fail(signature.CodeSoftwareKey, key+" is a "+pub.Type()+" key: only a hardware security key (ed25519-sk, ecdsa-sk) can sign a decision",
					"create one with `ssh-keygen -t ed25519-sk` and enrol it")
			}
			repo := signature.RepositoryOf(top)
			tuple := confirm.SignatureTuple(d, repo)
			msg, err := tuple.Message()
			if err != nil {
				return io.fail(signature.CodeIncomplete, err.Error(), "set the origin remote to the GitHub repository")
			}

			rows := strings.Join(tuple.Rows, ", ")
			if rows == "" {
				rows = "(none)"
			}
			io.errorf("Decision    %s %s v%d\nPacket      %s\nPromises    %s\n", d.Action, d.ID, d.Version, d.PacketHash, rows)
			if d.Reason != "" {
				io.errorf("Reason      %s\n", d.Reason)
			}
			io.errorf("Repository  %s\nKey         %s (%s)\nFile        %s\n\nSigning this message in namespace %s:\n\n%s\nTouch your security key when it blinks.\n",
				repo, fp, pub.Type(), file, signature.Namespace, indent(string(msg)))

			sig, err := keygenSign(io, key, msg)
			if err != nil {
				return io.fail("SIGN_FAILED", err.Error(), nil)
			}
			v, f := signature.Verify(sig, tuple)
			if f != nil {
				return io.fail(f.Code, "the new signature does not verify: "+f.Text, nil)
			}
			if v.Fingerprint != fp {
				return io.fail(signature.CodeInvalid, "ssh-keygen signed with "+v.Fingerprint+", not "+fp, nil)
			}
			out := signature.SigPath(file)
			if err := os.WriteFile(out, sig, 0o644); err != nil {
				return io.fail("SIGN_FAILED", err.Error(), nil)
			}
			rel, _ := filepath.Rel(top, out)
			if io.JSON {
				return io.printJSON(map[string]interface{}{"contract": "lattice.sign/1", "signature": filepath.ToSlash(rel),
					"fingerprint": fp, "repository": repo, "id": d.ID, "version": d.Version, "action": d.Action, "packet_hash": d.PacketHash})
			}
			io.printf("wrote %s (%s)\ncommit it with the decision file and merge; `lattice confirm` then verifies it\n", filepath.ToSlash(rel), signature.Short(fp))
			return nil
		},
	}
	cmd.Flags().StringVar(&keyPath, "key", "", "the security-key private key file (default: the first ~/.ssh/id_ed25519_sk* or id_ecdsa_sk*)")
	cmd.Flags().BoolVar(&signers, "signers", false, "endorse the change to lattice/allowed_signers in the working tree")
	cmd.Flags().StringVar(&branch, "branch", "", "with --signers: the default branch the change merges into (default: origin's HEAD, main or master)")
	return cmd
}

// signSigners endorses the working tree's change to allowed_signers against
// the default branch: the signers it adds and removes, after the commit that
// last changed the file there.
func signSigners(io *IO, g confirm.Git, top, latticeDir, keyPath, branch string) error {
	rel, err := filepath.Rel(evalLinks(top), evalLinks(latticeDir))
	if err != nil {
		return io.fail("NOT_A_REPO", err.Error(), nil)
	}
	rel = filepath.ToSlash(rel)
	file := filepath.Join(latticeDir, signature.AllowedSignersFile)
	next, err := os.ReadFile(file)
	if err != nil {
		return io.fail("SIGN_NO_SIGNERS", "no "+filepath.ToSlash(filepath.Join(rel, signature.AllowedSignersFile))+" in the working tree: "+err.Error(),
			"write the change to allowed_signers first, then endorse it")
	}
	if branch == "" {
		branch = confirm.DefaultBranch(g)
	}
	repo := signature.Repo{Dir: top, LatticeRel: rel, Ref: branch, Repository: signature.RepositoryOf(top)}
	after, err := repo.LastChange(branch)
	if err != nil {
		return io.fail("SIGN_NO_BRANCH", err.Error(), "pass the default branch with --branch")
	}
	prev, _ := g.Show(branch, path.Join(rel, signature.AllowedSignersFile))
	if signature.SignersDigest(prev) == signature.SignersDigest(next) {
		return io.fail("SIGN_NO_CHANGE", "allowed_signers is the same as on "+branch+": there is no change to endorse", "edit lattice/allowed_signers, then endorse the change")
	}
	key, pub, err := signingKey(keyPath)
	if err != nil {
		return io.fail("SIGN_NO_KEY", err.Error(), "create one with `ssh-keygen -t ed25519-sk`, or pass --key")
	}
	fp := ssh.FingerprintSHA256(pub)
	if !signature.HardwareKey(pub.Type()) {
		return io.fail(signature.CodeSoftwareKey, key+" is a "+pub.Type()+" key: only a hardware security key (ed25519-sk, ecdsa-sk) can endorse allowed_signers",
			"create one with `ssh-keygen -t ed25519-sk` and enrol it")
	}
	change := signature.SignersChange{Repository: repo.Repository, After: after, Signers: next}
	msg, err := change.Message()
	if err != nil {
		return io.fail(signature.CodeIncomplete, err.Error(), "set the origin remote to the GitHub repository")
	}
	added, removed := signature.DiffSigners(prev, next)
	list := func(l []string) string {
		if len(l) == 0 {
			return "(none)"
		}
		return strings.Join(l, "\n            ")
	}
	io.errorf("Change      lattice/allowed_signers against %s, after %s\nAdded       %s\nRemoved     %s\n", branch, after, list(added), list(removed))
	if len(added) == 0 {
		io.errorf("Note        this change adds no signer: it needs no endorsement\n")
	}
	if trusted, _ := repo.Trust(branch).Logins(pub); len(trusted) == 0 {
		io.errorf("Warning     key %s is not trusted in allowed_signers on %s: its endorsement will not count\n", signature.Short(fp), branch)
	}
	io.errorf("Repository  %s\nKey         %s (%s)\n\nSigning this message in namespace %s:\n\n%s\nTouch your security key when it blinks.\n",
		repo.Repository, fp, pub.Type(), signature.Namespace, indent(string(msg)))

	sig, err := keygenSign(io, key, msg)
	if err != nil {
		return io.fail("SIGN_FAILED", err.Error(), nil)
	}
	v, f := signature.VerifyChange(sig, change)
	if f != nil {
		return io.fail(f.Code, "the new endorsement does not verify: "+f.Text, nil)
	}
	if v.Fingerprint != fp {
		return io.fail(signature.CodeInvalid, "ssh-keygen signed with "+v.Fingerprint+", not "+fp, nil)
	}
	out := filepath.Join(latticeDir, signature.SignersSigFile)
	if err := os.WriteFile(out, sig, 0o644); err != nil {
		return io.fail("SIGN_FAILED", err.Error(), nil)
	}
	outRel, _ := filepath.Rel(top, out)
	if io.JSON {
		return io.printJSON(map[string]interface{}{"contract": "lattice.sign/1", "signature": filepath.ToSlash(outRel),
			"fingerprint": fp, "repository": repo.Repository, "after": after, "signers_digest": signature.SignersDigest(next),
			"added": added, "removed": removed})
	}
	io.printf("wrote %s (%s)\ncommit it in the same commit as the change to allowed_signers and merge\n", filepath.ToSlash(outRel), signature.Short(fp))
	return nil
}

// keygenSign runs ssh-keygen -Y sign over msg in the decision namespace and
// returns the armoured signature. The human touches the key while it runs.
func keygenSign(io *IO, key string, msg []byte) ([]byte, error) {
	tmp, err := os.MkdirTemp("", "lattice-sign-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)
	msgPath := filepath.Join(tmp, "message")
	if err := os.WriteFile(msgPath, msg, 0o600); err != nil {
		return nil, err
	}
	run := exec.Command(sshKeygen, "-Y", "sign", "-n", signature.Namespace, "-f", key, msgPath)
	run.Stdin, run.Stdout, run.Stderr = os.Stdin, io.Err, io.Err
	if err := run.Run(); err != nil {
		return nil, fmt.Errorf("ssh-keygen -Y sign failed: %w", err)
	}
	sig, err := os.ReadFile(msgPath + signature.Ext)
	if err != nil {
		return nil, fmt.Errorf("ssh-keygen wrote no signature: %w", err)
	}
	return sig, nil
}

// signRefusal says why sign must not run here, or "" when it may.
// @enforces confirm:INV-14
func signRefusal(io *IO) (code, why string) {
	if id := results.CIRunID(); id != results.LocalRun {
		return "SIGN_CI", "this is a CI run (" + id + "): a decision is signed by a human at their own terminal, never in CI"
	}
	for _, v := range ciMarkers {
		if val := strings.TrimSpace(os.Getenv(v)); val != "" && !strings.EqualFold(val, "false") && val != "0" {
			return "SIGN_CI", v + " is set: a decision is signed by a human at their own terminal, never in CI"
		}
	}
	if a := io.actor(); schema.IsAgentActor(a) {
		return "SIGN_AGENT", a + " is an agent: only a human signs a decision"
	}
	if !signTerminal() {
		return "SIGN_NO_TTY", "no terminal: sign runs only interactively, with a human present to touch the key"
	}
	return "", ""
}

// findDecision resolves a decision file path, stem or decision/<stem> branch
// name to a file in the working tree.
func findDecision(top, latticeDir, arg string) (string, confirm.Decision, error) {
	stem := strings.TrimSuffix(filepath.Base(strings.TrimPrefix(arg, "decision/")), ".yaml")
	candidates := []string{filepath.Join(latticeDir, filepath.FromSlash(confirm.ApprovalsDir), stem+".yaml"), arg}
	if !filepath.IsAbs(arg) {
		candidates = append(candidates, filepath.Join(top, arg))
	}
	for _, p := range candidates {
		data, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		d, err := confirm.ParseDecision(data)
		if err != nil {
			return "", d, err
		}
		abs, _ := filepath.Abs(p)
		return abs, d, nil
	}
	return "", confirm.Decision{}, fmt.Errorf("no decision file %s in the working tree (looked for %s)", arg, candidates[0])
}

// signingKey finds the private key to sign with and reads its public half.
func signingKey(keyPath string) (string, ssh.PublicKey, error) {
	if keyPath == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", nil, err
		}
		var found []string
		for _, pat := range []string{"id_ed25519_sk*", "id_ecdsa_sk*"} {
			m, _ := filepath.Glob(filepath.Join(home, ".ssh", pat))
			sort.Strings(m)
			for _, p := range m {
				if !strings.HasSuffix(p, ".pub") {
					found = append(found, p)
				}
			}
		}
		if len(found) == 0 {
			return "", nil, fmt.Errorf("no security key in %s (id_ed25519_sk*, id_ecdsa_sk*)", filepath.Join(home, ".ssh"))
		}
		keyPath = found[0]
	}
	pubPath := keyPath
	if !strings.HasSuffix(keyPath, ".pub") {
		pubPath = keyPath + ".pub"
	}
	data, err := os.ReadFile(pubPath)
	if err != nil {
		return "", nil, err
	}
	pub, _, _, _, err := ssh.ParseAuthorizedKey(bytes.TrimSpace(data))
	if err != nil {
		return "", nil, err
	}
	return keyPath, pub, nil
}

func charDevice(f *os.File) bool {
	st, err := f.Stat()
	return err == nil && st.Mode()&os.ModeCharDevice != 0
}

func indent(s string) string {
	return "    " + strings.ReplaceAll(strings.TrimSuffix(s, "\n"), "\n", "\n    ") + "\n"
}
