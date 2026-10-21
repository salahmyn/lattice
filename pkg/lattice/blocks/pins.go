package blocks

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

// CheckPin reads, without changing the clone, how a pin stands: whether its
// commit exists, whether its ref has moved past it, and whether it is pushed.
// clone is the pin's clone path, already resolved.
func CheckPin(p schema.Pin, clone string) schema.PinCheck {
	if clone == "" || p.Commit == "" || strings.HasPrefix(p.Commit, "-") || strings.HasPrefix(p.Ref, "-") {
		return schema.PinCheck{State: "not checked"}
	}
	if st, err := os.Stat(clone); err != nil || !st.IsDir() {
		return schema.PinCheck{State: "not checked"}
	}
	git := func(args ...string) string {
		out, err := runGit(clone, args...)
		if err != nil {
			return ""
		}
		return strings.TrimSpace(string(out))
	}
	commit := git("rev-parse", "--verify", "--quiet", p.Commit+"^{commit}")
	if commit == "" {
		return schema.PinCheck{State: "commit missing"}
	}
	c := schema.PinCheck{CommitDate: git("log", "-1", "--format=%cI", commit), State: "at the pin"}
	if p.Ref != "" {
		c.Tip = git("rev-parse", "--verify", "--quiet", p.Ref+"^{commit}")
	}
	if c.Tip != "" && c.Tip != commit {
		c.Moved, _ = strconv.Atoi(git("rev-list", "--count", commit+".."+c.Tip))
		c.State = fmt.Sprintf("%s moved %s past the pin", p.Ref, plural(c.Moved, "commit"))
	}
	c.Pushed = git("branch", "-r", "--contains", commit) != ""
	return c
}
