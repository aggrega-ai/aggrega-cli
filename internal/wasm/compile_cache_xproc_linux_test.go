// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//go:build linux

package wasm

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/tetratelabs/wazero"
	"golang.org/x/sys/unix"
)

// Environment a compile-cache worker process reads. Absent them, the worker test
// is inert.
const (
	cacheWorkerDirEnv    = "AGGREGA_COMPILE_CACHE_WORKER_DIR"
	cacheWorkerGuestEnv  = "AGGREGA_COMPILE_CACHE_WORKER_GUEST"
	cacheWorkerOutMarker = "AGGREGA_COMPILE_CACHE_WORKER_OUT="
)

// TestCompileCacheWorker is the process the cross-process compile-cache tests
// start. It loads one guest through a Loader that compiles into the directory it
// is given — a Loader of the shape every caching load uses, cross-process lock and
// attribution included — calls describe(), and prints the output as a marker
// line. It is inert unless one of those tests started it.
func TestCompileCacheWorker(t *testing.T) {
	dir := os.Getenv(cacheWorkerDirEnv)
	if dir == "" {
		t.Skip("not a compile-cache worker invocation")
	}
	guest, err := os.ReadFile(os.Getenv(cacheWorkerGuestEnv))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	cache, err := wazero.NewCompilationCacheWithDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cache.Close(ctx) }()
	out, err := describeWith(ctx, &Loader{Cache: cache, compileDir: dir}, buildProfile, PluginSpec{Name: "compile-cache-worker", Bytes: guest})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("%s%s", cacheWorkerOutMarker, base64.StdEncoding.EncodeToString(out))
}

// cacheWorker is one running TestCompileCacheWorker process, started by
// startCacheWorker and ended by wait or kill.
type cacheWorker struct {
	cmd    *exec.Cmd
	output bytes.Buffer
	exited chan struct{}
	err    error
}

// startCacheWorker starts a worker process loading the guest at guestPath from
// the compile directory dir.
func startCacheWorker(t *testing.T, dir, guestPath string) *cacheWorker {
	t.Helper()
	w := &cacheWorker{exited: make(chan struct{})}
	w.cmd = exec.Command(os.Args[0], "-test.run=^TestCompileCacheWorker$", "-test.count=1", "-test.v")
	w.cmd.Env = append(os.Environ(), cacheWorkerDirEnv+"="+dir, cacheWorkerGuestEnv+"="+guestPath)
	w.cmd.Stdout = &w.output
	w.cmd.Stderr = &w.output
	if err := w.cmd.Start(); err != nil {
		t.Fatalf("start a worker process: %v", err)
	}
	go func() {
		w.err = w.cmd.Wait()
		close(w.exited)
	}()
	return w
}

// wait waits for the worker to exit and returns the describe() output it
// printed, or an error carrying everything it wrote.
func (w *cacheWorker) wait() ([]byte, error) {
	<-w.exited
	combined := w.output.String()
	if w.err != nil {
		return nil, fmt.Errorf("worker process %d failed: %v\noutput:\n%s", w.cmd.Process.Pid, w.err, combined)
	}
	for _, line := range strings.Split(combined, "\n") {
		if i := strings.Index(line, cacheWorkerOutMarker); i >= 0 {
			return base64.StdEncoding.DecodeString(strings.TrimSpace(line[i+len(cacheWorkerOutMarker):]))
		}
	}
	return nil, fmt.Errorf("worker process %d printed no output\noutput:\n%s", w.cmd.Process.Pid, combined)
}

// kill ends the worker with SIGKILL, addressed to its process id, and waits for it.
func (w *cacheWorker) kill(t *testing.T) {
	t.Helper()
	if err := w.cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
		t.Fatalf("kill worker process %d: %v", w.cmd.Process.Pid, err)
	}
	<-w.exited
}

