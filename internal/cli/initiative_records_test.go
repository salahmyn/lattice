package cli

import (
	"strings"
	"testing"

	"github.com/salahmyn/lattice/pkg/lattice/blocks"
)

func TestSetTopLevelKeepsTheRest(t *testing.T) {
	src := "# the cart kit\nid: new-cart\ntype: initiative\nstatus: proposed # under review\nmap:\n  system: {title: Cart kit}\n  blocking: [G-TAX]\n"
	out, err := setTopLevel([]byte(src), "status", "paused")
	if err != nil {
		t.Fatal(err)
	}
	in, err := blocks.ParseInitiative(out, "t")
	if err != nil {
		t.Fatal(err)
	}
	if in.Status != "paused" || in.Map.Blocking[0] != "G-TAX" || !strings.Contains(string(out), "# the cart kit") {
		t.Errorf("got:\n%s", out)
	}
	if _, err := setTopLevel([]byte("- a\n"), "status", "paused"); err == nil {
		t.Error("a list is not a record")
	}
}
