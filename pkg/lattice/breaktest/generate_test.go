package breaktest

import (
	"errors"
	"strings"
	"testing"
)

// lineOf is the 1-based line of the first line containing sub.
func lineOf(t *testing.T, src, sub string) int {
	t.Helper()
	for i, l := range strings.Split(src, "\n") {
		if strings.Contains(l, sub) {
			return i + 1
		}
	}
	t.Fatalf("%q not in source", sub)
	return 0
}

func gen(t *testing.T, lang, file, src, sub, name string) (string, error) {
	t.Helper()
	b, err := Generate(lang, file, []byte(src), Symbol{Name: name, Kind: "function", Line: lineOf(t, src, sub)})
	if err != nil {
		return "", err
	}
	return string(b.Apply([]byte(src))), nil
}

// The Go break goes after the opening brace and keeps the rest of the body.
// @verifies breaktest:INV-1
func TestGenerateGo(t *testing.T) {
	const src = `package p

import "errors"

func Plain(x int) {
	println(x)
}

func One(x int) (int, error) {
	return x, errors.New("x")
}

func Named(x int) (n int, err error) {
	n = x
	return
}

func Generic[T any, K comparable](m map[K]T, k K) (T, bool) {
	v, ok := m[k]
	return v, ok
}

type S struct{}

func (s *S) Method(a, b string) (*S, []string, error) {
	return s, []string{a, b}, nil
}

func Single() string { return "x" }

func Multi(
	a int,
	b int,
) (
	int,
	error,
) {
	return a + b, nil
}
`
	cases := []struct{ sub, name, want string }{
		{"func Plain", "Plain", "func Plain(x int) { if true { return }; \n"},
		{"func One", "One", "{ if true { var r0 int; var r1 error; return r0, r1 }; \n"},
		{"func Named", "Named", "{ if true { return }; \n"},
		{"func Generic", "Generic", "{ if true { var r0 T; var r1 bool; return r0, r1 }; \n"},
		{"func (s *S) Method", "Method", "{ if true { var r0 *S; var r1 []string; var r2 error; return r0, r1, r2 }; \n"},
		{"func Single", "Single", "{ if true { var r0 string; return r0 };  return \"x\" }"},
		{"func Multi", "Multi", "{ if true { var r0 int; var r1 error; return r0, r1 }; \n"},
	}
	for _, c := range cases {
		got, err := gen(t, "go", "p.go", src, c.sub, c.name)
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		if !strings.Contains(got, c.want) {
			t.Errorf("%s: want %q in\n%s", c.name, c.want, got)
		}
		// Everything else stays, so the file still parses.
		if _, err := parse(goGrammar(), []byte(got)); err != nil {
			t.Errorf("%s: broken file does not parse: %v", c.name, err)
		}
	}
	// A function that is not there, or a bodyless declaration, has no break.
	if _, err := gen(t, "go", "p.go", src, "type S struct", "S"); err == nil {
		t.Error("a type got a break")
	}
}

// A function with no body (assembly stub) reads unknown, not a failed break.
// @verifies breaktest:INV-1
func TestGenerateGoBodyless(t *testing.T) {
	_, err := gen(t, "go", "p.go", "package p\n\nfunc Asm(x int) int\n", "func Asm", "Asm")
	var nb *NoBreak
	if !errors.As(err, &nb) {
		t.Fatalf("want NoBreak, got %v", err)
	}
}

// @verifies breaktest:INV-1
func TestGenerateTypeScript(t *testing.T) {
	const src = `export function decl(a: number): number {
  return a + 1;
}

export async function* gen<T>(items: T[]): AsyncGenerator<T> {
  yield* items;
}

export class Box {
  constructor(private v: number) {
    this.v = v;
  }
  get value(): number {
    return this.v;
  }
  set value(n: number) {
    this.v = n;
  }
  method(x: string): string {
    return x;
  }
}

export const arrow = (a: number): number => {
  return a * 2;
};

export const short = (a: number) => a * 2;

export const NOT_FN = 42;

const fnExpr = function (a: number) {
  return a;
};
`
	ok := []struct{ sub, name string }{
		{"export function decl", "decl"}, {"function* gen", "gen"}, {"  method(", "method"},
		{"export const arrow", "arrow"}, {"const fnExpr", "fnExpr"}, {"get value", "value"},
	}
	for _, c := range ok {
		got, err := gen(t, "typescript", "src/a.ts", src, c.sub, c.name)
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		if !strings.Contains(got, "{ if (true) { return undefined as any; } ") {
			t.Errorf("%s: no break in\n%s", c.name, got)
		}
		if n := strings.Count(got, "return undefined as any"); n != 1 {
			t.Errorf("%s: %d breaks inserted", c.name, n)
		}
	}
	for _, c := range []struct{ sub, name, why string }{
		{"constructor(", "constructor", "constructor"},
		{"set value", "value", "setter"},
	} {
		_, err := gen(t, "typescript", "src/a.ts", src, c.sub, c.name)
		var nb *NoBreak
		if !errors.As(err, &nb) || !strings.Contains(nb.Reason, c.why) {
			t.Errorf("%s: want NoBreak(%s), got %v", c.name, c.why, err)
		}
	}
}

