// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package registryops

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/aggrega-ai/aggrega-cli/internal/registryindex"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
)

// The child role of TestAnIndexWriteEndedPartWayNeverLeavesAPartialIndex: the
// workspace it writes the next index into, and the number of file-system changes
// it makes before the process ends.
const (
	endedIndexWriteWorkspaceEnv = "AGGREGA_TEST_ENDED_INDEX_WRITE_WORKSPACE"
	endedIndexWriteAfterEnv     = "AGGREGA_TEST_ENDED_INDEX_WRITE_AFTER"
	endedIndexWriteExitCode     = 3
)

const (
	endedIndexWriteCacheDir = ".aggrega/cache"
	endedIndexWriteHost     = "registry.example.invalid"
)

var (
	endedIndexWritePreviousTime = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	endedIndexWriteNextTime     = time.Date(2026, 2, 3, 4, 5, 6, 0, time.UTC)
)

// endedIndexWriteRecord is one record of the host the test writes an index for.
func endedIndexWriteRecord(name, version string) registryindex.Record {
	return registryindex.Record{
		URI:     "oci://" + endedIndexWriteHost + "/" + name,
		Name:    name,
		Version: version,
		Kind:    "Component",
	}
}

// endedIndexWritePrevious and endedIndexWriteNext are the index on disk before
// the write and the one the write lays out. They share a repository at different
// versions and each holds one the other does not, so every part of either set,
// and every mix of the two, reads as neither.
func endedIndexWritePrevious() HostIndex {
	return HostIndex{Host: endedIndexWriteHost, Records: []registryindex.Record{
		endedIndexWriteRecord("component/core/api", "1.0.0"),
		endedIndexWriteRecord("component/core/db", "1.0.0"),
		endedIndexWriteRecord("component/core/web", "1.0.0"),
	}}
}

func endedIndexWriteNext() HostIndex {
	return HostIndex{Host: endedIndexWriteHost, Records: []registryindex.Record{
		endedIndexWriteRecord("component/core/api", "2.0.0"),
		endedIndexWriteRecord("component/core/cache", "1.0.0"),
		endedIndexWriteRecord("component/core/db", "1.0.0"),
	}}
}

