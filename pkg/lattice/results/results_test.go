package results

import "testing"

const sampleJUnit = `<?xml version="1.0" encoding="UTF-8"?>
<testsuites>
  <testsuite name="journey">
    <testcase classname="test.journey.us1" name="lists in insertion order"></testcase>
    <testcase classname="AddTodoHandler" name="execute appends"></testcase>
    <testcase classname="CompleteTodoHandler" name="idempotent">
      <failure message="expected done"></failure>
    </testcase>
    <testcase classname="ListTodos" name="skipped case">
      <skipped></skipped>
    </testcase>
  </testsuite>
</testsuites>`

func TestParseJUnitOutcomes(t *testing.T) {
	set, err := ParseJUnit([]byte(sampleJUnit))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	cases := []struct {
		ref  string
		want Outcome
	}{
		{"test.journey.us1.lists in insertion order", Pass},
		{"AddTodoHandler.execute appends", Pass},
		{"CompleteTodoHandler.idempotent", Fail},
		{"ListTodos.skipped case", Skip},
	}
	for _, c := range cases {
		got, ok := set.Lookup(c.ref)
		if !ok {
			t.Errorf("Lookup(%q): not found", c.ref)
			continue
		}
		if got != c.want {
			t.Errorf("Lookup(%q) = %s, want %s", c.ref, got, c.want)
		}
	}
}

func TestLookupTrailingSegment(t *testing.T) {
	set, _ := ParseJUnit([]byte(sampleJUnit))
	// A lattice FQN that differs in prefix but shares the trailing method
	// should still resolve.
	if o, ok := set.Lookup("src.features.complete.CompleteTodoHandler.idempotent"); !ok || o != Fail {
		t.Errorf("trailing-segment lookup = (%s,%v), want (fail,true)", o, ok)
	}
}

func TestLookupMissing(t *testing.T) {
	set, _ := ParseJUnit([]byte(sampleJUnit))
	if _, ok := set.Lookup("nothing.here"); ok {
		t.Error("expected miss for unknown test")
	}
	var empty Set
	if _, ok := empty.Lookup("anything"); ok {
		t.Error("empty set should never match")
	}
}

func TestParseBareTestsuite(t *testing.T) {
	bare := `<testsuite name="x"><testcase classname="A" name="b"></testcase></testsuite>`
	set, err := ParseJUnit([]byte(bare))
	if err != nil {
		t.Fatalf("parse bare: %v", err)
	}
	if o, ok := set.Lookup("A.b"); !ok || o != Pass {
		t.Errorf("bare suite lookup = (%s,%v), want (pass,true)", o, ok)
	}
}

// TestLookupVitestItBlock joins a vitest report (classname = file path, name =
// "describe > it") to the FQN the TypeScript adapter gives an it() block.
func TestLookupVitestItBlock(t *testing.T) {
	const report = `<testsuites><testsuite name="src/kid/screenTime.test.ts">
<testcase classname="src/kid/screenTime.test.ts" name="screen time &gt; counts watched seconds against the daily limit"/>
<testcase classname="src/kid/screenTime.test.ts" name="screen time &gt; stores usage per kid: saving one kid never changes another kid's remaining time"><failure/></testcase>
</testsuite></testsuites>`
	set, err := ParseJUnit([]byte(report))
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		ref  string
		want Outcome
	}{
		{"apps.client.src.kid.screenTime.test.test.counts-watched-seconds-against-the-daily-limit", Pass},
		{"apps.client.src.kid.screenTime.test.test.stores-usage-per-kid-saving-one-kid-never-changes-another-kid-s-remaining-time", Fail},
	}
	for _, c := range cases {
		if got, ok := set.Lookup(c.ref); !ok || got != c.want {
			t.Errorf("Lookup(%q) = (%s,%v), want (%s,true)", c.ref, got, ok, c.want)
		}
	}
}
