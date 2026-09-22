package session

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// Registry lazily opens one Store per run under root/runs/<runId>/ and fans
// every append out to daemon-wide listeners.
type Registry struct {
	Root        string
	MaxDisputes int
	onVerdict   func(runID string, v VerdictState)

	mu        sync.Mutex
	stores    map[string]*Store
	listeners map[int]func(runID string, m Message)
	nextID    int
}

// RegistryOption configures a Registry beyond its required arguments.
type RegistryOption func(*Registry)

// WithOnVerdict calls fn after any message that changes a run's verdict state.
func WithOnVerdict(fn func(runID string, v VerdictState)) RegistryOption {
	return func(r *Registry) { r.onVerdict = fn }
}

// NewRegistry returns a Registry over root.
func NewRegistry(root string, maxDisputes int, opts ...RegistryOption) *Registry {
	r := &Registry{Root: root, MaxDisputes: maxDisputes, stores: map[string]*Store{}, listeners: map[int]func(string, Message){}}
	for _, opt := range opts {
		opt(r)
	}
	return r
}

// Get opens or returns the store for runID. The run directory must already
// exist (the manager creates it), so a typo cannot manufacture a run.
func (r *Registry) Get(runID string) (*Store, error) {
	// runID comes from MCP callers; anything but one plain path element could escape runs/.
	if runID == "" || runID == "." || strings.HasPrefix(runID, "..") || strings.ContainsAny(runID, `/\`) {
		return nil, fmt.Errorf("no run %q", runID)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if s, ok := r.stores[runID]; ok {
		return s, nil
	}
	dir := filepath.Join(r.Root, "runs", runID)
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		return nil, fmt.Errorf("no run %q", runID)
	}
	s, err := Open(dir, r.MaxDisputes)
	if err != nil {
		return nil, err
	}
	s.Subscribe(func(m Message) { r.fanOut(runID, s, m) })
	r.stores[runID] = s
	return s, nil
}

// fanOut runs under the store's lock, so listeners must not call back into
// the store.
func (r *Registry) fanOut(runID string, s *Store, m Message) {
	r.mu.Lock()
	listeners := make([]func(string, Message), 0, len(r.listeners))
	for _, fn := range r.listeners {
		listeners = append(listeners, fn)
	}
	r.mu.Unlock()
	for _, fn := range listeners {
		fn(runID, m)
	}
	if r.onVerdict != nil {
		switch m.Kind {
		case Verdict, Accept, Dispute:
			r.onVerdict(runID, s.verdictLocked())
		}
	}
}

// Listen calls fn for every message appended to any run. The returned
// function removes the listener.
func (r *Registry) Listen(fn func(runID string, m Message)) func() {
	r.mu.Lock()
	defer r.mu.Unlock()
	id := r.nextID
	r.nextID++
	r.listeners[id] = fn
	return func() {
		r.mu.Lock()
		defer r.mu.Unlock()
		delete(r.listeners, id)
	}
}

// RunIDs lists every run directory under root, open or not, newest last.
func (r *Registry) RunIDs() ([]string, error) {
	entries, err := os.ReadDir(filepath.Join(r.Root, "runs"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	ids := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			ids = append(ids, e.Name())
		}
	}
	sort.Strings(ids) // run ids start with a timestamp
	return ids, nil
}

// Evict drops runID's store from memory and closes it, so a destroyed run's
// history is not held forever. A later Get reopens it from disk.
func (r *Registry) Evict(runID string) {
	r.mu.Lock()
	s, ok := r.stores[runID]
	r.mu.Unlock()
	if !ok {
		return
	}
	// Close before forgetting, so no append can land in the old store after
	// Get has reopened the file. fanOut locks r.mu under s.mu, so close must
	// not run under r.mu.
	s.close()
	r.mu.Lock()
	if r.stores[runID] == s {
		delete(r.stores, runID)
	}
	r.mu.Unlock()
}
