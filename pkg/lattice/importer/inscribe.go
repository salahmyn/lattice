package importer

import (
	"context"
	"os"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/salahmyn/lattice/pkg/lattice/adapters"
	"github.com/salahmyn/lattice/pkg/lattice/schema/ir"
)

// InscribedRecordFileName is where an inline inscribe records what it wrote,
// so a later uninscribe can reverse exactly those edits.
const InscribedRecordFileName = "inscribed.yaml"

// InscribeRecord is the persisted list of inline edits an inscribe applied.
type InscribeRecord struct {
	Version int            `yaml:"version"`
	Edits   []InscribeEdit `yaml:"edits"`
}

// SaveInscribeRecord persists the applied inline edits to path.
func SaveInscribeRecord(path string, edits []InscribeEdit) error {
	data, err := yaml.Marshal(InscribeRecord{Version: 1, Edits: edits})
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

// LoadInscribeRecord reads the applied inline edits from path.
func LoadInscribeRecord(path string) ([]InscribeEdit, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var rec InscribeRecord
	if err := yaml.Unmarshal(data, &rec); err != nil {
		return nil, err
	}
	return rec.Edits, nil
}

// FeatureSymbols links an accepted feature to its implementing symbol FQNs.
type FeatureSymbols struct {
	Feature string
	Symbols []string
}

// InscribeEdit is one planned inline annotation insertion.
type InscribeEdit struct {
	File    string `json:"file"`
	Line    int    `json:"line"` // 1-based declaration line the annotation goes above
	Symbol  string `json:"symbol"`
	Feature string `json:"feature"`
	Text    string `json:"text"` // rendered annotation block
}

// InscribePlan is every inline insertion an inscribe would make, plus the
// symbols it deliberately left alone.
type InscribePlan struct {
	Edits         []InscribeEdit `json:"edits"`
	AlreadyMarked []string       `json:"already_marked,omitempty"`
}

// InscribeFailure records a file whose modification was rejected.
type InscribeFailure struct {
	File   string `json:"file"`
	Reason string `json:"reason"`
}

// InscribeResult is the outcome of applying (or reversing) an inline inscribe.
type InscribeResult struct {
	FilesChanged        []string          `json:"files_changed"`
	AnnotationsInserted int               `json:"annotations_inserted,omitempty"`
	AnnotationsRemoved  int               `json:"annotations_removed,omitempty"`
	Failed              []InscribeFailure `json:"failed,omitempty"`
	// Applied lists the edits actually written, recorded so an uninscribe can
	// reverse exactly what was inscribed.
	Applied []InscribeEdit `json:"applied,omitempty"`
}

// inscribeKinds are the symbol kinds an inline annotation is attached to.
// Methods inherit their feature from the enclosing class, so only top-level
// declarations are annotated.
var inscribeKinds = map[ir.SymbolKind]bool{
	ir.KindClass: true, ir.KindFunction: true, ir.KindTrait: true,
}

// PlanInscribe works out which inline `@feature` annotations to insert. It
// annotates each accepted feature's top-level symbols, skipping any symbol
// that already carries a feature annotation — so a re-run is a no-op.
func PlanInscribe(modules []ir.Module, reg *adapters.Registry, accepted []FeatureSymbols) (InscribePlan, error) {
	wanted := map[string]string{}
	for _, fs := range accepted {
		for _, s := range fs.Symbols {
			if _, dup := wanted[s]; !dup {
				wanted[s] = fs.Feature
			}
		}
	}

	var plan InscribePlan
	for mi := range modules {
		mod := &modules[mi]
		ad := reg.For(mod.File)
		if ad == nil {
			continue
		}
		for si := range mod.Symbols {
			sym := &mod.Symbols[si]
			feature, ok := wanted[sym.FQN]
			if !ok || sym.EnclosingFQN != "" || !inscribeKinds[sym.Kind] {
				continue
			}
			if hasFeatureAnnotation(sym) {
				plan.AlreadyMarked = append(plan.AlreadyMarked, sym.FQN)
				continue
			}
			text, err := ad.RenderAnnotationSuggestion(*sym, []adapters.AnnotationSuggestion{{
				Annotation: "feature",
				Args:       []interface{}{feature},
			}})
			if err != nil {
				return plan, err
			}
			plan.Edits = append(plan.Edits, InscribeEdit{
				File: mod.File, Line: sym.Line, Symbol: sym.FQN,
				Feature: feature, Text: text,
			})
		}
	}
	sort.Strings(plan.AlreadyMarked)
	sort.Slice(plan.Edits, func(i, j int) bool {
		if plan.Edits[i].File != plan.Edits[j].File {
			return plan.Edits[i].File < plan.Edits[j].File
		}
		return plan.Edits[i].Line < plan.Edits[j].Line
	})
	return plan, nil
}

// ApplyInscribe writes the planned annotations into source. Each touched file
// is re-parsed after modification; a file whose edit breaks parsing is left
// untouched and reported, never half-written.
func ApplyInscribe(ctx context.Context, plan InscribePlan, reg *adapters.Registry, absPath func(string) string) InscribeResult {
	byFile := map[string][]InscribeEdit{}
	for _, e := range plan.Edits {
		byFile[e.File] = append(byFile[e.File], e)
	}
	files := make([]string, 0, len(byFile))
	for f := range byFile {
		files = append(files, f)
	}
	sort.Strings(files)

	var res InscribeResult
	for _, file := range files {
		abs := absPath(file)
		src, err := os.ReadFile(abs)
		if err != nil {
			res.Failed = append(res.Failed, InscribeFailure{File: file, Reason: "read: " + err.Error()})
			continue
		}
		ad := reg.For(abs)
		if ad == nil {
			res.Failed = append(res.Failed, InscribeFailure{File: file, Reason: "no adapter for file"})
			continue
		}

		updated := insertAnnotations(string(src), byFile[file])
		if _, perr := ad.Parse(ctx, file, []byte(updated)); perr != nil {
			res.Failed = append(res.Failed, InscribeFailure{File: file, Reason: "annotation broke parsing: " + perr.Error()})
			continue
		}
		if werr := os.WriteFile(abs, []byte(updated), 0o644); werr != nil {
			res.Failed = append(res.Failed, InscribeFailure{File: file, Reason: "write: " + werr.Error()})
			continue
		}
		res.FilesChanged = append(res.FilesChanged, file)
		res.AnnotationsInserted += len(byFile[file])
		res.Applied = append(res.Applied, byFile[file]...)
	}
	return res
}

// Uninscribe reverses an inline inscribe: it removes exactly the text blocks
// a prior inscribe recorded, leaving any hand-written annotation untouched.
// Each touched file is re-parsed; a removal that breaks parsing is rejected.
func Uninscribe(ctx context.Context, edits []InscribeEdit, reg *adapters.Registry, absPath func(string) string) InscribeResult {
	byFile := map[string][]InscribeEdit{}
	for _, e := range edits {
		byFile[e.File] = append(byFile[e.File], e)
	}
	files := make([]string, 0, len(byFile))
	for f := range byFile {
		files = append(files, f)
	}
	sort.Strings(files)

	var res InscribeResult
	for _, file := range files {
		abs := absPath(file)
		src, err := os.ReadFile(abs)
		if err != nil {
			res.Failed = append(res.Failed, InscribeFailure{File: file, Reason: "read: " + err.Error()})
			continue
		}
		ad := reg.For(abs)
		if ad == nil {
			res.Failed = append(res.Failed, InscribeFailure{File: file, Reason: "no adapter for file"})
			continue
		}
		blocks := make([]string, 0, len(byFile[file]))
		for _, e := range byFile[file] {
			blocks = append(blocks, e.Text)
		}
		updated, removed := removeBlocks(string(src), blocks)
		if removed == 0 {
			continue
		}
		if _, perr := ad.Parse(ctx, file, []byte(updated)); perr != nil {
			res.Failed = append(res.Failed, InscribeFailure{File: file, Reason: "removal broke parsing: " + perr.Error()})
			continue
		}
		if werr := os.WriteFile(abs, []byte(updated), 0o644); werr != nil {
			res.Failed = append(res.Failed, InscribeFailure{File: file, Reason: "write: " + werr.Error()})
			continue
		}
		res.FilesChanged = append(res.FilesChanged, file)
		res.AnnotationsRemoved += removed
	}
	return res
}

// removeBlocks deletes the first occurrence of each block (a run of
// consecutive lines) from src, returning the result and the count removed.
func removeBlocks(src string, blocks []string) (string, int) {
	lines := strings.Split(src, "\n")
	removed := 0
	for _, block := range blocks {
		blk := strings.Split(strings.TrimRight(block, "\n"), "\n")
		if idx := findBlock(lines, blk); idx >= 0 {
			lines = append(lines[:idx], lines[idx+len(blk):]...)
			removed++
		}
	}
	return strings.Join(lines, "\n"), removed
}

// findBlock returns the index where blk first appears as consecutive lines.
func findBlock(lines, blk []string) int {
	if len(blk) == 0 {
		return -1
	}
	for i := 0; i+len(blk) <= len(lines); i++ {
		match := true
		for j := range blk {
			if lines[i+j] != blk[j] {
				match = false
				break
			}
		}
		if match {
			return i
		}
	}
	return -1
}

// insertAnnotations splices each edit's text block in above its declaration
// line. Edits are applied bottom-up so earlier insertions do not shift the
// line numbers of later ones.
func insertAnnotations(src string, edits []InscribeEdit) string {
	lines := strings.Split(src, "\n")
	ordered := append([]InscribeEdit(nil), edits...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].Line > ordered[j].Line })

	for _, e := range ordered {
		idx := e.Line - 1
		if idx < 0 {
			idx = 0
		}
		if idx > len(lines) {
			idx = len(lines)
		}
		block := strings.Split(strings.TrimRight(e.Text, "\n"), "\n")
		lines = append(lines[:idx], append(block, lines[idx:]...)...)
	}
	return strings.Join(lines, "\n")
}

// hasFeatureAnnotation reports whether a symbol already carries a Lattice
// feature annotation, in code or merged from a sidecar.
func hasFeatureAnnotation(sym *ir.Symbol) bool {
	for _, a := range sym.Annotations {
		if a.Kind == "feature" || a.Kind == "module_feature" {
			return true
		}
	}
	return false
}
