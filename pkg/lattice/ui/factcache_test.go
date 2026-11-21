package ui

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/salahmyn/lattice/pkg/lattice/schema"
	"github.com/salahmyn/lattice/pkg/lattice/store"
	"github.com/salahmyn/lattice/pkg/lattice/workspace"
)

// cacheFixture is a git repository with one tracked source file outside
// lattice/, served by a Server whose read-only builder counts its builds.
type cacheFixture struct {
	root   string
	dir    string // lattice/
	srv    *Server
	builds atomic.Int32
}

func newCacheFixture(t *testing.T) *cacheFixture {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	root := t.TempDir()
	if real, err := filepath.EvalSymlinks(root); err == nil {
		root = real
	}
	gitIn(t, root, "init", "-q", "-b", "main")
	gitIn(t, root, "config", "user.email", "dev@example.com")
	gitIn(t, root, "config", "user.name", "dev")
	gitIn(t, root, "config", "commit.gpgsign", "false")
	dir := filepath.Join(root, "lattice")
	writeFile(t, filepath.Join(root, ".gitignore"), "lattice/.cache/\n")
	writeFile(t, filepath.Join(dir, "features", "fees.yaml"), "id: fees\nversion: 1\n")
	writeFile(t, filepath.Join(root, "fees", "fees.go"), "package fees\n")
	gitIn(t, root, "add", "-A")
	gitIn(t, root, "commit", "-q", "-m", "base")

	ws := &workspace.Workspace{LatticeDir: dir, CodeRoots: []workspace.CodeRoot{{Name: "default", Path: root, Abs: root, Available: true}}}
	st := store.OpenFiles(ws)
	if err := st.ReplaceGraph(context.Background(), store.Graph{Blocks: []schema.Block{
		{ID: "feat:fees", Kind: "composite", Title: "fees"},
		{ID: "inv:fees.INV-1", Kind: "invariant", Parent: "feat:fees", Title: "INV-1"},
	}}); err != nil {
		t.Fatal(err)
	}
	gitIn(t, root, "add", "-A")
	gitIn(t, root, "commit", "-q", "-m", "graph")

	f := &cacheFixture{root: root, dir: dir}
	build := func(context.Context) (schema.KnowledgeGraph, error) {
		f.builds.Add(1)
		return schema.KnowledgeGraph{Features: []schema.Manifest{{ID: "fees",
			Invariants: []schema.Invariant{{ID: "INV-1", Statement: "A fee never exceeds five percent."}}}}}, nil
	}
	f.srv = New(ws, Options{Store: st, GraphBuilder: build, ReadOnlyGraphBuilder: build})
	return f
}

// get requests path and returns the cache header.
func (f *cacheFixture) get(t *testing.T, path string) string {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.RemoteAddr = "127.0.0.1:41000"
	req.Host = "127.0.0.1:7070"
	w := httptest.NewRecorder()
	f.srv.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("%s = %d %s", path, w.Code, w.Body.String())
	}
	return w.Header().Get(cacheHeader)
}

// expect requests path and checks the cache header and the build count.
func (f *cacheFixture) expect(t *testing.T, path, cache string, builds int32) {
	t.Helper()
	if got := f.get(t, path); got != cache {
		t.Errorf("%s: %s = %q, want %q", path, cacheHeader, got, cache)
	}
	if got := f.builds.Load(); got != builds {
		t.Errorf("%s: %d builds, want %d", path, got, builds)
	}
}

// A second request with nothing changed reuses the graph and audit, across
// /api/v2 and the map.
// @verifies ui:INV-3
func TestFactsCacheHitWhenNothingChanged(t *testing.T) {
	f := newCacheFixture(t)
	f.expect(t, "/api/v2/health", cacheMiss, 1)
	f.expect(t, "/api/v2/health", cacheHit, 1)
	f.expect(t, "/api/graph", cacheHit, 1)
	f.expect(t, "/api/v2/queue", cacheHit, 1)
}

