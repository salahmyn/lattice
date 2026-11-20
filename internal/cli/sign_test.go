package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"github.com/salahmyn/lattice/pkg/lattice/check"
	"github.com/salahmyn/lattice/pkg/lattice/confirm"
	"github.com/salahmyn/lattice/pkg/lattice/confirm/signature"
	"github.com/salahmyn/lattice/pkg/lattice/confirm/signature/sigtest"
	"github.com/salahmyn/lattice/skills"
)

// signFixture is the check fixture with a GitHub origin and the decision
// approving brd.shop v1 in the working tree.
type signFixture struct {
	*checkFixture
	d    confirm.Decision
	file string // the decision file, from the repository root
}

func newSignFixture(t *testing.T) *signFixture {
	t.Helper()
	clearCIEnv(t)
	for _, v := range ciMarkers {
		t.Setenv(v, "")
	}
	t.Setenv("GITHUB_REPOSITORY", "")
	f := &signFixture{checkFixture: newCheckFixture(t)}
	f.git("remote", "add", "origin", "git@github.com:"+sigtest.Repository+".git")
	f.d = confirm.Decision{Action: confirm.ActionApprove, ID: "brd.shop", Version: 1, PacketHash: "sha256:5e1f04ab9c",
		PreparedBy: "Ana <ana@example.com>", PreparedAt: "2026-01-01T00:00:00Z"}
	body, err := f.d.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	f.file = "lattice/" + f.d.RelPath()
	f.write(f.file, string(body))
	return f
}

func setTerminal(t *testing.T, tty bool) {
	t.Helper()
	old := signTerminal
	signTerminal = func() bool { return tty }
	t.Cleanup(func() { signTerminal = old })
}

