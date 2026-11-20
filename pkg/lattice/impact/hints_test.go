package impact

import (
	"bytes"
	"reflect"
	"strings"
	"testing"
)

// checkoutSpec is an end-to-end spec that drives the built app. It imports
// nothing from the source tree and names none of its symbols, so coverage,
// the call graph and the neighbour heuristic cannot find it.
func checkoutSpec(step string) string {
	return `import { test, expect } from '@playwright/test';

test('pays for the order', async ({ page }) => {
  await page.goto('/pay');
  await expect(page.getByRole('status')).toHaveText('` + step + `');
});
`
}

const checkoutSpecFile = "e2e/checkout.spec.ts"

// shortSHA is the abbreviated hash git log prints for a commit.
func shortSHA(r *repo, sha string) string { return r.git("log", "-1", "--format=%h", sha) }

// specFQN is the graph FQN of the checkout spec's one test.
func specFQN(t *testing.T, r *repo) string {
	return testFQN(t, r, checkoutSpecFile, "pays for the order")
}

// hintedOnly asserts the plan selects only test via a hint, lists the symbol
// under hinted and nothing under uncovered, and returns the test's why.
func hintedOnly(t *testing.T, plan TestPlan, test, sym, via string) []string {
	t.Helper()
	if len(plan.Runners) != 0 || len(plan.Uncovered) != 0 || len(plan.Unmatched) != 1 ||
		!reflect.DeepEqual(plan.Unmatched[0].Tests, []string{test}) {
		t.Fatalf("plan = %+v", plan)
	}
	if len(plan.Hinted) != 1 || plan.Hinted[0].Symbol != sym || plan.Hinted[0].Via != via ||
		!reflect.DeepEqual(plan.Hinted[0].Tests, []string{test}) {
		t.Fatalf("hinted = %+v", plan.Hinted)
	}
	return plan.Unmatched[0].Why[test]
}

// A spec that changed in the same commits as the touched file is selected,
// labelled co_change, with the commits it shared; one that shared a single
// commit is not (min_commits 2). The symbol is listed under hinted, not
// uncovered, and the text says so.
// @verifies impact:INV-8
func TestTestPlanUnguardedByCoChange(t *testing.T) {
	r, _ := runnersRepo(t, runnersConfig)
	r.git("checkout", "-q", "main")
	r.write("apps/web/src/total.ts", totalTS)
	r.write(checkoutSpecFile, checkoutSpec("paid"))
	r.write("e2e/journey.spec.ts", homeSpec)
	first := r.commit("add total and its journeys")
	r.write("apps/web/src/total.ts", totalTS+"\nexport const currency = 'EUR';\n")
	r.write(checkoutSpecFile, checkoutSpec("paid in EUR"))
	second := r.commit("show the currency")
	r.write("apps/web/src/other.ts", "export const other = 1;\n")
	base := r.commit("unrelated")
	r.git("checkout", "-q", "-B", "change")
	r.write("apps/web/src/total.ts", strings.Replace(totalTS, "amount + 1", "amount + 2", 1)+"\nexport const currency = 'EUR';\n")

	plan := planWith(t, r, base, nil, true)
	spec := specFQN(t, r)
	why := hintedOnly(t, plan, spec, totalSym, ViaCoChange)
	want := "unguarded " + totalSym + " via co_change (hint: " + checkoutSpecFile + " changed with apps/web/src/total.ts in 2 commits (" +
		shortSHA(r, second) + ", " + shortSHA(r, first) + "))"
	if !reflect.DeepEqual(why, []string{want}) {
		t.Errorf("why = %q, want %q", why, want)
	}
	var text bytes.Buffer
	WriteTests(&text, plan)
	if !strings.Contains(text.String(), "# hinted: touched lines no promise guards whose tests only a hint (co_change, mentions) found\n"+
		"#   apps/web/src/total.ts:3-5  "+totalSym+" via co_change\n") || strings.Contains(text.String(), "uncovered") {
		t.Errorf("text:\n%s", text.String())
	}
}

// A spec edited in the change itself counts as changed with the touched
// file, though the two never shared a commit before.
// @verifies impact:INV-8
func TestTestPlanUnguardedByCoChangeInRange(t *testing.T) {
	r, base := apartRepo(t, checkoutSpecFile, checkoutSpec("paid"))
	r.write(checkoutSpecFile, checkoutSpec("paid in full"))
	r.commit("change total and its journey")

	plan := planWith(t, r, base, nil, true)
	why := hintedOnly(t, plan, specFQN(t, r), totalSym, ViaCoChange)
	want := "unguarded " + totalSym + " via co_change (hint: " + checkoutSpecFile + " changed with apps/web/src/total.ts in this change)"
	if !reflect.DeepEqual(why, []string{want}) {
		t.Errorf("why = %q, want %q", why, want)
	}
}

