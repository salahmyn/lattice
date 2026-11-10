package contract

// @module-feature api

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// allowedStates are the states an assurance value may take.
var allowedStates = map[string]bool{
	StatePass: true, StateFail: true, StateUnknown: true, StateInfo: true, StateDrifted: true,
	StatePresent: true, StatePartial: true, StateAbsent: true, StateStale: true,
}

var honesties = map[string]bool{Fact: true, UpperBound: true, Proxy: true}

// verdictStates are the states of a verdict object.
var verdictStates = map[string]bool{"ASSURED": true, "UNPROVEN": true, "BROKEN": true, StateUnknown: true}

// LintOptions tune Lint.
type LintOptions struct {
	// AllowAssured lets a verdict read ASSURED or BROKEN. Only a payload
	// computed against a base (lattice.check/1) may.
	AllowAssured bool
}

// Lint walks a JSON payload and lists every place it breaks the floor:
//
//   - an assurance value (an object with state and honesty) has a state
//     outside the closed list, or an honesty outside fact, upper_bound, proxy;
//   - a pass carries a code that names missing evidence (NOT_COMPUTED, NO_*,
//     NOT_*, PARTIAL*, *_UNKNOWN, CONFIRMED_*, LIVE_*, BREAK_SURVIVED, ...);
//     a descriptive code, such as the check's UNTRACED_LINES on an empty
//     blind spot, is fine;
//   - an unknown carries no code to say why it is unknown;
//   - NOT_COMPUTED is anything but unknown;
//   - a verdict (an object with state and code but no honesty) reads ASSURED
//     or BROKEN in a payload that has no base to say so, or an unknown state.
//
// An empty result means the payload never shows missing evidence as a pass.
// @enforces api:INV-2
func Lint(payload []byte, opt LintOptions) ([]string, error) {
	var v interface{}
	if err := json.Unmarshal(payload, &v); err != nil {
		return nil, err
	}
	var out []string
	walk(v, "$", opt, &out)
	sort.Strings(out)
	return out, nil
}

// gapCode reports whether a code says evidence is missing, weak or failed.
func gapCode(code string) bool {
	if code == "" {
		return false
	}
	for _, p := range []string{"NO_", "NOT_", "PARTIAL", "CONFIRMED_", "LIVE_", "EVIDENCE_NOT_CI", "BREAK_SURVIVED", "NEVER_", "SUITE_RED", "GAPS", "BROKEN"} {
		if strings.HasPrefix(code, p) {
			return true
		}
	}
	return strings.HasSuffix(code, "_UNKNOWN") || strings.HasSuffix(code, "_NOT_RUN")
}

func walk(v interface{}, path string, opt LintOptions, out *[]string) {
	switch x := v.(type) {
	case map[string]interface{}:
		lintObject(x, path, opt, out)
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			walk(x[k], path+"."+k, opt, out)
		}
	case []interface{}:
		for i, e := range x {
			walk(e, fmt.Sprintf("%s[%d]", path, i), opt, out)
		}
	}
}

func str(m map[string]interface{}, k string) (string, bool) {
	s, ok := m[k].(string)
	return s, ok
}

func lintObject(m map[string]interface{}, path string, opt LintOptions, out *[]string) {
	state, hasState := str(m, "state")
	if !hasState {
		return
	}
	honesty, hasHonesty := str(m, "honesty")
	code, _ := str(m, "code")
	bad := func(format string, a ...interface{}) { *out = append(*out, path+": "+fmt.Sprintf(format, a...)) }

	if hasHonesty {
		if !allowedStates[state] {
			bad("state %q is not an assurance state", state)
		}
		if !honesties[honesty] {
			bad("honesty %q is not fact, upper_bound or proxy", honesty)
		}
		switch {
		case state == StatePass && gapCode(code):
			bad("a pass carries code %q, which names missing evidence", code)
		case state == StateUnknown && code == "":
			bad("an unknown has no code to say why")
		}
		if code == CodeNotComputed && state != StateUnknown {
			bad("NOT_COMPUTED reads %q, not unknown", state)
		}
		return
	}
	// A verdict: state and code, no honesty.
	if _, hasCode := m["code"]; hasCode && !opt.AllowAssured {
		if !verdictStates[state] && state != "" && strings.ToUpper(state) == state {
			bad("verdict state %q is not ASSURED, UNPROVEN, BROKEN or unknown", state)
		}
		if state == "ASSURED" || state == "BROKEN" {
			bad("a verdict reads %s without a base to compare with", state)
		}
	}
}