// Every input change is a miss: a tracked source file outside lattice/
// edited and edited again (its status line stays " M"), an untracked file,
// ingested results under .cache/, and a commit.
// @verifies ui:INV-3
func TestFactsCacheMissOnEveryInputChange(t *testing.T) {
	f := newCacheFixture(t)
	n := int32(0)
	step := func(name string, change func()) {
		t.Helper()
		change()
		n++
		f.expect(t, "/api/graph", cacheMiss, n)
		f.expect(t, "/api/v2/health", cacheHit, n)
		if t.Failed() {
			t.Fatalf("after %s", name)
		}
	}
	src := filepath.Join(f.root, "fees", "fees.go")
	step("first request", func() {})
	// same length each time: only the content tells the edits apart
	step("tracked edit", func() { writeFile(t, src, "package fees // 1\n") })
	step("second tracked edit", func() { writeFile(t, src, "package fees // 2\n") })
	step("untracked file", func() { writeFile(t, filepath.Join(f.root, "fees", "round.go"), "package fees\n") })
	step("results ingested", func() {
		writeFile(t, filepath.Join(f.dir, ".cache", "results", "results.json"), `{"commit":"c0ffee","results":{"fees.testcap":"pass"}}`)
	})
	step("feature edited", func() { writeFile(t, filepath.Join(f.dir, "features", "fees.yaml"), "id: fees\nversion: 2\n") })
	step("commit", func() {
		gitIn(t, f.root, "add", "-A")
		gitIn(t, f.root, "commit", "-q", "-m", "round")
	})
}

// A file written long enough ago is named by its stat alone; edits that
// change it are still misses.
// @verifies ui:INV-3
func TestFactsCacheMissOnEditByStat(t *testing.T) {
	old := racyWindow
	racyWindow = -time.Hour // nothing is recent: every file is named by its stat
	t.Cleanup(func() { racyWindow = old })
	f := newCacheFixture(t)
	src := filepath.Join(f.root, "fees", "fees.go")
	f.expect(t, "/api/v2/health", cacheMiss, 1)
	writeFile(t, src, "package fees // edited\n")
	f.expect(t, "/api/v2/health", cacheMiss, 2)
	writeFile(t, src, "package fees // edited again\n")
	f.expect(t, "/api/v2/health", cacheMiss, 3)
	f.expect(t, "/api/v2/health", cacheHit, 3)
}

// When the inputs cannot be named the cache is not used: a code root git
// does not track (extraction would scan every file in it), or any error from
// the fingerprint.
// @verifies ui:INV-3
func TestFactsCacheBypassWhenFingerprintFails(t *testing.T) {
	f := newCacheFixture(t)
	untracked := t.TempDir()
	f.srv.ws.CodeRoots = append(f.srv.ws.CodeRoots, workspace.CodeRoot{Name: "loose", Path: untracked, Abs: untracked, Available: true})
	if _, err := inputsFingerprint(context.Background(), f.srv.ws); err == nil {
		t.Fatal("a code root outside git was fingerprinted")
	}
	f.expect(t, "/api/v2/health", cacheBypass, 1)
	f.expect(t, "/api/graph", cacheBypass, 2)

	g := newCacheFixture(t)
	g.srv.fingerprint = func(context.Context, *workspace.Workspace) (string, error) { return "", errors.New("unreadable") }
	g.expect(t, "/api/v2/health", cacheBypass, 1)
	g.expect(t, "/api/v2/health", cacheBypass, 2)
}