// writeGuest writes guest to a file of its own and returns the path, for a worker
// process to read.
func writeGuest(t *testing.T, guest []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "guest.wasm")
	if err := os.WriteFile(path, guest, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// entryDir returns the directory the runtime writes its entries in beneath dir —
// the one subdirectory named for the runtime's version and platform — creating it
// through a throwaway cache if nothing has yet.
func entryDir(t *testing.T, dir string) string {
	t.Helper()
	c, err := wazero.NewCompilationCacheWithDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	_ = c.Close(context.Background())
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var subdirs []string
	for _, e := range entries {
		if e.IsDir() {
			subdirs = append(subdirs, e.Name())
		}
	}
	if len(subdirs) != 1 {
		t.Fatalf("want exactly one runtime subdirectory under %s; found %v", dir, subdirs)
	}
	return filepath.Join(dir, subdirs[0])
}

// stagingFiles returns the names of the staging files in entries — the temporary
// files an entry is written to before it is renamed into place.
func stagingFiles(t *testing.T, entries string) []string {
	t.Helper()
	list, err := os.ReadDir(entries)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range list {
		if strings.HasSuffix(e.Name(), ".tmp") {
			out = append(out, e.Name())
		}
	}
	return out
}

// entryWatch reports, through the kernel's file-event interface, every file created
// in one directory and every file renamed into it. It observes the writes of every
// process, not only this one's.
type entryWatch struct{ fd int }

// entryEvent is one file created in, or renamed into, a watched directory.
type entryEvent struct {
	name    string
	renamed bool
}

func watchEntries(t *testing.T, dir string) *entryWatch {
	t.Helper()
	fd, err := unix.InotifyInit1(unix.IN_CLOEXEC | unix.IN_NONBLOCK)
	if err != nil {
		t.Fatalf("start watching %s: %v", dir, err)
	}
	t.Cleanup(func() { _ = unix.Close(fd) })
	if _, err := unix.InotifyAddWatch(fd, dir, unix.IN_CREATE|unix.IN_MOVED_TO); err != nil {
		t.Fatalf("watch %s: %v", dir, err)
	}
	return &entryWatch{fd: fd}
}

// await waits up to timeout for at least one event and returns every event
// available by then.
func (w *entryWatch) await(t *testing.T, timeout time.Duration) []entryEvent {
	t.Helper()
	fds := []unix.PollFd{{Fd: int32(w.fd), Events: unix.POLLIN}}
	if _, err := unix.Poll(fds, int(timeout/time.Millisecond)); err != nil && !errors.Is(err, unix.EINTR) {
		t.Fatalf("wait for file events: %v", err)
	}
	return w.drain(t)
}

// drain returns every event queued so far, without waiting.
func (w *entryWatch) drain(t *testing.T) []entryEvent {
	t.Helper()
	var out []entryEvent
	buf := make([]byte, 64<<10)
	for {
		n, err := unix.Read(w.fd, buf)
		if errors.Is(err, unix.EAGAIN) {
			return out
		}
		if err != nil {
			t.Fatalf("read file events: %v", err)
		}
		for off := 0; off+unix.SizeofInotifyEvent <= n; {
			mask := binary.NativeEndian.Uint32(buf[off+4:])
			nameLen := int(binary.NativeEndian.Uint32(buf[off+12:]))
			start := off + unix.SizeofInotifyEvent
			name := strings.TrimRight(string(buf[start:start+nameLen]), "\x00")
			out = append(out, entryEvent{name: name, renamed: mask&unix.IN_MOVED_TO != 0})
			off = start + nameLen
		}
	}
}

// tally splits events into the names created and the names renamed into place,
// each sorted.
func tally(events []entryEvent) (created, renamed []string) {
	for _, e := range events {
		if e.renamed {
			renamed = append(renamed, e.name)
		} else {
			created = append(created, e.name)
		}
	}
	sort.Strings(created)
	sort.Strings(renamed)
	return created, renamed
}

// TestEightProcessesLoadingOneGuestColdPublishOneEntry starts eight processes at
// once, each loading one guest the shared on-disk cache does not yet hold. Exactly
// one of them may compile it: the rest must wait on the cross-process lock and
// then read what that one published. So the entry directory must see exactly one
// staging file created and exactly one entry renamed into place, every process
// must load the guest and describe it as a load with no cache does, and a ninth
// process must load the published entry without writing anything. The runtime
// kernel is put on disk first, by a load of another guest, so the target guest's
// entry is the only one the processes can write.
func TestEightProcessesLoadingOneGuestColdPublishOneEntry(t *testing.T) {
	if testing.Short() {
		t.Skip("cross-process compile-cache test skipped in -short")
	}
	ctx := context.Background()
	target := isolatedGuest(t, "policy-go.wasm", t.Name()+"/target")
	other := isolatedGuest(t, "policy-go.wasm", t.Name()+"/other")
	want := uncachedDescribe(t, buildProfile, PluginSpec{Name: "target", Bytes: target})
	targetPath := writeGuest(t, target)

	dir := compileDirIn(t)
	if _, err := describeWith(ctx, &Loader{Cache: diskCache(t, dir), compileDir: dir}, buildProfile, PluginSpec{Name: "other", Bytes: other}); err != nil {
		t.Fatalf("put the runtime kernel on disk: %v", err)
	}
	entries := entryDir(t, dir)
	before := leafFiles(t, dir)
	watch := watchEntries(t, entries)

	const processes = 8
	workers := make([]*cacheWorker, processes)
	for i := range workers {
		workers[i] = startCacheWorker(t, dir, targetPath)
	}
	for i, w := range workers {
		out, err := w.wait()
		if err != nil {
			t.Errorf("process %d: %v", i, err)
			continue
		}
		if !bytes.Equal(out, want) {
			t.Errorf("process %d described the guest differently from a load with no cache\n got: %s\nwant: %s", i, out, want)
		}
	}
	if t.Failed() {
		return
	}

	created, renamed := tally(watch.drain(t))
	if len(renamed) != 1 || len(created) != 1 || !strings.HasPrefix(created[0], renamed[0]+".") || !strings.HasSuffix(created[0], ".tmp") {
		t.Fatalf("want one compile publishing one entry by rename; the entry directory saw files created %v and renamed into place %v",
			created, renamed)
	}
	published := filepath.Base(entries) + "/" + renamed[0]
	if changes := leafChanges(before, leafFiles(t, dir)); changes != "added "+published {
		t.Fatalf("want the cache to gain exactly the one published entry %s; it changed: %s", published, changes)
	}
	if left := stagingFiles(t, entries); len(left) != 0 {
		t.Fatalf("staging files left behind: %v", left)
	}

	reader := startCacheWorker(t, dir, targetPath)
	out, err := reader.wait()
	if err != nil {
		t.Fatalf("a new process could not load the published entry: %v", err)
	}
	if !bytes.Equal(out, want) {
		t.Fatalf("a new process described the guest differently from a load with no cache\n got: %s\nwant: %s", out, want)
	}
	if events := watch.drain(t); len(events) != 0 {
		t.Fatalf("a process loading the published entry wrote to the cache: %+v", events)
	}
}

// TestAWriterKilledMidPublishLeavesNothingAReaderAccepts kills a process while it
// publishes a compiled entry — the moment it creates a file in the entry directory,
// which is its staging file — and then starts a reader. The writer must have
// published nothing, and the reader must load the guest and describe it as a load
// with no cache does, compiling and publishing the entry itself. A writer that got
// its entry renamed into place before the kill landed proves nothing, so that
// attempt is repeated with a fresh cache. The reader is started after every kill,
// whatever it landed on, so a partial entry left under the entry's own name fails
// the test on that attempt.
func TestAWriterKilledMidPublishLeavesNothingAReaderAccepts(t *testing.T) {
	if testing.Short() {
		t.Skip("cross-process compile-cache test skipped in -short")
	}
	ctx := context.Background()
	target := isolatedGuest(t, "policy-go.wasm", t.Name()+"/target")
	other := isolatedGuest(t, "policy-go.wasm", t.Name()+"/other")
	want := uncachedDescribe(t, buildProfile, PluginSpec{Name: "target", Bytes: target})
	targetPath := writeGuest(t, target)

	const attempts = 10
	for attempt := 1; attempt <= attempts; attempt++ {
		dir := compileDirIn(t)
		if _, err := describeWith(ctx, &Loader{Cache: diskCache(t, dir), compileDir: dir}, buildProfile, PluginSpec{Name: "other", Bytes: other}); err != nil {
			t.Fatalf("put the runtime kernel on disk: %v", err)
		}
		entries := entryDir(t, dir)
		before := leafFiles(t, dir)
		watch := watchEntries(t, entries)

		writer := startCacheWorker(t, dir, targetPath)
		for killed := false; !killed; {
			select {
			case <-writer.exited:
				_, err := writer.wait()
				t.Fatalf("attempt %d: the writer exited before it wrote anything: %v", attempt, err)
			default:
			}
			if len(watch.await(t, 10*time.Millisecond)) > 0 {
				writer.kill(t)
				killed = true
			}
		}

		staged := stagingFiles(t, entries)
		publishedByWriter := leafChanges(before, leafFiles(t, dir))

		reader := startCacheWorker(t, dir, targetPath)
		out, err := reader.wait()
		if err != nil {
			t.Fatalf("attempt %d: after a writer was killed while publishing, a reader could not load the guest: %v", attempt, err)
		}
		if !bytes.Equal(out, want) {
			t.Fatalf("attempt %d: after a writer was killed while publishing, a reader described the guest differently from a load with no cache\n got: %s\nwant: %s",
				attempt, out, want)
		}

		if len(staged) > 0 && publishedByWriter == "" {
			changes := leafChanges(before, leafFiles(t, dir))
			if !strings.HasPrefix(changes, "added ") || strings.Contains(changes, ";") {
				t.Fatalf("the reader should have published exactly one entry of its own; the cache changed: %s", changes)
			}
			return
		}
		t.Logf("attempt %d: the writer had published its entry before the kill landed (staging files %v, changes %q); trying again",
			attempt, staged, publishedByWriter)
	}
	t.Fatalf("in %d attempts the writer was never killed between creating its staging file and renaming it into place", attempts)
}
