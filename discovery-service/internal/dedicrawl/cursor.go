package dedicrawl

import (
	"sync"

	"github.com/remiges-tushar/discovery-service/internal/beckn"
)

// cursorEntry tracks one catalog's last-indexed state and content, so a
// re-crawl can skip re-fetching unchanged entries while still including
// their last-known content in the served snapshot, and so the one-way
// RETIRED lifecycle transition (§10.1/§10.5) can never be reversed by a
// later crawl that simply omits the entry.
//
// entryVersion and contentVersion are deliberately separate counters, not
// one value doing both jobs: a Provider Node bumps entry.EntryVersion on
// EVERY publish call, including ones where nothing in the catalog's
// actual content changed (e.g. a pure isActive toggle), while
// baseline.version and each change file's fromVersion/toVersion use a
// second, content-only counter that only advances on a real content
// change. entryVersion exists purely so applyEntry can cheaply tell "has
// anything happened since I last looked" (the early-exit in applyEntry);
// contentVersion is what actually lines up with entry.Changes for
// buildChangeChain. Mixing the two here was the cause of a real bug: once
// a no-op publish ticked entryVersion without a matching change/baseline
// file, no chain could ever reach it again, permanently stranding the
// cursor on stale content (see crawl.go's resolveCatalog).
type cursorEntry struct {
	entryVersion   int
	contentVersion int
	isActive       bool
	retired        bool
	catalog        beckn.Catalog
}

// cursorState is this Crawler's in-memory, per-catalogId crawl progress
// and content cache — deliberately not persisted (per the plan's scope
// decision): a process restart forces a full re-crawl of every
// subscriber, acceptable until durable storage (M2/Postgres) lands. It is
// long-lived across repeated Refresh ticks (owned by a Crawler, not
// rebuilt per call) so unchanged/retired catalogs are remembered between
// crawls.
type cursorState struct {
	mu      sync.RWMutex
	entries map[string]cursorEntry
}

func newCursorState() *cursorState {
	return &cursorState{entries: make(map[string]cursorEntry)}
}

// get returns the stored entry for catalogID, if any.
func (c *cursorState) get(catalogID string) (cursorEntry, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	e, ok := c.entries[catalogID]
	return e, ok
}

// put stores/updates catalogID's entry. retired latches permanently once
// set true — a later put with retired=false cannot un-retire it (§10.5:
// no delete/un-retire on a missing or ambiguous signal).
func (c *cursorState) put(catalogID string, e cursorEntry) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if prev, ok := c.entries[catalogID]; ok && prev.retired {
		e.retired = true
	}
	c.entries[catalogID] = e
}

// snapshot returns every currently active, non-retired catalog's last
// known content — the set BuildOnDiscover/service.CatalogStore should
// serve.
func (c *cursorState) snapshot() []beckn.Catalog {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]beckn.Catalog, 0, len(c.entries))
	for _, e := range c.entries {
		if e.retired || !e.isActive {
			continue
		}
		out = append(out, e.catalog)
	}
	return out
}