// bannerTS holds a component the spec names but never imports.
const bannerTS = `// @module-feature fees

export function TotalBanner(amount: number): string {
  return 'Total: ' + amount;
}
`

const bannerSym = "apps.web.src.banner.TotalBanner"

// A spec whose text names the touched symbol as a whole word is selected,
// labelled mentions, with the line that names it.
// @verifies impact:INV-8
func TestTestPlanUnguardedByMention(t *testing.T) {
	r, _ := runnersRepo(t, runnersConfig)
	r.git("checkout", "-q", "main")
	r.write(checkoutSpecFile, checkoutSpec("paid")+`
test('shows the banner', async ({ page }) => {
  await expect(page.getByTestId('TotalBanner')).toBeVisible();
});
`)
	r.commit("add the journey")
	r.write("apps/web/src/banner.ts", bannerTS)
	base := r.commit("add the banner")
	r.git("checkout", "-q", "-B", "change")
	r.write("apps/web/src/banner.ts", strings.Replace(bannerTS, "'Total: '", "'Total due: '", 1))

	plan := planWith(t, r, base, nil, true)
	pays, banner := specFQN(t, r), testFQN(t, r, checkoutSpecFile, "shows the banner")
	if len(plan.Uncovered) != 0 || len(plan.Unmatched) != 1 || !reflect.DeepEqual(plan.Unmatched[0].Tests, sortedPair(pays, banner)) ||
		len(plan.Hinted) != 1 || plan.Hinted[0].Via != ViaMentions || plan.Hinted[0].Symbol != bannerSym {
		t.Fatalf("plan = %+v", plan)
	}
	want := []string{"unguarded " + bannerSym + " via mentions (hint: " + checkoutSpecFile + `:9 mentions "TotalBanner")`}
	for _, test := range []string{pays, banner} {
		if got := plan.Unmatched[0].Why[test]; !reflect.DeepEqual(got, want) {
			t.Errorf("why[%s] = %q, want %q", test, got, want)
		}
	}
}

func sortedPair(a, b string) []string {
	if b < a {
		a, b = b, a
	}
	return []string{a, b}
}

// widgetsTS has a name too short to look for (run), a single plain word
// (summary), a common compound (onClick), a specific name (TotalBanner) the
// spec only names in other spellings, and one (PriceTag) the spec names
// exactly.
const widgetsTS = `// @module-feature fees

export function run(n: number): number {
  return n + 1;
}

export function summary(n: number): number {
  return n + 1;
}

export function onClick(n: number): number {
  return n + 1;
}

export function TotalBanner(n: number): number {
  return n + 1;
}

export function PriceTag(n: number): number {
  return n + 1;
}
`

// A short name, a plain word, a common compound, a different case and a
// longer identifier that contains the name are not mentions: those four
// symbols stay uncovered, while the one the spec names exactly is hinted.
// @verifies impact:INV-8
func TestTestPlanUnguardedMentionConservative(t *testing.T) {
	r, _ := runnersRepo(t, runnersConfig)
	r.git("checkout", "-q", "main")
	r.write(checkoutSpecFile, `import { test } from '@playwright/test';

test('pays for the order', async ({ page }) => {
  await run(page);
  await page.getByTestId('summary').click();
  await onClick(page);
  await page.getByTestId('totalBanner').click();
  await page.getByTestId('TotalBannerWide').click();
  await page.getByTestId('MyTotalBanner').click();
  await page.getByTestId('PriceTag').click();
});
`)
	r.commit("add the journey")
	r.write("apps/web/src/widgets.ts", widgetsTS)
	base := r.commit("add widgets")
	r.git("checkout", "-q", "-B", "change")
	r.write("apps/web/src/widgets.ts", strings.ReplaceAll(widgetsTS, "n + 1", "n + 2"))

	plan := planWith(t, r, base, nil, false)
	var got []string
	for _, u := range plan.Uncovered {
		got = append(got, u.Symbol)
	}
	want := []string{"apps.web.src.widgets.TotalBanner", "apps.web.src.widgets.onClick", "apps.web.src.widgets.run", "apps.web.src.widgets.summary"}
	if !reflect.DeepEqual(got, want) || len(plan.Hinted) != 1 || plan.Hinted[0].Symbol != "apps.web.src.widgets.PriceTag" ||
		plan.Hinted[0].Via != ViaMentions {
		t.Fatalf("plan = %+v", plan)
	}
}

