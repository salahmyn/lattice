package laravel

import (
	"context"
	"regexp"
	"sort"
	"strings"

	"github.com/salahmyn/lattice/pkg/lattice/entrypoints"
	"github.com/salahmyn/lattice/pkg/lattice/schema"
	"github.com/salahmyn/lattice/pkg/lattice/schema/ir"
	"github.com/salahmyn/lattice/pkg/lattice/workspace"
)

// QueueDetector finds Laravel queue jobs — classes that implement
// Illuminate\Contracts\Queue\ShouldQueue (directly or via the short
// alias when the file imports it).
//
// Each job's handle() method is the handler. The queue name comes from
// the optional `$queue` property; absent that, the trigger uses the
// class short name as the queue label so the entry point is still
// visible in the view.
type QueueDetector struct{}

func init() { entrypoints.Register(QueueDetector{}) }

// Name implements entrypoints.Detector.
func (QueueDetector) Name() string { return "laravel-queue" }

// Detect implements entrypoints.Detector.
func (QueueDetector) Detect(_ context.Context, ws *workspace.Workspace, modules []ir.Module) ([]schema.EntryPoint, error) {
	out := []schema.EntryPoint{}
	reader := newFileReader(ws)
	for _, m := range modules {
		body := reader.read(m.File)
		if body == "" {
			continue
		}
		uses := parseUseStatements(body)
		jobs := jobClasses(m, body, uses)
		if len(jobs) == 0 {
			continue
		}
		queues := scanQueueProperties(body)
		for class, classSym := range jobs {
			queue := queues[class]
			if queue == "" {
				queue = shortName(class)
			}
			handler := findMethod(m, class, "handle")
			if handler.Symbol == "" {
				handler = schema.Handler{Symbol: classSym.FQN + "::handle", File: classSym.File, Line: classSym.Line}
			}
			out = append(out, schema.EntryPoint{
				ID:      "ep.queue." + cliSlug(strings.ReplaceAll(class, "\\", ".")),
				Version: 1,
				Status:  schema.StatusProposal,
				Kind:    schema.EntryPointKindQueue,
				Trigger: schema.Trigger{Queue: queue},
				Handler: handler,
			})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// implementsShouldQueue matches `class X ... implements ... ShouldQueue` —
// the contract isn't always the first implemented interface so we use a
// permissive regex that anchors on the class keyword and looks for the
// interface name anywhere on the implements clause.
var implementsShouldQueue = regexp.MustCompile(`(?s)class\s+([A-Za-z_][A-Za-z0-9_]*)[^{]*?\bimplements\b[^{]*?\bShouldQueue\b`)

// queueProperty matches a $queue = 'name'; declaration.
var queueProperty = regexp.MustCompile(`(?m)(?:public|protected|private)?\s*\$queue\s*=\s*['"]([^'"]+)['"]\s*;`)

// jobClasses returns the FQNs in this module that implement ShouldQueue.
// We resolve the class short-name back to its FQN via the IR's class
// symbols. Anonymous job classes are skipped.
func jobClasses(m ir.Module, body string, uses map[string]string) map[string]ir.Symbol {
	out := map[string]ir.Symbol{}
	// Only proceed if the file actually mentions ShouldQueue — keeps
	// the regex pass cheap on the >1900-file DevelopersPortal scan.
	if !strings.Contains(body, "ShouldQueue") {
		return out
	}
	_ = uses // present for symmetry; resolution of ShouldQueue itself
	// isn't required because we match on the literal interface name.

	matches := implementsShouldQueue.FindAllStringSubmatch(body, -1)
	if len(matches) == 0 {
		return out
	}
	short := map[string]bool{}
	for _, mt := range matches {
		short[mt[1]] = true
	}
	for _, s := range m.Symbols {
		if s.Kind != "class" {
			continue
		}
		if i := strings.LastIndex(s.FQN, "\\"); i >= 0 {
			if short[s.FQN[i+1:]] {
				out[s.FQN] = s
			}
		} else if short[s.FQN] {
			out[s.FQN] = s
		}
	}
	return out
}

// scanQueueProperties returns a class -> queue-name map for any
// `protected $queue = 'name';` style declaration in the file. We can't
// pin the property to a class precisely from text, so we apply the
// first match to every job class in the file — accurate for the
// dominant pattern of one job per file.
func scanQueueProperties(body string) map[string]string {
	out := map[string]string{}
	m := queueProperty.FindStringSubmatch(body)
	if m == nil {
		return out
	}
	out["*"] = m[1]
	return out
}

var _ entrypoints.Detector = QueueDetector{}
