package astutil_test

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unsafe"

	sitter "github.com/tree-sitter/go-tree-sitter"
	gogrammar "github.com/tree-sitter/tree-sitter-go/bindings/go"
	tsphp "github.com/tree-sitter/tree-sitter-php/bindings/go"
	tspython "github.com/tree-sitter/tree-sitter-python/bindings/go"
	tsgrammar "github.com/tree-sitter/tree-sitter-typescript/bindings/go"

	"github.com/salahmyn/lattice/pkg/lattice/adapters/astutil"
)

// Every grammar the adapters use must have an ABI the core accepts, or
// SetLanguage refuses it and nothing parses.
func TestParseAcceptsEveryGrammar(t *testing.T) {
	for _, g := range []struct {
		name, src, root string
		lang            unsafe.Pointer
	}{
		{"go", "package p\nfunc F() {}\n", "source_file", gogrammar.Language()},
		{"typescript", "export function f(): number { return 1 }\n", "program", tsgrammar.LanguageTypescript()},
		{"tsx", "export const A = () => <div/>\n", "program", tsgrammar.LanguageTSX()},
		{"python", "def f():\n    return 1\n", "module", tspython.Language()},
		{"php", "<?php\nfunction f() { return 1; }\n", "program", tsphp.LanguagePHP()},
	} {
		lang := sitter.NewLanguage(g.lang)
		v := lang.AbiVersion()
		if v < sitter.MIN_COMPATIBLE_LANGUAGE_VERSION || v > sitter.LANGUAGE_VERSION {
			t.Errorf("%s: grammar ABI %d outside the core's %d..%d", g.name, v, sitter.MIN_COMPATIBLE_LANGUAGE_VERSION, sitter.LANGUAGE_VERSION)
		}
		tree, err := astutil.Parse(context.Background(), lang, []byte(g.src))
		if err != nil {
			t.Errorf("%s: %v", g.name, err)
			continue
		}
		root := tree.RootNode()
		if root.Kind() != g.root || root.HasError() {
			t.Errorf("%s: root %s (error %v), want a clean %s", g.name, root.Kind(), root.HasError(), g.root)
		}
		tree.Close()
	}
}

func TestParseStopsOnADoneContext(t *testing.T) {
	lang := sitter.NewLanguage(gogrammar.Language())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := astutil.Parse(ctx, lang, []byte("package p\n")); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled before the parse: err = %v, want context.Canceled", err)
	}

	// A context that is done once the parse has started: the progress
	// callback must see it and stop the parse part way through.
	src := []byte("package p\n" + strings.Repeat("func f() { x := []int{1, 2, 3}; _ = x }\n", 20000))
	late := &lateCancel{Context: context.Background()}
	if _, err := astutil.Parse(late, lang, src); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled during the parse: err = %v, want context.Canceled", err)
	}
	if late.calls.Load() < 3 {
		t.Fatalf("Err called %d times; the progress callback never ran", late.calls.Load())
	}
}

// lateCancel reports itself cancelled from its second Err call on: Parse's
// own check before parsing passes, the first progress callback stops it.
type lateCancel struct {
	context.Context
	calls atomic.Int32
}

func (c *lateCancel) Done() <-chan struct{}       { return make(chan struct{}) }
func (c *lateCancel) Deadline() (time.Time, bool) { return time.Time{}, false }
func (c *lateCancel) Err() error {
	if c.calls.Add(1) > 1 {
		return context.Canceled
	}
	return nil
}
