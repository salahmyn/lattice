package scip

// @module-feature scip

import (
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	scippb "github.com/sourcegraph/scip/bindings/go/scip"
	"google.golang.org/protobuf/proto"

	"github.com/salahmyn/lattice/pkg/lattice/adapters"
)

// OrchestrateRoots runs every adapter's SCIP indexer in each of indexRoots
// (directories inside codeRoot) instead of in codeRoot itself, for a
// monorepo whose packages each carry their own build configuration (a
// tsconfig.json per package and none at the root). Each language's runs
// are merged into the one <lang>.scip in outDir that Load reads, with every
// document path rebased onto codeRoot, so blast radius and flow tracing see
// the same paths as an index run from the root. A language no root indexed
// keeps whatever index it had.
func OrchestrateRoots(ctx context.Context, codeRoot string, indexRoots []string, outDir string, reg *adapters.Registry, timeout time.Duration) []IndexResult {
	_ = os.MkdirAll(outDir, 0o755)

	type part struct {
		path   string // the index this run wrote
		prefix string // the index root, relative to codeRoot (slash form)
	}
	parts := map[string][]part{}
	var results []IndexResult
	for i, root := range indexRoots {
		rel, err := filepath.Rel(codeRoot, root)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			results = append(results, IndexResult{Error: fmt.Sprintf("index root %s is outside the code root %s", root, codeRoot)})
			continue
		}
		prefix := filepath.ToSlash(rel)
		if prefix == "." {
			prefix = ""
		}
		for _, a := range reg.All() {
			out := filepath.Join(outDir, fmt.Sprintf("%s.root%d.part.scip", a.Name(), i))
			cmd, err := a.SCIPIndexerCommand(root, out)
			if err != nil || len(cmd) == 0 {
				continue
			}
			res := runIndexer(ctx, root, a.Name(), out, cmd, timeout)
			res.Root = root
			results = append(results, res)
			if res.OK {
				parts[a.Name()] = append(parts[a.Name()], part{path: out, prefix: prefix})
			} else {
				_ = os.Remove(out)
			}
		}
	}

	langs := make([]string, 0, len(parts))
	for lang := range parts {
		langs = append(langs, lang)
	}
	sort.Strings(langs)
	for _, lang := range langs {
		merged := &scippb.Index{}
		for _, p := range parts[lang] {
			idx, err := readIndex(p.path)
			_ = os.Remove(p.path)
			if err != nil {
				results = append(results, IndexResult{Language: lang, OutputPath: p.path, Error: err.Error()})
				continue
			}
			mergeInto(merged, idx, p.prefix, codeRoot)
		}
		final := filepath.Join(outDir, lang+".scip")
		data, err := proto.Marshal(merged)
		if err == nil {
			err = os.WriteFile(final, data, 0o644)
		}
		if err != nil {
			results = append(results, IndexResult{Language: lang, OutputPath: final, Error: err.Error()})
		}
	}
	return results
}

// mergeInto appends idx's documents to merged, rebasing each document path
// from the index root onto the code root.
// @enforces scip:INV-2
func mergeInto(merged, idx *scippb.Index, prefix, codeRoot string) {
	if merged.Metadata == nil && idx.Metadata != nil {
		md := proto.Clone(idx.Metadata).(*scippb.Metadata)
		md.ProjectRoot = "file://" + filepath.ToSlash(codeRoot)
		merged.Metadata = md
	}
	for _, doc := range idx.Documents {
		if prefix != "" {
			doc.RelativePath = path.Join(prefix, doc.RelativePath)
		}
		merged.Documents = append(merged.Documents, doc)
	}
	merged.ExternalSymbols = append(merged.ExternalSymbols, idx.ExternalSymbols...)
}

func readIndex(p string) (*scippb.Index, error) {
	data, err := os.ReadFile(p)
	if err != nil {
		return nil, err
	}
	idx := &scippb.Index{}
	if err := proto.Unmarshal(data, idx); err != nil {
		return nil, err
	}
	return idx, nil
}