// A route an entry point declares for the touched file counts as a mention
// when the spec visits it as a whole path.
// @verifies impact:INV-8
func TestTestPlanUnguardedByRouteMention(t *testing.T) {
	r, _ := runnersRepo(t, runnersConfig)
	r.git("checkout", "-q", "main")
	r.write("lattice/entry-points/http.get.cart.yaml", `id: http.get.cart
version: 1
status: production
kind: http_route
trigger:
  method: GET
  path: /cart/summary
handler:
  symbol: apps.web.src.cart.handle
  file: apps/web/src/cart.ts
`)
	r.write(checkoutSpecFile, `import { test } from '@playwright/test';

test('pays for the order', async ({ page }) => {
  await page.goto('/cart/summary-old');
  await page.goto('/cart/summary');
});
`)
	r.commit("add the journey")
	r.write("apps/web/src/cart.ts", "// @module-feature fees\n\nexport function handle(n: number): number {\n  return n + 1;\n}\n")
	base := r.commit("add the cart")
	r.git("checkout", "-q", "-B", "change")
	r.write("apps/web/src/cart.ts", "// @module-feature fees\n\nexport function handle(n: number): number {\n  return n + 2;\n}\n")

	plan := planWith(t, r, base, nil, false)
	why := hintedOnly(t, plan, specFQN(t, r), "apps.web.src.cart.handle", ViaMentions)
	want := "unguarded apps.web.src.cart.handle via mentions (hint: " + checkoutSpecFile + `:5 mentions "/cart/summary")`
	if !reflect.DeepEqual(why, []string{want}) {
		t.Errorf("why = %q, want %q", why, want)
	}
}

// The co-change window is bounded: a commit older than the last
// break.co_change.commits, or one that changes more than max_files files, is
// not read, and a test file needs min_commits shared commits in it.
// @verifies impact:INV-8
func TestTestPlanCoChangeWindow(t *testing.T) {
	r, _ := runnersRepo(t, runnersConfig)
	r.git("checkout", "-q", "main")
	r.write("apps/web/src/total.ts", totalTS)
	r.write(checkoutSpecFile, checkoutSpec("paid"))
	r.commit("add total and its journey")
	r.write("apps/web/src/total.ts", totalTS+"\nexport const currency = 'EUR';\n")
	r.write(checkoutSpecFile, checkoutSpec("paid in EUR"))
	r.commit("show the currency")
	for _, n := range []string{"1", "2"} {
		r.write("apps/web/src/other.ts", "export const other = "+n+";\n")
		r.commit("unrelated " + n)
	}
	base := r.git("rev-parse", "HEAD")
	r.git("checkout", "-q", "-B", "change")
	r.write("apps/web/src/total.ts", strings.Replace(totalTS, "amount + 1", "amount + 2", 1)+"\nexport const currency = 'EUR';\n")

	window := func(block string) TestPlan {
		r.write("lattice/config.yaml", runnersConfig+"  co_change:\n"+block)
		return planWith(t, r, base, nil, false)
	}
	uncovered := func(p TestPlan) bool {
		return len(p.Hinted) == 0 && len(p.Unmatched) == 0 && len(p.Uncovered) == 1 && p.Uncovered[0].Symbol == totalSym
	}
	if p := window("    commits: 3\n"); !uncovered(p) {
		t.Errorf("commits: 3 reached past the window: %+v", p)
	}
	if p := window("    commits: 4\n    max_files: 1\n"); !uncovered(p) {
		t.Errorf("max_files: 1 read a commit of two files: %+v", p)
	}
	if p := window("    commits: 3\n    min_commits: 1\n"); len(p.Hinted) != 1 || len(p.Uncovered) != 0 {
		t.Errorf("min_commits: 1 missed the one shared commit in the window: %+v", p)
	}
	if p := window("    commits: 4\n"); len(p.Hinted) != 1 || p.Hinted[0].Via != ViaCoChange || len(p.Uncovered) != 0 {
		t.Errorf("commits: 4 missed the two shared commits: %+v", p)
	}
}

