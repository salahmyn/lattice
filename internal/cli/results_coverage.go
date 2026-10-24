package cli

// @module-feature coverage

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/salahmyn/lattice/pkg/lattice/config"
	"github.com/salahmyn/lattice/pkg/lattice/coverage"
	"github.com/salahmyn/lattice/pkg/lattice/ledger"
	"github.com/salahmyn/lattice/pkg/lattice/results"
	"github.com/salahmyn/lattice/pkg/lattice/rtm"
	"github.com/salahmyn/lattice/pkg/lattice/schema"
	"github.com/salahmyn/lattice/pkg/lattice/workspace"
)

// coverageIndex loads the ingested coverage set bound to the graph, or nil
// when none was ingested.
func coverageIndex(ws *workspace.Workspace, kg schema.KnowledgeGraph) *coverage.Index {
	set, ok := coverage.Load(ws.LatticeDir)
	if !ok {
		return nil
	}
	return coverage.NewIndex(set, kg)
}

// coverageEvidence is coverageIndex as rtm.Options.Coverage: a nil index
// must stay a nil interface, or every row would read as having coverage.
func coverageEvidence(ws *workspace.Workspace, kg schema.KnowledgeGraph) rtm.CoverageEvidence {
	if ix := coverageIndex(ws, kg); ix != nil {
		return ix
	}
	return nil
}

