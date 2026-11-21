package cli

import (
	"os/exec"
	"testing"

	"github.com/salahmyn/lattice/pkg/lattice/breaktest"
)

// twinGuard is one promise's enforcer, copied word for word into two places:
// a client module and a shared one. Only the client copy has a test that
// notices it breaking.
const twinGuard = `/** @module-feature orders */

/** @enforces orders:INV-1 */
export function positive(n) {
  return n > 0;
}
`

// twinRunner fails only when the client copy is broken.
const twinRunner = `if grep -q 'return undefined' src/client/guard.js; then echo 'FAIL client rejects zero'; exit 1; fi
`

const twinFeature = `id: orders
version: 1
status: production
purpose: |
  One promise kept by the same code in two places.
owners:
  business: nobody
  engineering: nobody
invariants:
  - id: INV-1
    statement: Zero is not positive.
`

const twinTest = `/** @module-feature orders */
import test from 'node:test';
import { positive } from '../src/client/guard.js';

/** @verifies orders:INV-1 */
test('client rejects zero', () => {
  positive(0);
});
`

func twinRuns(t *testing.T, dir string) map[string]breaktest.Run {
	t.Helper()
	res := runBreakExit(t, dir, true)
	if res.code != 0 {
		t.Fatalf("break: exit %d\n%s%s", res.code, res.out, res.err)
	}
	out := map[string]breaktest.Run{}
	for _, r := range res.doc.Report.Runs {
		out[r.EnforcerFile] = r
	}
	return out
}

// Two enforcers with the same source, the same promise and so the same
// verifiers used to share one cache key: the second run on the same tree
// handed one enforcer the other's cached outcome (a kill quoting a test that
// cannot import it). Each must get back its own.
// @verifies breaktest:INV-16
func TestBreakCacheKeepsTwinEnforcersApart(t *testing.T) {
	for _, tool := range []string{"git", "sh"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skip(tool + " not on PATH")
		}
	}
	dir := t.TempDir()
	files := map[string]string{
		".gitignore":                   "lattice/.cache/\nlattice/lattice.json\n",
		"package.json":                 "{\"type\":\"module\"}\n",
		"src/client/guard.js":          twinGuard,
		"src/shared/guard.js":          twinGuard,
		"tests/guard.test.js":          twinTest,
		"run.sh":                       twinRunner,
		"lattice/config.yaml":          "break:\n  runners:\n    - name: file-runner\n      match: [\"tests/**\"]\n      command: [\"sh\", \"run.sh\", \"{files}\"]\n",
		"lattice/features/orders.yaml": twinFeature,
	}
	for name, body := range files {
		writeFixtureFile(t, dir, name, body)
	}
	runGit(t, dir, "init", "-q")
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-q", "-m", "fixture")

	first := twinRuns(t, dir)
	client, shared := first["src/client/guard.js"], first["src/shared/guard.js"]
	if client.Outcome != breaktest.Killed || shared.Outcome != breaktest.Survived {
		t.Fatalf("first run: want client killed and shared survived, got client %+v, shared %+v", client, shared)
	}
	if client.EnforcerHash != shared.EnforcerHash {
		t.Fatalf("the fixture needs twins with one source hash, got %s and %s", client.EnforcerHash, shared.EnforcerHash)
	}

	second := twinRuns(t, dir)
	for file, want := range map[string]string{"src/client/guard.js": breaktest.Killed, "src/shared/guard.js": breaktest.Survived} {
		r := second[file]
		if !r.Cached || r.Outcome != want {
			t.Errorf("%s on the same tree: want %s (cached), got %s cached=%v reason %q", file, want, r.Outcome, r.Cached, r.Reason)
		}
	}
}
