package importer

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// DecisionsFile is the on-disk batch-review document accepted by
// `lattice import review --from-file`. It exists so review work can be
// authored in a PR (decisions.yaml) and applied atomically instead of via
// dozens of shell-loop invocations.
//
//	# decisions.yaml
//	version: 1
//	decisions:
//	  cand_000514923da0: accept
//	  cand_5bc87047f89d: reject
type DecisionsFile struct {
	Version   int               `yaml:"version"`
	Decisions map[string]string `yaml:"decisions"`
}

const decisionsFileVersion = 1

// LoadDecisionsFile reads a decisions file from path and normalises the
// action strings ("accept"/"accepted" -> DecisionAccepted, etc).
func LoadDecisionsFile(path string) (map[string]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var df DecisionsFile
	if err := yaml.Unmarshal(data, &df); err != nil {
		return nil, fmt.Errorf("parse decisions file: %w", err)
	}
	if len(df.Decisions) == 0 {
		return nil, fmt.Errorf("decisions file has no `decisions:` map")
	}
	out := make(map[string]string, len(df.Decisions))
	for k, v := range df.Decisions {
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "accept", "accepted":
			out[k] = DecisionAccepted
		case "reject", "rejected":
			out[k] = DecisionRejected
		default:
			return nil, fmt.Errorf("decisions[%q]: unknown action %q (use accept|reject)", k, v)
		}
	}
	return out, nil
}

// CandidatePredicate is a filter over candidates — composed from one or
// more --where clauses. ParseWhere returns one of these from a single
// expression; combine with AndPredicates.
type CandidatePredicate func(Candidate) bool

// AndPredicates is the conjunction of every supplied predicate. An empty
// list returns a tautology.
func AndPredicates(ps []CandidatePredicate) CandidatePredicate {
	if len(ps) == 0 {
		return func(Candidate) bool { return true }
	}
	return func(c Candidate) bool {
		for _, p := range ps {
			if !p(c) {
				return false
			}
		}
		return true
	}
}

// ParseWhere turns one `--where` expression into a predicate. Supported:
//   - package=<prefix>          (string prefix match on Candidate.Package)
//   - confidence{<|<=|=|>=|>}<n>   (numeric on Candidate.Confidence)
//   - symbols{<|<=|=|>=|>}<n>     (numeric on len(Candidate.Symbols))
//
// Multiple --where clauses on the CLI are ANDed via AndPredicates.
func ParseWhere(expr string) (CandidatePredicate, error) {
	op, key, val, err := splitWhere(expr)
	if err != nil {
		return nil, err
	}
	switch key {
	case "package":
		if op != "=" {
			return nil, fmt.Errorf("--where %s: package only supports `=<prefix>`", expr)
		}
		return func(c Candidate) bool { return strings.HasPrefix(c.Package, val) }, nil
	case "confidence":
		n, perr := strconv.ParseFloat(val, 64)
		if perr != nil {
			return nil, fmt.Errorf("--where %s: confidence value not numeric", expr)
		}
		return numericPredicate(op, func(c Candidate) float64 { return c.Confidence }, n), nil
	case "symbols":
		n, perr := strconv.ParseFloat(val, 64)
		if perr != nil {
			return nil, fmt.Errorf("--where %s: symbols value not numeric", expr)
		}
		return numericPredicate(op, func(c Candidate) float64 { return float64(len(c.Symbols)) }, n), nil
	default:
		return nil, fmt.Errorf("--where %s: unknown key %q (use package, confidence, symbols)", expr, key)
	}
}

// splitWhere finds the longest matching operator so e.g. "confidence>=0.5"
// is split into ("confidence", ">=", "0.5") rather than (">", "=0.5").
func splitWhere(expr string) (op, key, val string, err error) {
	// Order matters: two-char ops first.
	for _, o := range []string{"<=", ">=", "<", ">", "="} {
		if i := strings.Index(expr, o); i > 0 {
			return o, strings.TrimSpace(expr[:i]), strings.TrimSpace(expr[i+len(o):]), nil
		}
	}
	return "", "", "", fmt.Errorf("--where %q: expected key OP value (OP one of <,<=,=,>=,>)", expr)
}

func numericPredicate(op string, get func(Candidate) float64, n float64) CandidatePredicate {
	switch op {
	case "<":
		return func(c Candidate) bool { return get(c) < n }
	case "<=":
		return func(c Candidate) bool { return get(c) <= n }
	case "=":
		return func(c Candidate) bool { return get(c) == n }
	case ">":
		return func(c Candidate) bool { return get(c) > n }
	case ">=":
		return func(c Candidate) bool { return get(c) >= n }
	}
	return func(Candidate) bool { return false }
}