func newResultsIngestCommand(io *IO) *cobra.Command {
	var perTest, perFile []string
	cmd := &cobra.Command{
		Use:   "ingest [<report>...]",
		Short: "Ingest test reports and coverage reports of one run into lattice/.cache/results",
		Long: "Parse the reports of one test run into lattice/.cache/results, replacing the\n" +
			"previous set. Each file is told apart by its content:\n" +
			"\n" +
			"  test reports      JUnit-family XML (junit, pytest, phpunit, Playwright) or a\n" +
			"                    TAP stream (pgTAP from pg_prove --verbose) -> results.json\n" +
			"  coverage reports  LCOV, go test -coverprofile, istanbul coverage-final.json,\n" +
			"                    coverage.py json (--show-contexts gives per-test data),\n" +
			"                    cobertura or clover XML, a Postgres pg_stat_user_functions\n" +
			"                    export -> coverage.json\n" +
			"\n" +
			"--per-test <dir> reads one coverage report per test, named by the test's slug\n" +
			"(`lattice results tests --verifiers` prints it); --per-file <dir> one report per\n" +
			"test file, which is weaker evidence and never makes a criterion exercised.\n" +
			"With no paths, the reports and per-test directories in config.yaml coverage:\n" +
			"are read. Pass every report of the run together: a verifier with no result\n" +
			"cannot count as demonstrated, and a failure in any report wins over a pass.\n" +
			"Results and coverage of one run share one commit stamp and one ledger event.",
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := openWorkspace(io)
			if err != nil {
				return io.fail("WORKSPACE_FAILED", err.Error(), nil)
			}
			// The commit is best-effort: the graph carries it, but ingesting
			// shouldn't require a full extract, so we read it cheaply.
			kg, _, _ := graphFor(io, cmd, false)
			cfg, _ := config.Load(ws.LatticeDir)
			covCfg := cfg.CoverageConfig()
			base := filepath.Dir(ws.LatticeDir)

			var testReports []string
			var inputs []coverage.Input
			for _, p := range args {
				data, err := os.ReadFile(p)
				if err != nil {
					return io.fail("INGEST_FAILED", err.Error(), nil)
				}
				if coverage.Detect(data) == "" {
					testReports = append(testReports, p)
					continue
				}
				inputs = append(inputs, reportInput(p, base, covCfg))
			}
			for _, d := range perTest {
				inputs = append(inputs, dirInput(d, coverage.BasisPerTest, base, covCfg))
			}
			for _, d := range perFile {
				inputs = append(inputs, dirInput(d, coverage.BasisPerFile, base, covCfg))
			}
			if len(args) == 0 && len(perTest) == 0 && len(perFile) == 0 {
				inputs = configInputs(base, covCfg)
				if len(inputs) == 0 {
					return io.fail("INGEST_FAILED", "no reports given, and config.yaml has no coverage.reports or coverage.per_test.dirs", nil)
				}
			}

			out := map[string]interface{}{"commit": kg.GeneratedFromCommit}
			var evidence []string
			if len(testReports) > 0 {
				set, err := results.Ingest(ws.LatticeDir, kg.GeneratedFromCommit, testReports...)
				if err != nil {
					return io.fail("INGEST_FAILED", err.Error(), nil)
				}
				total, pass, fail, skip := set.Tally()
				out["total"], out["pass"], out["fail"], out["skip"] = total, pass, fail, skip
				evidence = append(evidence, fmt.Sprintf("%d pass, %d fail, %d skip", pass, fail, skip))
				if !io.JSON {
					io.printf("ingested %d test results (%d pass, %d fail, %d skip)\n", total, pass, fail, skip)
				}
			}
			if len(inputs) > 0 {
				dirty, dirtyFiles := gitDirty(base)
				cs, notes, err := coverage.Ingest(ws.LatticeDir, kg, inputs, coverage.Options{
					Commit: kg.GeneratedFromCommit, Dirty: dirty, DirtyFiles: dirtyFiles,
					OnlyVerifiers: covCfg.PerTest.OnlyVerifiersOrDefault(),
				})
				printNotes(io, notes)
				if err != nil {
					return io.fail("INGEST_FAILED", err.Error(), nil)
				}
				suite := 0
				for _, s := range cs.Sources {
					if s.Basis == coverage.BasisSuite {
						suite++
					}
				}
				out["coverage"] = map[string]interface{}{
					"reports": suite, "symbols": len(cs.Symbols), "per_test": len(cs.PerTest),
					"per_file": len(cs.PerFile), "dirty": cs.Dirty, "unmatched": notes.Unmatched,
					"ambiguous": notes.Ambiguous, "non_verifiers_skipped": notes.NonVerifiers,
				}
				evidence = append(evidence, fmt.Sprintf("coverage %d report(s), %d per-test, %d per-file",
					suite, len(cs.PerTest), len(cs.PerFile)))
				if !io.JSON {
					io.printf("ingested coverage: %d suite report(s) over %d symbols, per-test data for %d test(s), per-file for %d file(s)\n",
						suite, len(cs.Symbols), len(cs.PerTest), len(cs.PerFile))
					if cs.Dirty {
						io.printf("warning: the working tree has uncommitted changes; validate will report COVERAGE_STALE\n")
					}
				}
			}
			appendLedgerEvent(io, ws, ledger.EventCheckRun, "workspace",
				fmt.Sprintf("results ingest: %s @ %s", strings.Join(evidence, "; "), shortCommit(kg.GeneratedFromCommit)))
			if io.JSON {
				return io.printJSON(out)
			}
			io.printf("run `lattice rtm` to see criteria upgrade to DEMONSTRATED (and EXERCISED with per-test coverage)\n")
			return nil
		},
	}
	cmd.Flags().StringArrayVar(&perTest, "per-test", nil, "directory of per-test coverage reports, each named by its test's slug (repeatable)")
	cmd.Flags().StringArrayVar(&perFile, "per-file", nil, "directory of per-test-file coverage reports, each named by the file's slug (repeatable)")
	return cmd
}

func printNotes(io *IO, n coverage.Notes) {
	if io.JSON {
		return
	}
	show := func(what string, list []string) {
		if len(list) == 0 {
			return
		}
		io.printf("warning: %d %s:", len(list), what)
		for i, s := range list {
			if i == 5 {
				io.printf(" ... (%d more)", len(list)-5)
				break
			}
			io.printf(" %s", s)
		}
		io.printf("\n")
	}
	show("coverage report(s) or context(s) name no test", n.Unmatched)
	show("coverage report(s) or context(s) fit several tests (rename them to the test's FQN)", n.Ambiguous)
	show("file(s) in a per-test directory are not coverage reports", n.NotCoverage)
	if n.NonVerifiers > 0 {
		io.printf("skipped %d per-test report(s) of tests that verify nothing (coverage.per_test.only_verifiers)\n", n.NonVerifiers)
	}
}