// An arrow with an expression body keeps its expression referenced: the body
// becomes (true ? (undefined as any) : (expr)), and the file still parses. A
// const that is not a function reads unknown, "not a function".
// @verifies breaktest:INV-1
func TestGenerateExpressionArrow(t *testing.T) {
	const src = `export const short = (a: number) => a * 2;

export const obj = (a: number) => ({ a });

export const multi = async (
  a: number,
  b: number,
): Promise<number> =>
  a + b;

export const NOT_FN = 42;

class Svc {
  field = (x: string) => x.trim();
  method(x: string): string {
    return x;
  }
}

const fnExpr = function (a: number) {
  return a;
};
`
	for _, c := range []struct{ sub, name, want string }{
		{"export const short", "short", "(a: number) => (true ? (undefined as any) : (a * 2));"},
		{"export const obj", "obj", "=> (true ? (undefined as any) : (({ a })));"},
		{"export const multi", "multi", "(true ? (undefined as any) : (a + b));"},
		{"field =", "field", "(true ? (undefined as any) : (x.trim()));"},
		{"  method(", "method", "{ if (true) { return undefined as any; } "},
		{"const fnExpr", "fnExpr", "{ if (true) { return undefined as any; } "},
	} {
		got, err := gen(t, "typescript", "src/a.ts", src, c.sub, c.name)
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		if !strings.Contains(got, c.want) {
			t.Errorf("%s: want %q in\n%s", c.name, c.want, got)
		}
		// The expression is still there, and the file still parses.
		if _, err := parse(tsGrammar(), []byte(got)); err != nil {
			t.Errorf("%s: broken file does not parse: %v\n%s", c.name, err, got)
		}
	}
	// JavaScript gets no type assertion.
	got, err := gen(t, "javascript", "a.js", "export const f = (a) => a * 2;\n", "export const f", "f")
	if err != nil || got != "export const f = (a) => (true ? undefined : (a * 2));\n" {
		t.Errorf("js: %v %q", err, got)
	}
	// A const that holds no function, or a call that only contains an arrow.
	for _, c := range []struct{ src, sub, name string }{
		{src, "export const NOT_FN", "NOT_FN"},
		{"export const h = useThing((a: number) => a);\n", "export const h", "h"},
	} {
		_, err := Generate("typescript", "a.ts", []byte(c.src), Symbol{Name: c.name, Kind: "const", Line: lineOf(t, c.src, c.sub)})
		var nb *NoBreak
		if !errors.As(err, &nb) || !strings.Contains(nb.Reason, "not a function") {
			t.Errorf("%s: want NoBreak(not a function), got %v", c.name, err)
		}
	}
}

// JavaScript gets `return undefined` with no type assertion.
// @verifies breaktest:INV-1
func TestGenerateJavaScript(t *testing.T) {
	const src = "function f(a) {\n  return a;\n}\n\nconst g = (a) => {\n  return a;\n};\n"
	for _, c := range []struct{ sub, name string }{{"function f", "f"}, {"const g", "g"}} {
		got, err := gen(t, "javascript", "a.js", src, c.sub, c.name)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if !strings.Contains(got, "{ if (true) { return undefined; } ") || strings.Contains(got, " as any") {
			t.Errorf("%s: %s", c.name, got)
		}
	}
	// JSX needs the tsx grammar.
	const jsx = "export function C() {\n  return <div/>;\n}\n"
	got, err := gen(t, "javascript", "c.jsx", jsx, "export function C", "C")
	if err != nil || !strings.Contains(got, "return undefined;") {
		t.Errorf("jsx: %v %s", err, got)
	}
}

// @verifies breaktest:INV-1
func TestGeneratePython(t *testing.T) {
	const src = `import os


def plain(x):
    """Doc."""
    return x


class K:
    @staticmethod
    def method(a, b):
        if a:
            return b
        return a

    def inline(self): return 1


async def co(x):
	return x
`
	got, err := gen(t, "python", "m.py", src, "def plain", "plain")
	if err != nil || !strings.Contains(got, "def plain(x):\n    return None\n    \"\"\"Doc.\"\"\"") {
		t.Errorf("plain: %v\n%s", err, got)
	}
	got, err = gen(t, "python", "m.py", src, "def method", "method")
	if err != nil || !strings.Contains(got, "    def method(a, b):\n        return None\n        if a:") {
		t.Errorf("method: %v\n%s", err, got)
	}
	// A decorator line is part of the header.
	if _, err = gen(t, "python", "m.py", src, "@staticmethod", "method"); err != nil {
		t.Errorf("decorated: %v", err)
	}
	got, err = gen(t, "python", "m.py", src, "async def co", "co")
	if err != nil || !strings.Contains(got, "async def co(x):\n\treturn None\n\treturn x") {
		t.Errorf("tab indent: %v\n%s", err, got)
	}
	var nb *NoBreak
	if _, err = gen(t, "python", "m.py", src, "def inline", "inline"); !errors.As(err, &nb) {
		t.Errorf("inline body: want NoBreak, got %v", err)
	}
}

// SQL and every language without a generator read unknown.
// @verifies breaktest:INV-1
func TestGenerateNoGenerator(t *testing.T) {
	for _, lang := range []string{"postgres", "php", "rust"} {
		_, err := Generate(lang, "x", []byte("select 1;"), Symbol{Name: "f", Line: 1})
		var nb *NoBreak
		if !errors.As(err, &nb) || !strings.Contains(nb.Reason, "add a script") {
			t.Errorf("%s: %v", lang, err)
		}
	}
}
