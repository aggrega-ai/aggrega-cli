// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package registryops

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// progressLog records the walk's progress events and its commentary in one
// sequence, in the order they reach it, so a test can say which came first.
type progressLog struct {
	mu      sync.Mutex
	entries []string
}

func (l *progressLog) add(entry string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.entries = append(l.entries, entry)
}

// Write records each piece of commentary the walk writes as one entry.
func (l *progressLog) Write(p []byte) (int, error) {
	if len(p) > 0 {
		l.add("note " + string(p))
	}
	return len(p), nil
}

func (l *progressLog) snapshot() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.Clone(l.entries)
}

// TestTheWalkReportsItsProgressInRepositoryOrder drives a parallel walk whose
// first repository is the slowest to read and the one carrying commentary — a
// deprecation marker that is present and not honored — so every other read
// finishes before it. It holds the walk's progress events to repository order:
// the registries, the registry being walked, how many repositories it lists, and
// then one count per repository reported, rising by one each time. The first
// count follows the first repository's commentary: a count taken as reads
// finish, rather than as repositories are reported, would run ahead of it.
func TestTheWalkReportsItsProgressInRepositoryOrder(t *testing.T) {
	t.Parallel()
	_, pub := keyedSigner(t)
	reg := newValidatingRegistry(t)
	const plain = 8
	slow := reg.repository(t, "component/a/slow")
	publish(t, slow, "1.0.0", "slow")
	attachUnsignedMarker(t, slow, "1.0.0")
	for i := range plain {
		publish(t, reg.repository(t, fmt.Sprintf("component/b/plain-%02d", i)), "1.0.0", fmt.Sprintf("plain %d", i))
	}
	reg.mu.Lock()
	reg.slowTags["component/a/slow"] = 150 * time.Millisecond
	reg.mu.Unlock()

	log := &progressLog{}
	rw := refreshWorkspace(t, t.TempDir(), filepath.Join(t.TempDir(), "config.yaml"), reg.host, pub)
	if _, err := Update(t.Context(), rw, UpdateOptions{
		workers: indexWalkWorkers,
		Now:     func() time.Time { return time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC) },
		Progress: UpdateProgress{
			Registries: func(hosts []string) { log.add(fmt.Sprintf("registries %v", hosts)) },
			Registry:   func(i int) { log.add(fmt.Sprintf("registry %d", i)) },
			Listed:     func(total int) { log.add(fmt.Sprintf("listed %d", total)) },
			Walked:     func(done, total int) { log.add(fmt.Sprintf("walked %d/%d", done, total)) },
		},
	}, log); err != nil {
		t.Fatalf("Update: %v\n%q", err, log.snapshot())
	}
	entries := log.snapshot()

	total := plain + 1
	want := []string{fmt.Sprintf("registries [%s]", reg.host), "registry 0", fmt.Sprintf("listed %d", total)}
	for done := 1; done <= total; done++ {
		want = append(want, fmt.Sprintf("walked %d/%d", done, total))
	}
	var events []string
	for _, e := range entries {
		if !strings.HasPrefix(e, "note ") {
			events = append(events, e)
		}
	}
	if !slices.Equal(events, want) {
		t.Errorf("the walk's progress events differ:\n got  %q\n want %q", events, want)
	}

	slowNote := slices.IndexFunc(entries, func(e string) bool {
		return strings.HasPrefix(e, "note ") && strings.Contains(e, "component/a/slow:1.0.0: marker present but not honored")
	})
	firstCount := slices.Index(entries, fmt.Sprintf("walked 1/%d", total))
	if slowNote < 0 {
		t.Fatalf("the slow repository wrote no commentary, so the order below holds nothing; got %q", entries)
	}
	if firstCount < slowNote {
		t.Errorf("the first count was reported before the first repository's commentary; got %q", entries)
	}
}