// sameFile reports whether a config path (relative to base) names p.
func sameFile(p, base, configured string) bool {
	if configured == "" {
		return false
	}
	a, err1 := filepath.Abs(p)
	b, err2 := filepath.Abs(filepath.Join(base, configured))
	return err1 == nil && err2 == nil && a == b
}

// label records a path relative to the directory holding lattice/ when it
// is inside it.
func label(p, base string) string {
	abs, err := filepath.Abs(p)
	if err != nil {
		return filepath.ToSlash(p)
	}
	b, _ := filepath.Abs(base)
	if rel, err := filepath.Rel(b, abs); err == nil && !strings.HasPrefix(rel, "..") {
		return filepath.ToSlash(rel)
	}
	return filepath.ToSlash(p)
}

// reportInput reads a report path given on the command line, taking its
// format, root and include set from a coverage.reports entry for the same
// file when there is one.
func reportInput(p, base string, c config.Coverage) coverage.Input {
	in := coverage.Input{Path: p, Label: label(p, base)}
	for _, r := range c.Reports {
		if sameFile(p, base, r.Path) {
			in.Format, in.Root, in.Include = coverage.Format(r.Format), r.Root, r.Include
		}
	}
	return in
}

func dirInput(d, basis, base string, c config.Coverage) coverage.Input {
	in := coverage.Input{Path: d, Label: label(d, base), Basis: basis}
	for _, r := range c.PerTest.Dirs {
		if sameFile(d, base, r.Dir) {
			in.Format, in.Root, in.Include = coverage.Format(r.Format), r.Root, r.Include
		}
	}
	return in
}

// configInputs are the reports config.yaml coverage: names.
func configInputs(base string, c config.Coverage) []coverage.Input {
	var out []coverage.Input
	for _, r := range c.Reports {
		out = append(out, coverage.Input{Path: filepath.Join(base, r.Path), Label: r.Path,
			Format: coverage.Format(r.Format), Root: r.Root, Include: r.Include})
	}
	for _, d := range c.PerTest.Dirs {
		basis := d.Basis
		if basis == "" {
			basis = coverage.BasisPerTest
		}
		out = append(out, coverage.Input{Path: filepath.Join(base, d.Dir), Label: d.Dir, Basis: basis,
			Format: coverage.Format(d.Format), Root: d.Root, Include: d.Include})
	}
	return out
}

// gitDirty reports whether tracked files under dir have uncommitted
// changes, naming up to ten. Lattice's own runtime state (lattice/.ledger,
// lattice/.cache) is not code and does not count; untracked files, such as
// the coverage reports themselves, do not either. Outside git it is clean.
func gitDirty(dir string) (bool, []string) {
	out, err := exec.Command("git", "-C", dir, "status", "--porcelain", "--untracked-files=no", "--", ".").Output()
	if err != nil {
		return false, nil
	}
	var files []string
	for _, line := range strings.Split(string(out), "\n") {
		if len(line) < 4 {
			continue
		}
		p := strings.TrimSpace(line[3:])
		if i := strings.Index(p, " -> "); i >= 0 {
			p = p[i+4:]
		}
		if strings.Contains("/"+p, "/lattice/.ledger/") || strings.Contains("/"+p, "/lattice/.cache/") {
			continue
		}
		files = append(files, p)
	}
	sort.Strings(files)
	dirty := len(files) > 0
	if len(files) > 10 {
		files = append(files[:10], fmt.Sprintf("and %d more", len(files)-10))
	}
	return dirty, files
}

