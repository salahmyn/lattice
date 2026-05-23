package laravel

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/salahmyn/lattice/pkg/lattice/entrypoints"
	"github.com/salahmyn/lattice/pkg/lattice/schema"
	"github.com/salahmyn/lattice/pkg/lattice/schema/ir"
	"github.com/salahmyn/lattice/pkg/lattice/workspace"
)

// CLIDetector finds Laravel CLI commands — classes that extend
// Illuminate\Console\Command (or its short alias Command in a file that
// imports it). Each command's `handle()` method is the handler symbol;
// the `$signature` property gives the command name a user types.
//
// We rely on the already-parsed IR so we don't reopen source. When a
// class's base ends in "\Console\Command" or "\Command" with a
// corresponding `use Illuminate\Console\Command;`, we treat it as a
// command. Command name comes from the $signature literal extracted via
// a focused regex over the file.
type CLIDetector struct{}

func init() { entrypoints.Register(CLIDetector{}) }

// Name implements entrypoints.Detector.
func (CLIDetector) Name() string { return "laravel-cli" }

// Detect implements entrypoints.Detector.
func (CLIDetector) Detect(_ context.Context, ws *workspace.Workspace, modules []ir.Module) ([]schema.EntryPoint, error) {
	out := []schema.EntryPoint{}
	reader := newFileReader(ws)
	for _, m := range modules {
		body := reader.read(m.File)
		uses := parseUseStatements(body)
		// Build a set of class symbols whose base class resolves to
		// Illuminate\Console\Command after applying the file's use map.
		classes := commandClasses(m, uses)
		if len(classes) == 0 {
			continue
		}
		signatures := scanSignatures(m, body)
		for class, classSym := range classes {
			sig := signatures[class] // may be ""
			name := commandNameFromSignature(sig)
			if name == "" {
				// Fall back to the short class name lowercased — Laravel
				// commands without an explicit $signature default to the
				// classname, which is still useful for a trigger label.
				name = shortName(class)
			}
			handler := findMethod(m, class, "handle")
			if handler.Symbol == "" {
				handler = schema.Handler{Symbol: classSym.FQN + "::handle", File: classSym.File, Line: classSym.Line}
			}
			out = append(out, schema.EntryPoint{
				ID:      "ep.cli." + cliSlug(name),
				Version: 1,
				Status:  schema.StatusProposal,
				Kind:    schema.EntryPointKindCLI,
				Trigger: schema.Trigger{Command: name},
				Handler: handler,
			})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// commandClasses returns classes in the module that extend
// Illuminate\Console\Command (directly or via short-name + use).
func commandClasses(m ir.Module, uses map[string]string) map[string]ir.Symbol {
	out := map[string]ir.Symbol{}
	for _, s := range m.Symbols {
		if s.Kind != "class" {
			continue
		}
		for _, base := range s.BaseClasses {
			resolved := base
			if !strings.Contains(base, "\\") {
				if fqn, ok := uses[base]; ok {
					resolved = fqn
				}
			}
			if resolved == "Illuminate\\Console\\Command" || strings.HasSuffix(resolved, "\\Console\\Command") {
				out[s.FQN] = s
				break
			}
		}
	}
	return out
}

// signatureProperty matches a $signature = '...'; declaration.
var signatureProperty = regexp.MustCompile(`(?m)\$signature\s*=\s*['"]([^'"]+)['"]\s*;`)

// scanSignatures returns class FQN -> first $signature literal in the
// file. Imperfect for files with multiple command classes but Laravel
// convention is one command class per file.
func scanSignatures(m ir.Module, body string) map[string]string {
	out := map[string]string{}
	if body == "" {
		return out
	}
	matches := signatureProperty.FindAllStringSubmatch(body, -1)
	if len(matches) == 0 {
		return out
	}
	// Map every command class in this module to the first $signature
	// we find. Refine if file holds multiple commands.
	for _, s := range m.Symbols {
		if s.Kind == "class" {
			out[s.FQN] = matches[0][1]
		}
	}
	return out
}

// commandNameFromSignature strips arguments/options off Laravel's signature
// string, leaving just the command name: "refunds:reconcile {--dry-run}"
// -> "refunds:reconcile".
func commandNameFromSignature(sig string) string {
	if sig == "" {
		return ""
	}
	if i := strings.IndexByte(sig, ' '); i > 0 {
		return sig[:i]
	}
	return sig
}

// cliSlug renders a CLI command name as a dotted EP id segment.
// "refunds:reconcile" -> "refunds.reconcile".
func cliSlug(name string) string {
	out := strings.ToLower(name)
	out = strings.ReplaceAll(out, ":", ".")
	out = strings.ReplaceAll(out, "/", ".")
	return out
}

func shortName(fqn string) string {
	if i := strings.LastIndex(fqn, "\\"); i >= 0 {
		return strings.ToLower(fqn[i+1:])
	}
	return strings.ToLower(fqn)
}

// fileReader resolves IR-relative file paths to absolute paths and reads
// the bytes. Cached per detector run so we don't re-read the same module
// repeatedly when several detectors hit the same file.
type fileReader struct {
	ws    *workspace.Workspace
	cache map[string]string
}

func newFileReader(ws *workspace.Workspace) *fileReader {
	return &fileReader{ws: ws, cache: map[string]string{}}
}

func (r *fileReader) read(relFile string) string {
	if v, ok := r.cache[relFile]; ok {
		return v
	}
	abs := r.resolve(relFile)
	if abs == "" {
		r.cache[relFile] = ""
		return ""
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		r.cache[relFile] = ""
		return ""
	}
	body := string(data)
	r.cache[relFile] = body
	return body
}

// resolve converts a code-root-relative IR path (e.g. "modules/X/Y.php")
// back into an absolute filesystem path by trying each code root.
func (r *fileReader) resolve(relFile string) string {
	if r.ws == nil {
		return ""
	}
	for _, root := range r.ws.CodeRoots {
		if !root.Available {
			continue
		}
		// IR path is "<root-name>/<within-root>". Strip the root name.
		if strings.HasPrefix(relFile, root.Name+"/") {
			abs := filepath.Join(root.Abs, strings.TrimPrefix(relFile, root.Name+"/"))
			if _, err := os.Stat(abs); err == nil {
				return abs
			}
		}
	}
	// Last resort: try every root verbatim.
	for _, root := range r.ws.CodeRoots {
		if !root.Available {
			continue
		}
		abs := filepath.Join(root.Abs, relFile)
		if _, err := os.Stat(abs); err == nil {
			return abs
		}
	}
	return ""
}

// findMethod returns the IR symbol for class::method (e.g. handle()), or
// a zero Handler when not present.
func findMethod(m ir.Module, class, method string) schema.Handler {
	for _, s := range m.Symbols {
		if s.FQN == class+"::"+method {
			return schema.Handler{Symbol: s.FQN, File: s.File, Line: s.Line}
		}
	}
	return schema.Handler{}
}

var _ entrypoints.Detector = CLIDetector{}
