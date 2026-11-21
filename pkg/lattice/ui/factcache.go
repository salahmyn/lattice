package ui

// @module-feature ui

import (
	"context"
	"errors"
	"sync"

	"github.com/salahmyn/lattice/pkg/lattice/audit"
	"github.com/salahmyn/lattice/pkg/lattice/schema"
	"github.com/salahmyn/lattice/pkg/lattice/slice"
)

// The knowledge graph the read-only builder makes, and the audit over it,
// are what /api/graph and /api/v2 spend their time on. serve keeps the last
// of them in memory, keyed by the fingerprint of every input they read
// (inputsFingerprint), and computes them again as soon as any input
// changes. When the fingerprint cannot be computed the cache is not used.

// cacheHeader tells a client how a response's facts were obtained.
const cacheHeader = "X-Lattice-Cache"

// Values of cacheHeader.
const (
	cacheHit    = "hit"    // the inputs are unchanged since the facts were computed
	cacheMiss   = "miss"   // computed for this request, and kept
	cacheBypass = "bypass" // computed for this request, not kept: the inputs could not be named
)

// factsCacheSize is how many fingerprints are kept: the current one and the
// one before it, so a request that was computing while an edit landed can
// still finish into the cache without holding more.
const factsCacheSize = 2

// facts is what one request reads from the live workspace: the graph, the
// audit over it, and the git state the audit took. Nothing writes to it once
// it is computed; requests share it.
type facts struct {
	kg     schema.KnowledgeGraph
	report audit.Report
	head   string // HEAD of the repository holding lattice/, "" when none
	dirty  *bool  // nil when git could not say
	// sg is the slice graph over the same build as kg; nil when the server
	// has no SliceBuilder or the build had no reach graph.
	sg *slice.Graph
}

type factsEntry struct {
	key  string
	done chan struct{} // closed once val and err are set
	val  facts
	err  error
}

// factsCache holds the last facts by fingerprint. Requests with the same
// fingerprint share one computation; a request with a new fingerprint never
// waits on an older one.
type factsCache struct {
	mu      sync.Mutex
	entries []*factsEntry // newest first, at most factsCacheSize
}

// get returns the facts for key, computing them with compute when no entry
// holds them. A failed computation is not kept.
// @enforces ui:INV-3
func (c *factsCache) get(ctx context.Context, key string, compute func(context.Context) (facts, error)) (facts, string, error) {
	c.mu.Lock()
	for _, e := range c.entries {
		if e.key != key {
			continue
		}
		c.mu.Unlock()
		select {
		case <-e.done:
			return e.val, cacheHit, e.err
		case <-ctx.Done():
			return facts{}, cacheHit, ctx.Err()
		}
	}
	e := &factsEntry{key: key, done: make(chan struct{})}
	c.entries = append([]*factsEntry{e}, c.entries...)
	if len(c.entries) > factsCacheSize {
		c.entries = c.entries[:factsCacheSize]
	}
	c.mu.Unlock()

	finished := false
	defer func() {
		if !finished { // compute panicked: release the waiters, keep nothing
			e.err = errors.New("the graph and audit could not be computed")
		}
		if e.err != nil {
			c.drop(e)
		}
		close(e.done)
	}()
	// The computation is shared: a client that goes away does not cancel it
	// for the requests waiting on it.
	e.val, e.err = compute(context.WithoutCancel(ctx))
	finished = true
	return e.val, cacheMiss, e.err
}

func (c *factsCache) drop(e *factsEntry) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for i, x := range c.entries {
		if x == e {
			c.entries = append(c.entries[:i:i], c.entries[i+1:]...)
			return
		}
	}
}

// facts returns the live workspace's graph and audit, from the cache while
// the inputs' fingerprint is unchanged. Only the read-only builder's graph is
// kept: the other builder may write (it labels and saves entry points).
func (s *Server) facts(ctx context.Context) (facts, string, error) {
	if s.opts.ReadOnlyGraphBuilder == nil {
		f, err := s.computeFacts(ctx)
		return f, cacheBypass, err
	}
	fp := s.fingerprint
	if fp == nil {
		fp = inputsFingerprint
	}
	key, err := fp(ctx, s.ws)
	if err != nil {
		f, err := s.computeFacts(ctx)
		return f, cacheBypass, err
	}
	return s.cache.get(ctx, key, s.computeFacts)
}

// computeFacts builds the graph read-only (with its slice graph, when the
// server has a SliceBuilder) and audits it.
func (s *Server) computeFacts(ctx context.Context) (facts, error) {
	var (
		kg  schema.KnowledgeGraph
		sg  *slice.Graph
		err error
	)
	if s.opts.SliceBuilder != nil {
		kg, sg, err = s.opts.SliceBuilder(ctx)
	} else {
		kg, err = s.readGraph(ctx)
	}
	if err != nil {
		return facts{}, err
	}
	head, dirty := s.gitState()
	rep := audit.Compute(audit.Collect(s.ws.LatticeDir, kg, dirty != nil && *dirty))
	return facts{kg: kg, report: rep, head: head, dirty: dirty, sg: sg}, nil
}