// TestAnIndexWriteEndedPartWayNeverLeavesAPartialIndex ends a process inside the
// index write after each file-system change it makes in turn — the first, the
// second, and so on until the write finishes — and reads the host's index back
// the way every consumer reads it.
//
// The process is ended rather than handed an error, because an ended process runs
// nothing after the point it stopped: no deferred clean-up and no error path. So
// the write runs in a child process re-executing this test binary, which leaves
// the process from inside the write itself; the parent then reads what is on
// disk.
//
// Whatever change the write stopped after, the index must read as the previous
// record set with its own freshness time or none, as no index, or as the new
// record set with its own freshness time or none — never a part of either set,
// and never a freshness time beside records it was not written for. And the next
// write must leave exactly what a write that was never interrupted leaves: the
// same files with the same bytes, and nothing else.
func TestAnIndexWriteEndedPartWayNeverLeavesAPartialIndex(t *testing.T) {
	if ws := os.Getenv(endedIndexWriteWorkspaceEnv); ws != "" {
		runEndedIndexWrite(t, ws, os.Getenv(endedIndexWriteAfterEnv))
		return
	}

	previous, next := endedIndexWritePrevious(), endedIndexWriteNext()
	previousRecords := completedIndexRecords(t, &previous, endedIndexWritePreviousTime)
	nextRecords := completedIndexRecords(t, &next, endedIndexWriteNextTime)

	rows := []struct {
		name     string
		previous *HostIndex
	}{
		{name: "an earlier index is replaced", previous: &previous},
		{name: "a host's first index"},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			// What a write that was never interrupted leaves, and the proof that it
			// is the host's directory of records and its freshness sibling and
			// nothing else.
			uninterrupted := t.TempDir()
			layOutIndex(t, uninterrupted, row.previous, next)
			want := indexTree(t, uninterrupted)
			if got, layout := keysOf(want), completedIndexLayout(uninterrupted, next); !reflect.DeepEqual(got, layout) {
				t.Fatalf("an uninterrupted write leaves %v; want exactly %v", got, layout)
			}

			ended := 0
			for after := 1; ; after++ {
				ws := t.TempDir()
				if row.previous != nil {
					if err := WriteHostIndex(ws, endedIndexWriteCacheDir, *row.previous, endedIndexWritePreviousTime); err != nil {
						t.Fatalf("lay out the earlier index: %v", err)
					}
				}
				if !endIndexWriteAfter(t, ws, after) {
					break
				}
				ended++

				got, err := registryindex.LoadHost(ws, endedIndexWriteCacheDir, endedIndexWriteHost)
				switch {
				case registryindex.IsNotBuilt(err):
					// No index at all: a reader is told to build one.
				case err != nil:
					t.Errorf("ended after change %d: reading the index failed: %v", after, err)
				case row.previous != nil && recordsJSON(t, got.Records) == previousRecords:
					if got.HasTimestamp && !got.LastUpdated.Equal(endedIndexWritePreviousTime) {
						t.Errorf("ended after change %d: the previous records read under the freshness time %s, written for other records",
							after, got.LastUpdated.Format(time.RFC3339))
					}
				case recordsJSON(t, got.Records) == nextRecords:
					if got.HasTimestamp && !got.LastUpdated.Equal(endedIndexWriteNextTime) {
						t.Errorf("ended after change %d: the new records read under the freshness time %s, written for other records",
							after, got.LastUpdated.Format(time.RFC3339))
					}
				default:
					stamp := "no freshness time"
					if got.HasTimestamp {
						stamp = "the freshness time " + got.LastUpdated.Format(time.RFC3339)
					}
					t.Errorf("ended after change %d: the index reads as a partial set of %d records under %s: %s",
						after, len(got.Records), stamp, recordsJSON(t, got.Records))
				}

				if err := WriteHostIndex(ws, endedIndexWriteCacheDir, next, endedIndexWriteNextTime); err != nil {
					t.Fatalf("ended after change %d: the next write failed: %v", after, err)
				}
				if got := indexTree(t, ws); !reflect.DeepEqual(got, want) {
					t.Errorf("ended after change %d: the next write left\n%v\nwant\n%v", after, got, want)
				}
			}
			// A write ended only once its last change was made, or one never ended
			// at all, would make this test vacuous: it has to have stopped between
			// the record files at least.
			if ended <= len(next.Records) {
				t.Errorf("the write was ended after only %d changes; it writes %d records, so it was never stopped part-way through them",
					ended, len(next.Records))
			}
		})
	}
}

// runEndedIndexWrite is the child role: it writes the next index into ws and
// leaves the process once the write has made the given number of file-system
// changes. A write that finishes first returns, and the child exits zero.
func runEndedIndexWrite(t *testing.T, ws, afterText string) {
	after, err := strconv.Atoi(afterText)
	if err != nil || after < 1 {
		t.Fatalf("the change count %q is not a positive number", afterText)
	}
	made := 0
	changed := func() {
		made++
		if made == after {
			os.Exit(endedIndexWriteExitCode)
		}
	}
	direct := indexFS
	indexFS.removeAll = func(path string) error { err := direct.removeAll(path); changed(); return err }
	indexFS.mkdirAll = func(path string, perm os.FileMode) error { err := direct.mkdirAll(path, perm); changed(); return err }
	indexFS.writeFile = func(path string, data []byte, perm os.FileMode) error {
		err := direct.writeFile(path, data, perm)
		changed()
		return err
	}
	indexFS.remove = func(path string) error { err := direct.remove(path); changed(); return err }
	indexFS.rename = func(oldPath, newPath string) error { err := direct.rename(oldPath, newPath); changed(); return err }
	t.Cleanup(func() { indexFS = direct })

	if err := WriteHostIndex(ws, endedIndexWriteCacheDir, endedIndexWriteNext(), endedIndexWriteNextTime); err != nil {
		t.Fatalf("the write failed rather than being ended: %v", err)
	}
}

