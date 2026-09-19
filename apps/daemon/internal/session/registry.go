package session

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
)

// Registry hands out one Store per run, opening each lazily from
// root/runs/<runId>/, and fans every append out to daemon-wide listeners
// such as the event stream and the manifest writer.
type Registry struct {
	Root        string
	MaxDisputes int
	OnVerdict   func(runID string, v VerdictState) // called after any message that changes the verdict state

	mu        sync.Mutex
	stores    map[string]*Store
	listeners map[int]func(runID string, m Message)
	nextID    int
}

// NewRegistry returns a Registry over root.
func NewRegistry(root string, maxDisputes int) *Registry {
	return &Registry{Root: root, MaxDisputes: maxDisputes, stores: map[string]*Store{}, listeners: map[int]func(string, Message){}}
}

// Get opens or returns the store for runID. The run directory must already
// exist: the machine manager creates it, and a read must not manufacture a
// run out of a typo.
func (r *Registry) Get(runID string) (*Store, error) {
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
	if r.OnVerdict != nil {
		switch m.Kind {
		case Verdict, Accept, Dispute:
			r.OnVerdict(runID, s.verdictLocked())
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
