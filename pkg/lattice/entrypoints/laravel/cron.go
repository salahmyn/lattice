package laravel

import (
	"context"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/salahmyn/lattice/pkg/lattice/entrypoints"
	"github.com/salahmyn/lattice/pkg/lattice/schema"
	"github.com/salahmyn/lattice/pkg/lattice/schema/ir"
	"github.com/salahmyn/lattice/pkg/lattice/workspace"
)

// CronDetector finds Laravel scheduled tasks declared in Console kernels.
//
// Recognises the canonical fluent shapes:
//   $schedule->command('foo:bar')->daily();
//   $schedule->command(BackupCommand::class)->cron('0 2 * * *');
//   $schedule->job(SendEmailsJob::class)->hourly();
//   $schedule->call(function () { ... })->everyMinute();   // skipped (no symbol)
//
// The trailing call chain provides the schedule — either an explicit
// ->cron(expr) or a shortcut (->daily, ->hourly, ->everyFiveMinutes,
// etc) that we map to a cron expression so the EntryPoint trigger is
// always machine-readable.
type CronDetector struct{}

func init() { entrypoints.Register(CronDetector{}) }

// Name implements entrypoints.Detector.
func (CronDetector) Name() string { return "laravel-cron" }

// Detect implements entrypoints.Detector.
func (CronDetector) Detect(_ context.Context, ws *workspace.Workspace, modules []ir.Module) ([]schema.EntryPoint, error) {
	files := collectKernelFiles(ws)
	if len(files) == 0 {
		return nil, nil
	}
	reader := newFileReader(ws)
	commandIndex := indexCLICommandsBySignature(modules, reader)
	classIndex := indexClassesByShortName(modules)

	var out []schema.EntryPoint
	seen := map[string]bool{}
	for _, f := range files {
		body := reader.read(f.rel)
		if body == "" {
			continue
		}
		uses := parseUseStatements(body)
		for _, c := range scanScheduleCalls(body, uses) {
			handler := resolveScheduledHandler(c, commandIndex, classIndex)
			if handler.Symbol == "" {
				continue
			}
			id := "ep.cron." + cronSlug(c.subject, c.schedule)
			if seen[id] {
				continue
			}
			seen[id] = true
			out = append(out, schema.EntryPoint{
				ID:      id,
				Version: 1,
				Status:  schema.StatusProposal,
				Kind:    schema.EntryPointKindCron,
				Trigger: schema.Trigger{Schedule: c.schedule, Command: c.subject},
				Handler: handler,
			})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// scheduledCall is one parsed $schedule->kind(subject)->chain.
type scheduledCall struct {
	kind     string // command | job | call
	subject  string // 'foo:bar' OR 'Backup::class' short name OR ''
	schedule string // cron expression after shortcut expansion
}

// scheduleCallRE captures kind + first arg + the trailing chain so we
// can post-process the cron expression / shortcut separately.
var scheduleCallRE = regexp.MustCompile(
	`\$schedule->(command|job|call)\s*\(\s*([^)]*?)\)((?:\s*->[a-zA-Z]+\s*\([^)]*\))+)`,
)

// scanScheduleCalls walks the kernel body and returns every
// $schedule-> call with its resolved schedule expression.
func scanScheduleCalls(body string, uses map[string]string) []scheduledCall {
	var out []scheduledCall
	for _, m := range scheduleCallRE.FindAllStringSubmatch(body, -1) {
		kind := strings.ToLower(m[1])
		subject := strings.TrimSpace(m[2])
		chain := m[3]
		// `call` takes a closure — no static handler symbol, skip.
		if kind == "call" {
			continue
		}
		subject = strings.Trim(subject, "'\" ")
		// Resolve a "::class" reference to a short class name for the
		// downstream indexer; full FQN resolution happens via uses.
		if strings.HasSuffix(subject, "::class") {
			subject = strings.TrimSuffix(subject, "::class")
		}
		_ = uses // resolveScheduledHandler does the lookup
		out = append(out, scheduledCall{
			kind:     kind,
			subject:  subject,
			schedule: extractSchedule(chain),
		})
	}
	return out
}

// shortcutSchedule maps Laravel's chainable schedule shortcuts to cron
// expressions. The set is the documented shortcut surface as of
// Laravel 10; uncommon variants fall through to their literal name.
var shortcutSchedule = map[string]string{
	"everyMinute":         "* * * * *",
	"everyTwoMinutes":     "*/2 * * * *",
	"everyThreeMinutes":   "*/3 * * * *",
	"everyFourMinutes":    "*/4 * * * *",
	"everyFiveMinutes":    "*/5 * * * *",
	"everyTenMinutes":     "*/10 * * * *",
	"everyFifteenMinutes": "*/15 * * * *",
	"everyThirtyMinutes":  "0,30 * * * *",
	"hourly":              "0 * * * *",
	"everyTwoHours":       "0 */2 * * *",
	"everyThreeHours":     "0 */3 * * *",
	"everyFourHours":      "0 */4 * * *",
	"everySixHours":       "0 */6 * * *",
	"daily":               "0 0 * * *",
	"twiceDaily":          "0 1,13 * * *",
	"weekly":              "0 0 * * 0",
	"monthly":             "0 0 1 * *",
	"quarterly":           "0 0 1 */3 *",
	"yearly":              "0 0 1 1 *",
}

// chainCallRE pulls each ->name(arg) link off the chain so we can
// recognise either an explicit ->cron(expr) or a shortcut name.
var chainCallRE = regexp.MustCompile(`->([a-zA-Z]+)\s*\(([^)]*)\)`)

func extractSchedule(chain string) string {
	matches := chainCallRE.FindAllStringSubmatch(chain, -1)
	for _, m := range matches {
		name, arg := m[1], strings.Trim(m[2], "'\" ")
		if name == "cron" && arg != "" {
			return arg
		}
		if expr, ok := shortcutSchedule[name]; ok {
			return expr
		}
	}
	return "unknown"
}

// resolveScheduledHandler maps the call subject to a real handler
// symbol. Commands look up by signature (CLI detector's index); jobs
// look up by short class name and resolve to ::handle.
func resolveScheduledHandler(c scheduledCall, commands map[string]schema.Handler, classes map[string]ir.Symbol) schema.Handler {
	switch c.kind {
	case "command":
		// Subject is either a signature like 'foo:bar' or a Command
		// class short name. Try both.
		if h, ok := commands[c.subject]; ok {
			return h
		}
		if cls, ok := classes[c.subject]; ok {
			return schema.Handler{Symbol: cls.FQN + "::handle", File: cls.File, Line: cls.Line}
		}
	case "job":
		if cls, ok := classes[c.subject]; ok {
			return schema.Handler{Symbol: cls.FQN + "::handle", File: cls.File, Line: cls.Line}
		}
	}
	return schema.Handler{}
}

// collectKernelFiles enumerates the canonical Laravel Console kernel
// locations across every available code root.
type kernelFile struct{ abs, rel string }

func collectKernelFiles(ws *workspace.Workspace) []kernelFile {
	var out []kernelFile
	seen := map[string]bool{}
	for _, root := range ws.CodeRoots {
		if !root.Available {
			continue
		}
		for _, pat := range []string{
			filepath.Join(root.Abs, "..", "app", "Console", "Kernel.php"),
			filepath.Join(root.Abs, "Console", "Kernel.php"),
			filepath.Join(root.Abs, "..", "Modules", "*", "Console", "Kernel.php"),
			filepath.Join(root.Abs, "*", "Console", "Kernel.php"),
			filepath.Join(root.Abs, "..", "Modules", "*", "Providers", "*Kernel.php"),
		} {
			matches, _ := filepath.Glob(pat)
			for _, m := range matches {
				if seen[m] {
					continue
				}
				seen[m] = true
				rel := m
				for _, r := range ws.CodeRoots {
					if strings.HasPrefix(m, r.Abs+string(filepath.Separator)) {
						rel = filepath.Join(r.Name, strings.TrimPrefix(m, r.Abs+string(filepath.Separator)))
						break
					}
				}
				out = append(out, kernelFile{abs: m, rel: rel})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].abs < out[j].abs })
	return out
}

// indexCLICommandsBySignature scans every Laravel Command class in the
// IR and indexes it by both signature string and short class name. The
// cron detector's command('foo:bar') and command(BackupCommand::class)
// both resolve through this map.
func indexCLICommandsBySignature(modules []ir.Module, reader *fileReader) map[string]schema.Handler {
	out := map[string]schema.Handler{}
	for _, m := range modules {
		body := reader.read(m.File)
		uses := parseUseStatements(body)
		classes := commandClasses(m, uses)
		if len(classes) == 0 {
			continue
		}
		signatures := scanSignatures(m, body)
		for class, sym := range classes {
			handler := schema.Handler{Symbol: class + "::handle", File: sym.File, Line: sym.Line}
			// Try to populate handler.Line/File with the actual handle() symbol.
			if h := findMethod(m, class, "handle"); h.Symbol != "" {
				handler = h
			}
			short := class
			if i := strings.LastIndex(class, "\\"); i >= 0 {
				short = class[i+1:]
			}
			out[short] = handler
			if sig := commandNameFromSignature(signatures[class]); sig != "" {
				out[sig] = handler
			}
		}
	}
	return out
}

// indexClassesByShortName lets the cron detector resolve a Class::class
// reference back to the IR symbol.
func indexClassesByShortName(modules []ir.Module) map[string]ir.Symbol {
	out := map[string]ir.Symbol{}
	for _, m := range modules {
		for _, s := range m.Symbols {
			if s.Kind != "class" {
				continue
			}
			short := s.FQN
			if i := strings.LastIndex(s.FQN, "\\"); i >= 0 {
				short = s.FQN[i+1:]
			}
			if _, exists := out[short]; !exists {
				out[short] = s
			}
		}
	}
	return out
}

// cronSlug renders a unique EP id segment from the call's subject and
// schedule. Keeps human readability while staying URL-safe.
func cronSlug(subject, schedule string) string {
	parts := strings.FieldsFunc(strings.ToLower(subject+"_"+schedule), func(r rune) bool {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			return false
		}
		return true
	})
	if len(parts) == 0 {
		return "anon"
	}
	return strings.Join(parts, ".")
}

var _ entrypoints.Detector = CronDetector{}
