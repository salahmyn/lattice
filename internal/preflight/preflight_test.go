package preflight

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/salahmyn/lattice/pkg/lattice/config"
)

func TestCommandWords(t *testing.T) {
	for _, tc := range []struct {
		line string
		want []string
	}{
		{"npm ci", []string{"npm"}},
		{"FOO=1 BAR='a b' npm run build", []string{"npm"}},
		{"cd apps/web && pnpm build | tee build.log; echo 'a && b'", []string{"pnpm", "tee"}},
		{"docker compose up -d 2>&1 >/dev/null", []string{"docker"}},
		{"env -i PATH=/bin deno test", []string{"deno"}},
		{"if [ -f x ]; then make; fi", []string{"make"}},
		{"(cd web && npx vitest run {files} -t {filter})", []string{"npx"}},
		{"for f in a b; do touch $f; done", []string{"touch"}},
		{"$TOOL run", nil},
		{"python -m pytest {files} -k {filter} # pytest", []string{"python"}},
		{"\"./scripts/ci test.sh\" --fast", []string{"./scripts/ci test.sh"}},
	} {
		var got []string
		for _, w := range commandWords(tc.line) {
			if checkable(w) {
				got = append(got, w)
			}
		}
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("commandWords(%q) = %q, want %q", tc.line, got, tc.want)
		}
	}
}

func TestLowerBound(t *testing.T) {
	for in, want := range map[string]string{
		">=18": "18", ">= 18.17.0": "18.17.0", "^20.11": "20.11", "~18.2.1": "18.2.1",
		"18.x": "18", "v20": "20", ">=18 <23": "18",
		"16 || 18": "", ">18": "", "<20": "", "latest": "", "": "",
	} {
		got, ok := lowerBound(in)
		if got != want || ok != (want != "") {
			t.Errorf("lowerBound(%q) = %q, %v; want %q", in, got, ok, want)
		}
	}
	if compareVersions("v18.19.0", "18.17") <= 0 || compareVersions("v16.20.2", "18") >= 0 || compareVersions("v18.0.0-pre", "18") != 0 {
		t.Error("compareVersions orders versions wrongly")
	}
}

// The keys of break's requirements name the config entry each comes from; a
// path-named executable is not checked once break.setup could have made it,
// and pnpm is not looked up after corepack puts it on PATH.
func TestBreakRequirements(t *testing.T) {
	base := t.TempDir()
	b := config.Break{
		Setup: []string{"corepack enable && pnpm install"},
		Runners: []config.Runner{
			{Name: "vitest", Match: []string{"**/*.test.ts"}, Command: []string{"node_modules/.bin/vitest", "{files}"}, Dir: "apps/web"},
			{Name: "deno", Match: []string{"**/*_test.ts"}, Command: []string{"deno", "test", "{files}"}},
		},
		Commands: map[string]string{"python": "python3 -m pytest {files} -k {filter}"},
		Services: []config.Service{{Name: "db", Address: "localhost:5432"}},
	}
	var got []string
	for _, r := range Break(b, base) {
		got = append(got, string(r.Kind)+" "+r.Key+" "+r.Target)
	}
	want := []string{
		"executable break.setup[0] corepack",
		"path break.runners[0].dir apps/web",
		"executable break.runners[1].command deno",
		"executable break.commands.python python3",
		"service break.services[0].address localhost:5432",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("requirements:\n got %q\nwant %q", got, want)
	}
}

// Every failed requirement is a problem with its code, key and fix: a
// missing executable, a missing dir, a Node minimum and a closed port, all
// in one pass. A port with a listener passes.
func TestCheckReportsEveryProblem(t *testing.T) {
	dir := t.TempDir()
	open, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skip("cannot listen on loopback: " + err.Error())
	}
	defer open.Close()
	closed, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	closedAddr := closed.Addr().String()
	_ = closed.Close()

	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"engines":{"node":">=999"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	rt := config.Runtime{
		CleanInstall: "no-such-tool-5e1a install",
		Build:        "node build.js",
		Services:     []config.Service{{Name: "api", Address: open.Addr().String()}, {Name: "db", Address: closedAddr}},
	}
	reqs := append(Runtime(rt, dir), Requirement{Kind: KindPath, Key: "break.runners[0].dir", Target: "missing", Dir: dir})
	results := Check(context.Background(), reqs)
	codes := map[string]string{}
	for _, p := range Problems(results) {
		if p.Next == "" || p.Detail == "" {
			t.Errorf("problem without detail or next: %+v", p)
		}
		codes[p.Key+" "+p.Code] = p.Detail
	}
	for _, want := range []string{
		"runtime.clean_install " + CodeExecNotFound,
		"runtime.services[1].address " + CodeServiceDown,
		"break.runners[0].dir " + CodePathNotFound,
	} {
		if _, ok := codes[want]; !ok {
			t.Errorf("%s not reported: %v", want, codes)
		}
	}
	if _, ok := codes["runtime.services[0].address "+CodeServiceDown]; ok {
		t.Error("a port with a listener was reported unreachable")
	}
	if _, err := LookupExecutable("node", dir); err == nil {
		if _, ok := codes["runtime.build "+CodeVersionTooOld]; !ok {
			t.Errorf("node below engines.node not reported: %v", codes)
		}
	}
}

// check needs only what it lists by: the runners, never break.setup,
// break.commands or break.services.
func TestRunnersRequirements(t *testing.T) {
	b := config.Break{
		Setup: []string{"make deps"},
		Runners: []config.Runner{
			{Name: "web", Command: []string{"npx", "vitest", "{files}"}, Dir: "web"},
			{Name: "local", Command: []string{"./bin/test", "{files}"}},
		},
		Commands: map[string]string{"python": "python3 -m pytest {files}"},
		Services: []config.Service{{Address: "localhost:5432"}},
	}
	var got []string
	for _, r := range Runners(b, t.TempDir()) {
		got = append(got, string(r.Kind)+" "+r.Key+" "+r.Target)
	}
	want := []string{
		"path break.runners[0].dir web",
		"executable break.runners[0].command npx",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("requirements:\n got %q\nwant %q", got, want)
	}
}

// A command that runs docker needs its daemon as well as its CLI.
func TestDockerRequirement(t *testing.T) {
	var got []string
	for _, r := range Runtime(config.Runtime{CleanInstall: "docker compose up -d && npm ci"}, t.TempDir()) {
		got = append(got, string(r.Kind)+" "+r.Key+" "+r.Target)
	}
	want := []string{
		"executable runtime.clean_install docker",
		"docker runtime.clean_install docker",
		"executable runtime.clean_install npm",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("requirements:\n got %q\nwant %q", got, want)
	}
}
