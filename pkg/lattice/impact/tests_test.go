package impact

import (
	"bytes"
	"reflect"
	"strings"
	"testing"

	"github.com/salahmyn/lattice/pkg/lattice/config"
)

const runnersConfig = `agentic:
  llm:
    enabled: false
break:
  runners:
    - name: vitest
      match: ["apps/web/**/*.test.ts"]
      dir: apps/web
      command: ["pnpm", "vitest", "run", "{files}", "-t", "{filter}"]
    - name: deno
      match: ["services/**/*_test.ts"]
      command: ["deno", "test", "{files}", "--filter", "{filter}", "--junit-path={junit}"]
`

const feesTSFeature = `id: fees
version: 1
status: production
purpose: Charges a fee on each payment.
owners:
  business: payments
  engineering: payments-eng
invariants:
  - id: INV-1
    statement: A fee never exceeds the cap.
  - id: INV-2
    statement: A fee is rounded to cents.
`

const feesTS = `// @module-feature fees

/** @enforces fees:INV-1 */
export function cap(fee: number): number {
  return Math.min(fee, 100);
}

/** @enforces fees:INV-2 */
export function round(fee: number): number {
  return Math.round(fee * 100) / 100;
}
`

func tsTest(inv string, titles ...string) string {
	var b strings.Builder
	b.WriteString("// @module-feature fees\nimport { cap } from './fees';\n")
	for _, title := range titles {
		b.WriteString("\n/** @verifies " + inv + " */\ntest('" + title + "', () => {\n  cap(1);\n});\n")
	}
	return b.String()
}

// runnersRepo commits a project with verifiers of fees:INV-1 under two
// runners and one no runner matches, and a verifier of fees:INV-2.
func runnersRepo(t *testing.T, cfg string) (*repo, string) {
	r := newRepo(t)
	r.write("lattice/config.yaml", cfg)
	r.write("lattice/adapters.yaml", "adapters:\n  typescript:\n    enabled: true\n")
	r.write("lattice/features/fees.yaml", feesTSFeature)
	r.write("apps/web/src/fees.ts", feesTS)
	r.write("apps/web/src/fees.test.ts", tsTest("fees:INV-1", "caps the fee", "rejects a negative amount"))
	r.write("apps/web/src/round.test.ts", tsTest("fees:INV-2", "rounds to cents"))
	r.write("services/fees/cap_test.ts", tsTest("fees:INV-1", "caps on the server"))
	r.write("e2e/fees.spec.ts", tsTest("fees:INV-1", "checkout shows the capped fee"))
	base := r.commit("base")
	r.git("checkout", "-q", "-b", "change")
	return r, base
}

func planOf(t *testing.T, r *repo, base string) TestPlan {
	t.Helper()
	in := r.input(base)
	cfg, err := config.Load(in.Workspace.LatticeDir)
	if err != nil {
		t.Fatal(err)
	}
	in.Break = &cfg.Break
	rep := r.report(in)
	if rep.Tests == nil {
		t.Fatal("no test plan")
	}
	return *rep.Tests
}

// An enforcer change lists the promise's verifiers under each runner whose
// glob matches, with the command filled, and the one no runner matches apart.
// Another promise's verifier is not listed.
// @verifies impact:INV-6
func TestTestPlanByRunner(t *testing.T) {
	r, base := runnersRepo(t, runnersConfig)
	r.write("apps/web/src/fees.ts", strings.Replace(feesTS, "Math.min(fee, 100)", "Math.min(fee, 99)", 1))
	plan := planOf(t, r, base)

	if len(plan.Runners) != 2 {
		t.Fatalf("runners = %+v", plan.Runners)
	}
	byName := map[string]TestRunner{}
	for _, g := range plan.Runners {
		byName[g.Name] = g
	}
	v := byName["vitest"]
	if v.Dir != "apps/web" || !reflect.DeepEqual(v.Files, []string{"src/fees.test.ts"}) ||
		v.Filter != "(caps the fee|rejects a negative amount)$" || !reflect.DeepEqual(v.Promises, []string{"fees:INV-1"}) ||
		v.Command != "pnpm vitest run {files} -t {filter}" ||
		v.Run != "pnpm vitest run src/fees.test.ts -t '(caps the fee|rejects a negative amount)$'" {
		t.Errorf("vitest = %+v", v)
	}
	d := byName["deno"]
	if d.Dir != "" || !reflect.DeepEqual(d.Files, []string{"services/fees/cap_test.ts"}) || d.JUnit != "junit-deno.xml" ||
		!reflect.DeepEqual(d.Argv, []string{"deno", "test", "services/fees/cap_test.ts", "--filter", "(caps on the server)$", "--junit-path=junit-deno.xml"}) {
		t.Errorf("deno = %+v", d)
	}
	if len(plan.Unmatched) != 1 || plan.Unmatched[0].File != "e2e/fees.spec.ts" ||
		plan.Unmatched[0].Reason != ReasonNoRunnerMatches || !reflect.DeepEqual(plan.Unmatched[0].Promises, []string{"fees:INV-1"}) {
		t.Errorf("unmatched = %+v", plan.Unmatched)
	}

	var text bytes.Buffer
	WriteTests(&text, plan)
	want := "(cd apps/web && pnpm vitest run src/fees.test.ts -t '(caps the fee|rejects a negative amount)$')  # vitest: fees:INV-1\n"
	if !strings.Contains(text.String(), want) ||
		!strings.Contains(text.String(), "# unmatched e2e/fees.spec.ts (fees:INV-1): no break.runners entry matches\n") {
		t.Errorf("text:\n%s", text.String())
	}
}

// A change to a verifier file alone touches its promise: that file's
// verifiers, and the promise's other verifiers, are listed.
// @verifies impact:INV-6
func TestTestPlanChangedVerifier(t *testing.T) {
	r, base := runnersRepo(t, runnersConfig)
	r.write("apps/web/src/round.test.ts", tsTest("fees:INV-2", "rounds to cents", "rounds half up"))
	plan := planOf(t, r, base)
	if len(plan.Runners) != 1 || plan.Runners[0].Name != "vitest" ||
		!reflect.DeepEqual(plan.Runners[0].Files, []string{"src/round.test.ts"}) ||
		!reflect.DeepEqual(plan.Runners[0].Promises, []string{"fees:INV-2"}) || len(plan.Unmatched) != 0 {
		t.Fatalf("plan = %+v", plan)
	}
}

// With no runner configured, every verifier is unmatched with that reason;
// the list is never silently empty.
// @verifies impact:INV-6
func TestTestPlanNoRunners(t *testing.T) {
	r, base := runnersRepo(t, "agentic:\n  llm:\n    enabled: false\n")
	r.write("apps/web/src/fees.ts", strings.Replace(feesTS, "Math.min(fee, 100)", "Math.min(fee, 99)", 1))
	plan := planOf(t, r, base)
	if len(plan.Runners) != 0 || len(plan.Unmatched) != 3 {
		t.Fatalf("plan = %+v", plan)
	}
	for _, u := range plan.Unmatched {
		if u.Reason != ReasonNoRunners || !reflect.DeepEqual(u.Promises, []string{"fees:INV-1"}) {
			t.Errorf("unmatched = %+v", u)
		}
	}
	var text bytes.Buffer
	WriteTests(&text, plan)
	if strings.Count(text.String(), "# unmatched ") != 3 {
		t.Errorf("text:\n%s", text.String())
	}
}