// endIndexWriteAfter runs the child role over ws, ending the write after the
// given number of changes. It reports whether the write was ended; false means
// it finished first.
func endIndexWriteAfter(t *testing.T, ws string, after int) bool {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestAnIndexWriteEndedPartWayNeverLeavesAPartialIndex$", "-test.count=1")
	cmd.Env = append(os.Environ(),
		endedIndexWriteWorkspaceEnv+"="+ws,
		endedIndexWriteAfterEnv+"="+strconv.Itoa(after),
	)
	out, err := cmd.CombinedOutput()
	if err == nil {
		return false
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() == endedIndexWriteExitCode {
		return true
	}
	t.Fatalf("the child writing the index failed rather than being ended after change %d: %v\n%s", after, err, out)
	return false
}

// completedIndexRecords is the record set LoadHost returns for idx once a write
// of it has finished, in the form recordsJSON compares.
func completedIndexRecords(t *testing.T, idx *HostIndex, now time.Time) string {
	t.Helper()
	ws := t.TempDir()
	if err := WriteHostIndex(ws, endedIndexWriteCacheDir, *idx, now); err != nil {
		t.Fatalf("write the index: %v", err)
	}
	got, err := registryindex.LoadHost(ws, endedIndexWriteCacheDir, idx.Host)
	if err != nil {
		t.Fatalf("read the index back: %v", err)
	}
	if !got.HasTimestamp || !got.LastUpdated.Equal(now) {
		t.Fatalf("a finished write reads back with freshness time %v (present %v); want %v", got.LastUpdated, got.HasTimestamp, now)
	}
	return recordsJSON(t, got.Records)
}

// layOutIndex writes previous (where there is one) and then next into ws, each to
// completion.
func layOutIndex(t *testing.T, ws string, previous *HostIndex, next HostIndex) {
	t.Helper()
	if previous != nil {
		if err := WriteHostIndex(ws, endedIndexWriteCacheDir, *previous, endedIndexWritePreviousTime); err != nil {
			t.Fatalf("write the earlier index: %v", err)
		}
	}
	if err := WriteHostIndex(ws, endedIndexWriteCacheDir, next, endedIndexWriteNextTime); err != nil {
		t.Fatalf("write the index: %v", err)
	}
}

// completedIndexLayout is every entry a finished write of idx leaves under the
// index root, relative to it, as indexTree names them: the host's directory, one
// record file per repository in it, and the freshness sibling.
func completedIndexLayout(ws string, idx HostIndex) []string {
	indexRoot := oci.IndexRoot(ws, endedIndexWriteCacheDir)
	rel := func(path string) string {
		r, _ := filepath.Rel(indexRoot, path)
		return filepath.ToSlash(r)
	}
	base := oci.IndexBasePath(ws, endedIndexWriteCacheDir, idx.Host)
	out := []string{"./", rel(base) + "/", rel(base + ".updated")}
	for _, rec := range idx.Records {
		out = append(out, rel(oci.IndexRepoPath(ws, endedIndexWriteCacheDir, idx.Host, rec.Name)))
	}
	sort.Strings(out)
	return out
}

// indexTree is every entry under the workspace's index root, relative to it:
// each file with its bytes, and each directory — an empty one included — named
// with a trailing slash.
func indexTree(t *testing.T, ws string) map[string]string {
	t.Helper()
	indexRoot := oci.IndexRoot(ws, endedIndexWriteCacheDir)
	out := map[string]string{}
	err := filepath.WalkDir(indexRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, rerr := filepath.Rel(indexRoot, path)
		if rerr != nil {
			return rerr
		}
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			out[strings.TrimSuffix(rel, "/")+"/"] = ""
			return nil
		}
		data, rerr := os.ReadFile(path)
		if rerr != nil {
			return rerr
		}
		out[rel] = string(data)
		return nil
	})
	if err != nil {
		t.Fatalf("list the index root: %v", err)
	}
	return out
}

// keysOf is the sorted names of an indexTree listing.
func keysOf(tree map[string]string) []string {
	out := make([]string, 0, len(tree))
	for k := range tree {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// recordsJSON is a record set as JSON, so two sets read back from disk compare
// by their content.
func recordsJSON(t *testing.T, records []registryindex.Record) string {
	t.Helper()
	data, err := json.Marshal(records)
	if err != nil {
		t.Fatalf("encode records: %v", err)
	}
	return string(data)
}