// langAliases maps --lang spellings to adapter languages.
var langAliases = map[string]string{
	"go": "go", "golang": "go",
	"ts": "typescript", "typescript": "typescript", "js": "typescript", "javascript": "typescript", "node": "typescript",
	"py": "python", "python": "python",
	"php": "php",
	"sql": "postgres", "pg": "postgres", "postgres": "postgres", "pgtap": "postgres",
}

func newResultsTestsCommand(io *IO) *cobra.Command {
	var verifiers bool
	var lang, runner string
	cmd := &cobra.Command{
		Use:   "tests",
		Short: "List tests with the filter each runner takes, to collect per-test coverage in CI",
		Long: "Prints the graph's tests (with --verifiers, only those carrying @verifies), each\n" +
			"with its report slug and the arguments that run it alone:\n" +
			"\n" +
			"  go        go test <package> -run '^TestX$'\n" +
			"  node      node --test --test-name-pattern '^<title>$' <file>\n" +
			"  vitest    vitest run <file> -t '<title>$'   (jest: -t too)\n" +
			"  deno      deno test --filter '/^<title>$/' <file>\n" +
			"  pytest    pytest <node id>   (pytest-k: -k <name>)\n" +
			"  phpunit   phpunit --filter '/::<method>$/'\n" +
			"  pg_prove  pg_prove <file>   (pgTAP runs a file as one unit)\n" +
			"\n" +
			"Lattice does not run tests. CI loops over this list, in parallel, and writes one\n" +
			"coverage report per test named <slug>.<ext> into a directory that\n" +
			"`lattice results ingest --per-test <dir>` reads. Text output is one line per\n" +
			"test and runner, tab-separated: runner, slug, file (go: package), arguments.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			kg, _, err := graphFor(io, cmd, false)
			if err != nil {
				return io.fail("EXTRACT_FAILED", err.Error(), nil)
			}
			want := ""
			if lang != "" {
				var ok bool
				if want, ok = langAliases[strings.ToLower(lang)]; !ok {
					return io.fail("BAD_FLAG", fmt.Sprintf("--lang %q: want go, typescript, python, php or postgres", lang), nil)
				}
			}
			var sel []results.Selector
			for _, s := range results.Selectors(kg, verifiers) {
				lng := s.Language
				if lng == "javascript" {
					lng = "typescript"
				}
				if want != "" && lng != want {
					continue
				}
				if runner != "" {
					args, ok := s.Filters[runner]
					if !ok {
						continue
					}
					s.Filters = map[string][]string{runner: args}
				}
				sel = append(sel, s)
			}
			if io.JSON {
				if sel == nil {
					sel = []results.Selector{}
				}
				return io.printJSON(map[string]interface{}{"tests": sel})
			}
			for _, s := range sel {
				runners := make([]string, 0, len(s.Filters))
				for r := range s.Filters {
					runners = append(runners, r)
				}
				sort.Strings(runners)
				where := s.File
				if s.Package != "" {
					where = s.Package
				}
				for _, r := range runners {
					io.printf("%s\t%s\t%s\t%s\n", r, s.Slug, where, shellJoin(s.Filters[r]))
				}
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&verifiers, "verifiers", false, "only tests that carry @verifies")
	cmd.Flags().StringVar(&lang, "lang", "", "only tests of one language: go, typescript (js), python, php, postgres")
	cmd.Flags().StringVar(&runner, "runner", "", "only one runner's filter: go, node, vitest, jest, deno, pytest, pytest-k, phpunit, pg_prove")
	return cmd
}

// shellJoin quotes arguments for a POSIX shell.
func shellJoin(args []string) string {
	out := make([]string, len(args))
	for i, a := range args {
		if a != "" && strings.IndexFunc(a, func(r rune) bool {
			return !(r == '-' || r == '_' || r == '.' || r == '/' || r == ':' || r == '=' ||
				(r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9'))
		}) < 0 {
			out[i] = a
			continue
		}
		out[i] = "'" + strings.ReplaceAll(a, "'", `'\''`) + "'"
	}
	return strings.Join(out, " ")
}