// keyFiles writes a key's public half (and a placeholder private file) where
// --key can find it.
func keyFiles(t *testing.T, k *sigtest.Key) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "id_test_sk")
	if err := os.WriteFile(p, []byte("private key placeholder\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p+".pub", []byte(k.AuthorizedKey()+" test\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func failCode(t *testing.T, out string) string {
	t.Helper()
	var e jsonError
	if err := json.Unmarshal([]byte(out), &e); err != nil {
		t.Fatalf("not a JSON error: %s", out)
	}
	return e.Code
}

// Acceptance 8: sign refuses without a terminal, under CI and for an agent,
// before it looks at a key or runs anything; and it refuses a software key.
// @verifies confirm:INV-14
func TestSignRefuses(t *testing.T) {
	f := newSignFixture(t)
	old := sshKeygen
	sshKeygen = filepath.Join(t.TempDir(), "must-not-run")
	t.Cleanup(func() { sshKeygen = old })
	sk := keyFiles(t, sigtest.NewKey(t, sigtest.SKEd25519))

	setTerminal(t, false)
	out, err := run(t, f.dir, newSignCommand, f.file, "--key", sk)
	if err == nil || failCode(t, out) != "SIGN_NO_TTY" {
		t.Errorf("no terminal: %v %s", err, out)
	}

	setTerminal(t, true)
	for _, env := range []string{"GITHUB_RUN_ID", "CI", "GITHUB_ACTIONS", "GITLAB_CI"} {
		t.Setenv(env, "1")
		out, err := run(t, f.dir, newSignCommand, f.file, "--key", sk)
		if err == nil || failCode(t, out) != "SIGN_CI" {
			t.Errorf("%s set: %v %s", env, err, out)
		}
		t.Setenv(env, "")
	}

	t.Setenv("LATTICE_ACTOR", "agent:impl-1")
	io := &IO{Repo: f.dir, JSON: true}
	if code, _ := signRefusal(io); code != "SIGN_AGENT" {
		t.Errorf("an agent actor: %s", code)
	}
	t.Setenv("LATTICE_ACTOR", "")

	soft := keyFiles(t, sigtest.NewKey(t, sigtest.Software))
	out, err = run(t, f.dir, newSignCommand, f.file, "--key", soft)
	if err == nil || failCode(t, out) != signature.CodeSoftwareKey {
		t.Errorf("a software key: %v %s", err, out)
	}
}

// sign prints the decision, runs ssh-keygen -Y sign in the lattice-decision
// namespace, verifies what it wrote and puts <stem>.sig beside the decision.
// The ssh-keygen here is a stand-in that returns a signature the test made
// with a security key over the same message.
func TestSignWritesSignature(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the ssh-keygen stand-in is a shell script")
	}
	f := newSignFixture(t)
	setTerminal(t, true)
	k := sigtest.NewKey(t, sigtest.SKEd25519)
	made := filepath.Join(t.TempDir(), "made.sig")
	if err := os.WriteFile(made, k.SignTuple(t, confirm.SignatureTuple(f.d, sigtest.Repository), sigtest.Options{}), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(t.TempDir(), "ssh-keygen")
	script := "#!/bin/sh\necho \"$@\" > \"$0.args\"\nfor a; do last=$a; done\ncp \"" + made + "\" \"$last.sig\"\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	old := sshKeygen
	sshKeygen = bin
	t.Cleanup(func() { sshKeygen = old })

	key := keyFiles(t, k)
	out, err := run(t, f.dir, newSignCommand, f.d.Stem(), "--key", key)
	if err != nil {
		t.Fatalf("sign: %v\n%s", err, out)
	}
	for _, want := range []string{"brd.approve brd.shop v1", f.d.PacketHash, sigtest.Repository, "lattice-decision/v1"} {
		if !strings.Contains(out, want) {
			t.Errorf("the summary must show %q:\n%s", want, out)
		}
	}
	args, _ := os.ReadFile(bin + ".args")
	if !strings.HasPrefix(string(args), "-Y sign -n lattice-decision -f "+key+" ") {
		t.Errorf("ssh-keygen ran with %s", args)
	}
	sig, err := os.ReadFile(filepath.Join(f.dir, filepath.FromSlash(signature.SigPath(f.file))))
	if err != nil {
		t.Fatalf("no signature beside the decision: %v", err)
	}
	if _, fail := signature.Verify(sig, confirm.SignatureTuple(f.d, sigtest.Repository)); fail != nil {
		t.Errorf("the written signature must verify: %+v", fail)
	}
}

// Acceptance 9: check re-verifies a signature verdict from the tree on every
// run. A confirm.json with an intact stamp that claims a signature verdict
// the tree does not back is ignored; the same claim with the signature in
// the tree confirms.
// @verifies confirm:INV-13
func TestCheckIgnoresHandWrittenSignatureVerdict(t *testing.T) {
	f := newSignFixture(t)
	k := sigtest.NewKey(t, sigtest.SKEd25519)
	f.write("lattice/allowed_signers", k.AllowedLine("ana"))
	f.commit("enrol ana and add the decision, unsigned")
	f.git("checkout", "-q", "-b", "edit")
	f.write("shop/shop.go", strings.Replace(shopGo(true), "n > 0", "n >= 1", 1))
	f.commit("edit")
	head := f.git("rev-parse", "HEAD")
	latticeDir := filepath.Join(f.dir, "lattice")

	claim := func() {
		t.Helper()
		cf := confirm.File{Contract: confirm.Schema, Commit: head, Branch: "main", Records: []confirm.Record{{
			ID: "brd.shop", Version: 1, Action: confirm.ActionApprove, State: confirm.StateConfirmed, Via: confirm.ViaSignature,
			ApproverLogin: "ana", Fingerprint: k.Fingerprint(), DecisionPath: f.file,
			Assurance: confirm.Assurance{State: confirm.StateConfirmed, Honesty: "fact", Code: signature.CodeSigned},
		}}}
		if err := confirm.SaveCache(latticeDir, confirm.Seal(cf, confirm.Stamp{CIRun: "4812", Platform: "github"})); err != nil {
			t.Fatal(err)
		}
	}
	confirmed := func() check.Assurance {
		t.Helper()
		rep, _ := f.check("--base", "main", "--ci")
		for _, r := range rep.Promises {
			if r.Promise == "shop:INV-1" {
				return r.Signals.Confirmed
			}
		}
		t.Fatalf("shop:INV-1 is not on the page: %+v", rep.Promises)
		return check.Assurance{}
	}

	claim()
	if c := confirmed(); c.State == check.Pass || !strings.Contains(c.Text, signature.CodeNotReverified) {
		t.Errorf("a claimed signature verdict with no signature in the tree must be ignored: %+v", c)
	}

	f.git("checkout", "-q", "main")
	f.write(signature.SigPath(f.file), string(k.SignTuple(t, confirm.SignatureTuple(f.d, sigtest.Repository), sigtest.Options{})))
	f.commit("sign")
	f.git("checkout", "-q", "edit")
	f.git("merge", "-q", "main", "-m", "sync")
	head = f.git("rev-parse", "HEAD")
	claim()
	if c := confirmed(); c.State != check.Pass || !strings.Contains(c.Text, "human (signed by ana, "+signature.Short(k.Fingerprint())+")") {
		t.Errorf("control: the same claim backed by the tree confirms, with its method: %+v", c)
	}
}

// sign is not offered to agents: no MCP tool runs it, and no shipped skill
// tells an agent to.
// @verifies confirm:INV-14
func TestSignIsNotExposedToAgents(t *testing.T) {
	src, err := os.ReadFile(filepath.Join("..", "..", "lattice-mcp", "src", "tools", "index.ts"))
	if err != nil {
		t.Fatal(err)
	}
	if regexp.MustCompile(`args:\s*\[\s*"sign"`).Match(src) || strings.Contains(string(src), `"sign"`) {
		t.Error("an MCP tool must never run `lattice sign`")
	}
	dir := t.TempDir()
	if err := skills.ExportAll(dir); err != nil {
		t.Fatal(err)
	}
	mention := regexp.MustCompile("lattice\\s+sign(\\s|`|$)")
	_ = filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() {
			if data, _ := os.ReadFile(p); mention.Match(data) {
				t.Errorf("%s mentions `lattice sign`; skills must not offer it", p)
			}
		}
		return nil
	})
}
