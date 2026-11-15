package core

import (
	"log"
	"strings"
	"sync"
	"time"

	"codegen/internal/bus"
)

// reportStoreErrors makes failed history writes visible. Runs keep going when
// the store cannot write (a full or read-only disk), but the run would then be
// missing from History and Metrics without anyone knowing: every failure is
// logged, and a "store.error" event (no run id, so it is not stored itself)
// tells the UI, at most once a minute for the same kind of write.
func (h *Hub) reportStoreErrors() {
	var mu sync.Mutex
	last := map[string]time.Time{}
	h.Store.SetOnWriteError(func(op string, err error) {
		log.Printf("store: could not %s: %v", op, err)
		kind, _, _ := strings.Cut(op, " run ") // "save run X" and "save run Y" are the same kind
		mu.Lock()
		quiet := time.Since(last[kind]) < time.Minute
		if !quiet {
			last[kind] = time.Now()
		}
		mu.Unlock()
		if quiet {
			return
		}
		h.Bus.Publish(bus.Event{Type: "store.error", Summary: "Run history could not be saved (" + op + "): " + err.Error() + ". The run goes on, but it may be missing from History and Metrics. Check free disk space and that the app's data folder is writable."})
	})
}