// A change of more than max_files files is not one unit: its commits count
// one by one, so a spec edited in another commit of the change is not
// selected, while one edited in the same commit as the touched file is.
// @verifies impact:INV-8
func TestTestPlanCoChangeLargeChange(t *testing.T) {
	for _, together := range []bool{true, false} {
		r, base := apartRepo(t, checkoutSpecFile, checkoutSpec("paid"))
		r.write("lattice/config.yaml", runnersConfig+"  co_change:\n    max_files: 3\n    min_commits: 1\n")
		if together {
			r.write(checkoutSpecFile, checkoutSpec("paid in full"))
		}
		first := r.commit("change total")
		r.write("apps/web/src/a.ts", "export const a = 1;\n")
		r.write("apps/web/src/b.ts", "export const b = 1;\n")
		if !together {
			r.write(checkoutSpecFile, checkoutSpec("paid in full"))
		}
		r.commit("more")

		plan := planWith(t, r, base, nil, false)
		if !together {
			if len(plan.Hinted) != 0 || len(plan.Uncovered) != 1 {
				t.Errorf("a spec from another commit of a large change was selected: %+v", plan)
			}
			continue
		}
		why := hintedOnly(t, plan, specFQN(t, r), totalSym, ViaCoChange)
		want := "unguarded " + totalSym + " via co_change (hint: " + checkoutSpecFile + " changed with apps/web/src/total.ts in 1 commit (" +
			shortSHA(r, first) + "))"
		if !reflect.DeepEqual(why, []string{want}) {
			t.Errorf("why = %q, want %q", why, want)
		}
	}
}

// A spec that changed with the touched file only in large commits scores
// low (each shared commit adds 1/(its files)) and is not hinted, though it
// shares min_commits commits; one that shared small commits, or that the
// change itself edits, is.
// @verifies impact:INV-8
func TestTestPlanCoChangeWeighsCommitSize(t *testing.T) {
	const wide = "e2e/wallet.spec.ts"
	setup := func(t *testing.T) (*repo, string) {
		r, _ := runnersRepo(t, runnersConfig)
		r.git("checkout", "-q", "main")
		r.write(checkoutSpecFile, checkoutSpec("paid"))
		r.write(wide, homeSpec)
		r.commit("add the journeys")
		r.write("apps/web/src/total.ts", totalTS)
		r.commit("add total")
		for round, step := range []string{"paid in EUR", "paid in full"} {
			// A sweeping commit: total.ts, the wide spec and ten other files.
			r.write("apps/web/src/total.ts", totalTS+strings.Repeat("\n", round+1))
			r.write(wide, homeSpec+strings.Repeat("\n", round+1))
			for i := 0; i < 10; i++ {
				r.write("apps/web/src/orders/o"+string(rune('a'+i))+".ts", "export const n = "+string(rune('0'+round))+";\n")
			}
			r.commit("sweep " + step)
			// A small commit: total.ts and the checkout spec alone.
			r.write("apps/web/src/total.ts", totalTS+strings.Repeat("\n", round+3))
			r.write(checkoutSpecFile, checkoutSpec(step))
			r.commit("show " + step)
		}
		base := r.git("rev-parse", "HEAD")
		r.git("checkout", "-q", "-B", "change")
		r.write("apps/web/src/total.ts", strings.Replace(totalTS, "amount + 1", "amount + 2", 1))
		return r, base
	}

	r, base := setup(t)
	plan := planWith(t, r, base, nil, false)
	pays := specFQN(t, r)
	why := hintedOnly(t, plan, pays, totalSym, ViaCoChange)
	if !strings.Contains(why[0], checkoutSpecFile+" changed with apps/web/src/total.ts in 2 commits") {
		t.Errorf("why = %q", why)
	}

	// The change itself edits the wide spec: that is enough on its own.
	r, base = setup(t)
	r.write(wide, homeSpec+"\n// edited with total\n")
	plan = planWith(t, r, base, nil, false)
	home := testFQN(t, r, wide, "opens the home page")
	if got := testsOf(plan); !reflect.DeepEqual(got, sortedPair(pays, home)) {
		t.Fatalf("tests = %v, want the checkout and the edited wide spec", got)
	}
	for _, u := range plan.Unmatched {
		if u.File == wide && !strings.Contains(u.Why[home][0], "in this change") {
			t.Errorf("why[%s] = %q", home, u.Why[home])
		}
	}

	// min_score is configurable: a low one lets the sweeping commits count.
	r, base = setup(t)
	r.write("lattice/config.yaml", runnersConfig+"  co_change:\n    min_score: 0.1\n")
	if got := testsOf(planWith(t, r, base, nil, false)); !reflect.DeepEqual(got, sortedPair(pays, home)) {
		t.Errorf("min_score 0.1: tests = %v", got)
	}
}