// Simultaneous requests with the same fingerprint compute once.
// @verifies ui:INV-3
func TestFactsCacheConcurrentRequestsComputeOnce(t *testing.T) {
	f := newCacheFixture(t)
	f.srv.fingerprint = func(context.Context, *workspace.Workspace) (string, error) { return "same", nil }
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	build := f.srv.opts.ReadOnlyGraphBuilder
	f.srv.opts.ReadOnlyGraphBuilder = func(ctx context.Context) (schema.KnowledgeGraph, error) {
		once.Do(func() { close(entered) })
		<-release
		return build(ctx)
	}
	const n = 8
	got := make([]string, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			path := "/api/v2/health"
			if i%2 == 1 {
				path = "/api/graph"
			}
			got[i] = f.get(t, path)
		}()
	}
	<-entered
	time.Sleep(50 * time.Millisecond) // let the others reach the cache
	close(release)
	wg.Wait()
	if b := f.builds.Load(); b != 1 {
		t.Errorf("%d concurrent requests built %d times, want 1", n, b)
	}
	misses := 0
	for _, c := range got {
		if c == cacheMiss {
			misses++
		}
	}
	if misses != 1 {
		t.Errorf("cache statuses %v: want exactly one miss", got)
	}
}

// A request whose fingerprint is new never waits on a computation for an
// older one, and a failed computation is not kept.
// @verifies ui:INV-3
func TestFactsCacheNewFingerprintDoesNotWait(t *testing.T) {
	var c factsCache
	ctx := context.Background()
	entered, release := make(chan struct{}), make(chan struct{})
	go func() {
		_, _, _ = c.get(ctx, "old", func(context.Context) (facts, error) {
			close(entered)
			<-release
			return facts{head: "old"}, nil
		})
	}()
	<-entered
	done := make(chan facts)
	go func() {
		f, _, _ := c.get(ctx, "new", func(context.Context) (facts, error) { return facts{head: "new"}, nil })
		done <- f
	}()
	select {
	case f := <-done:
		if f.head != "new" {
			t.Errorf("new fingerprint served %q", f.head)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a request with a new fingerprint waited on the old computation")
	}
	close(release)

	calls := 0
	failing := func(context.Context) (facts, error) { calls++; return facts{}, errors.New("build failed") }
	for range 2 {
		if _, status, err := c.get(ctx, "broken", failing); err == nil || status != cacheMiss {
			t.Errorf("failed build: status %q, err %v", status, err)
		}
	}
	if calls != 2 {
		t.Errorf("a failed build was kept: %d calls, want 2", calls)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.entries) > factsCacheSize {
		t.Errorf("%d entries kept, want at most %d", len(c.entries), factsCacheSize)
	}
}

// The fingerprint reads no source and runs no build: it is cheap enough to
// take on every request.
func BenchmarkInputsFingerprint(b *testing.B) {
	dir, err := os.Getwd()
	if err != nil {
		b.Fatal(err)
	}
	repo := filepath.Join(dir, "..", "..", "..")
	ws, err := workspace.Open(repo)
	if err != nil {
		b.Skip("no workspace: ", err)
	}
	for b.Loop() {
		if _, err := inputsFingerprint(context.Background(), ws); err != nil {
			b.Skip("fingerprint: ", err)
		}
	}
}

// Requests share the cached graph and audit read-only: every /api/v2
// payload served from the cache, by requests running at once, is the payload
// computed afresh.
// @verifies ui:INV-3
func TestFactsCacheSharedFactsServeTheSamePayloads(t *testing.T) {
	f := newV2Fixture(t)
	f.srv.opts.ReadOnlyGraphBuilder = f.srv.opts.GraphBuilder
	f.srv.fingerprint = func(context.Context, *workspace.Workspace) (string, error) { return "", errors.New("uncached") }
	fresh := map[string]string{}
	for _, ep := range v2Endpoints {
		_, body := f.get(t, ep.path)
		fresh[ep.path] = string(body)
	}
	f.srv.fingerprint = func(context.Context, *workspace.Workspace) (string, error) { return "same", nil }
	var wg sync.WaitGroup
	for range 4 {
		for _, ep := range v2Endpoints {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if _, body := f.get(t, ep.path); string(body) != fresh[ep.path] {
					t.Errorf("%s from the cache differs from the fresh payload:\n%s\nwant:\n%s", ep.path, body, fresh[ep.path])
				}
			}()
		}
	}
	wg.Wait()
}
