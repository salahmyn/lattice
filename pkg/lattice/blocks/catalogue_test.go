package blocks

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func readFixture(t *testing.T, rel string) File {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "shop", rel))
	if err != nil {
		t.Fatal(err)
	}
	return File{Path: rel, Data: data}
}

func TestParseCatalogue(t *testing.T) {
	c, ok, err := Parse(readFixture(t, "frontend/cart.json"))
	if err != nil || !ok {
		t.Fatalf("Parse: ok=%v err=%v", ok, err)
	}
	if c.Area != "cart" || len(c.Nodes) != 4 || c.Path != "frontend/cart.json" {
		t.Fatalf("catalogue = %+v", c)
	}
	n := c.Nodes[2]
	if n.API == nil || n.API.Method != "POST" || n.Tests["react"][0] != "web/cart.test.ts > checkout" {
		t.Errorf("node = %+v", n)
	}
	if !c.Nodes[3].Next[0].Loop {
		t.Error("loop flag lost")
	}
}

func TestParseSkipsNonCatalogue(t *testing.T) {
	_, ok, err := Parse(readFixture(t, "frontend/notes.json"))
	if err != nil || ok {
		t.Fatalf("notes.json: ok=%v err=%v, want ok=false err=nil", ok, err)
	}
	_, ok, err = Parse(File{Path: "list.json", Data: []byte(`[1,2]`)})
	if err != nil || ok {
		t.Fatalf("array: ok=%v err=%v, want ok=false err=nil", ok, err)
	}
}

func TestParseRejectsBadCatalogue(t *testing.T) {
	cases := map[string]string{
		"not JSON":      `{"area":`,
		"no kind":       `{"area":"a","nodes":[{"id":"x"}]}`,
		"no id":         `{"area":"a","nodes":[{"kind":"screen"}]}`,
		"appears twice": `{"area":"a","nodes":[{"id":"x","kind":"screen"},{"id":"x","kind":"screen"}]}`,
		"empty area":    `{"area":"","nodes":[]}`,
	}
	for want, body := range cases {
		_, _, err := Parse(File{Path: "bad.json", Data: []byte(body)})
		if err == nil || !strings.Contains(err.Error(), want) || !strings.Contains(err.Error(), "bad.json") {
			t.Errorf("%s: err = %v", want, err)
		}
	}
}

func TestParseAllDuplicateAcrossFiles(t *testing.T) {
	a := File{Path: "a.json", Data: []byte(`{"area":"a","nodes":[{"id":"x","kind":"screen"}]}`)}
	b := File{Path: "b.json", Data: []byte(`{"area":"b","nodes":[{"id":"x","kind":"screen"}]}`)}
	if _, _, err := ParseAll([]File{a, b}); err == nil || !strings.Contains(err.Error(), "a.json") {
		t.Fatalf("err = %v, want duplicate naming a.json", err)
	}
	cats, skipped, err := ParseAll([]File{readFixture(t, "frontend/cart.json"), readFixture(t, "frontend/notes.json")})
	if err != nil || len(cats) != 1 || len(skipped) != 1 || skipped[0] != "frontend/notes.json" {
		t.Fatalf("cats=%d skipped=%v err=%v", len(cats), skipped, err)
	}
}
