package ui

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

// eventBroker is a fan-out SSE broadcaster. One fsnotify goroutine
// watches the lattice/ directory; every connected /api/v1/events
// client receives every event. v0.4.2 — replaces "refresh the
// browser tab after a CLI edit" with auto-refresh.
type eventBroker struct {
	mu       sync.RWMutex
	subs     map[chan event]bool
	stopOnce sync.Once
	stop     chan struct{}
}

type event struct {
	Type string `json:"type"` // "fs.create" | "fs.write" | "fs.remove" | "fs.rename"
	Path string `json:"path"` // workspace-relative
}

// newEventBroker starts the fsnotify watcher rooted at latticeDir and
// returns a broker ready to serve SSE clients. The watcher runs until
// Stop() is called or ctx is cancelled.
func newEventBroker(latticeDir string) *eventBroker {
	b := &eventBroker{
		subs: map[chan event]bool{},
		stop: make(chan struct{}),
	}
	w, err := fsnotify.NewWatcher()
	if err != nil {
		// If fsnotify is unavailable (rare), serve a stub broker that
		// never emits — the page just doesn't auto-refresh, no crash.
		return b
	}
	// Watch features/, entry-points/, import/, and config files.
	// Recursive watches must be added per-directory; we walk once at
	// boot and add every subdir under the lattice dir except .cache/.
	_ = filepath.WalkDir(latticeDir, func(p string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			name := d.Name()
			if name == ".cache" || name == ".rejected" {
				return filepath.SkipDir
			}
			_ = w.Add(p)
		}
		return nil
	})
	go b.watch(w, latticeDir)
	return b
}

// watch forwards fsnotify events into the broker's fan-out channel.
// New subdirectories are auto-added so a newly-created
// lattice/entry-points/ep/cli/ is picked up without a restart.
func (b *eventBroker) watch(w *fsnotify.Watcher, root string) {
	defer w.Close()
	for {
		select {
		case <-b.stop:
			return
		case ev, ok := <-w.Events:
			if !ok {
				return
			}
			rel, err := filepath.Rel(root, ev.Name)
			if err != nil {
				rel = ev.Name
			}
			// Pick up new directories so deeply-nested manifest writes
			// (lattice/entry-points/ep/cli/x.yaml) reach us.
			if ev.Op&fsnotify.Create != 0 {
				if st, err := os.Stat(ev.Name); err == nil && st.IsDir() {
					_ = w.Add(ev.Name)
				}
			}
			b.broadcast(event{Type: fsOp(ev.Op), Path: filepath.ToSlash(rel)})
		case _, ok := <-w.Errors:
			if !ok {
				return
			}
		}
	}
}

// fsOp returns the dotted-prefix event type the JS side branches on.
func fsOp(op fsnotify.Op) string {
	switch {
	case op&fsnotify.Create != 0:
		return "fs.create"
	case op&fsnotify.Write != 0:
		return "fs.write"
	case op&fsnotify.Remove != 0:
		return "fs.remove"
	case op&fsnotify.Rename != 0:
		return "fs.rename"
	case op&fsnotify.Chmod != 0:
		return "fs.chmod"
	}
	return "fs.unknown"
}

// broadcast sends ev to every subscriber, non-blocking — a slow client
// drops events rather than back-pressuring the watcher.
func (b *eventBroker) broadcast(ev event) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	for ch := range b.subs {
		select {
		case ch <- ev:
		default:
			// Drop. SSE clients reconnect on the next interesting page
			// load anyway, and a missed mtime event matters less than
			// blocking the watcher.
		}
	}
}

// subscribe returns a channel the SSE handler reads from. The caller
// must call unsubscribe when done.
func (b *eventBroker) subscribe() chan event {
	ch := make(chan event, 64)
	b.mu.Lock()
	b.subs[ch] = true
	b.mu.Unlock()
	return ch
}

func (b *eventBroker) unsubscribe(ch chan event) {
	b.mu.Lock()
	delete(b.subs, ch)
	b.mu.Unlock()
	close(ch)
}

// Stop terminates the watcher goroutine; safe to call multiple times.
func (b *eventBroker) Stop() {
	b.stopOnce.Do(func() { close(b.stop) })
}

// apiEvents is the SSE endpoint. Pages open an EventSource against it
// on load, then branch on event.path to decide whether to reload —
// e.g. /features auto-refreshes on any features/*.yaml change but
// ignores import/ activity.
func (s *Server) apiEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	if s.events == nil {
		http.Error(w, "event broker not initialised", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no") // disable nginx-style buffering

	ch := s.events.subscribe()
	defer s.events.unsubscribe(ch)

	// Send a hello event so EventSource.onopen fires immediately —
	// helps the client confirm wiring during a manual test.
	fmt.Fprintf(w, "event: hello\ndata: {\"v\":\"v0.4.2\"}\n\n")
	flusher.Flush()

	ctx := r.Context()
	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-heartbeat.C:
			// SSE comments don't trigger client handlers but keep
			// intermediaries from idle-closing the connection.
			fmt.Fprintf(w, ": heartbeat\n\n")
			flusher.Flush()
		case ev, ok := <-ch:
			if !ok {
				return
			}
			payload, _ := json.Marshal(ev)
			fmt.Fprintf(w, "event: change\ndata: %s\n\n", payload)
			flusher.Flush()
		}
	}
}

// installEventBroker is called from Server.routes() to wire up the
// broker exactly once per Server instance.
func (s *Server) installEventBroker(ctx context.Context) {
	if s.events != nil {
		return
	}
	s.events = newEventBroker(s.ws.LatticeDir)
	go func() {
		<-ctx.Done()
		s.events.Stop()
	}()
}

// Ensures strings is imported (kept here for future helper additions).
var _ = strings.TrimSpace
