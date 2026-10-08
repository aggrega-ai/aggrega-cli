// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package filefailure

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

// TestWrapSaysTheActThePathAndTheReason pins the sentence Wrap builds for each
// shape an error can take: the operating system's path error on the path named,
// on another place along the way, and written in a Windows call's terms; a path
// error some caller already wrapped; and an error that is no path error at all.
// The call's name never reaches the sentence, and the path is said once.
func TestWrapSaysTheActThePathAndTheReason(t *testing.T) {
	t.Parallel()
	refused := errors.New("permission denied")
	for _, row := range []struct {
		name string
		path string
		err  error
		want string
	}{
		{
			name: "the path error names the path the act was on",
			path: "/ws/.aggrega/cache/entities/x/1.0.0",
			err:  &fs.PathError{Op: "mkdir", Path: "/ws/.aggrega/cache/entities/x/1.0.0", Err: refused},
			want: "create the cache directory /ws/.aggrega/cache/entities/x/1.0.0: permission denied",
		},
		{
			name: "the path error names the same place in another spelling",
			path: "/ws/cache/x",
			err:  &fs.PathError{Op: "mkdir", Path: "/ws/cache/x/", Err: refused},
			want: "create the cache directory /ws/cache/x: permission denied",
		},
		{
			name: "the path error names a parent the act had to create",
			path: "/ws/cache/entities/x/1.0.0",
			err:  &fs.PathError{Op: "mkdir", Path: "/ws/cache/entities", Err: refused},
			want: "create the cache directory /ws/cache/entities/x/1.0.0: /ws/cache/entities: permission denied",
		},
		{
			name: "a Windows call's name and its localized reason",
			path: `C:\Users\u\AppData\Roaming\aggrega\config.yaml`,
			err: &fs.PathError{Op: "GetFileAttributesEx", Path: `C:\Users\u\AppData\Roaming\aggrega\config.yaml`,
				Err: errors.New("Impossibile trovare il file specificato.")},
			want: `create the cache directory C:\Users\u\AppData\Roaming\aggrega\config.yaml: Impossibile trovare il file specificato.`,
		},
		{
			name: "a path error a caller already wrapped is said whole",
			path: "/ws/cache/x",
			err:  fmt.Errorf("open the slot: %w", &fs.PathError{Op: "mkdir", Path: "/ws/cache/x", Err: refused}),
			want: "create the cache directory /ws/cache/x: open the slot: mkdir /ws/cache/x: permission denied",
		},
		{
			name: "an error that is no path error is said whole",
			path: "/ws/cache/x",
			err:  errors.New("disk quota exceeded"),
			want: "create the cache directory /ws/cache/x: disk quota exceeded",
		},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			got := Wrap("create the cache directory", row.path, row.err)
			if got == nil {
				t.Fatal("Wrap returned nil for a failure")
			}
			if got.Error() != row.want {
				t.Errorf("Wrap said\n  %s\nwant\n  %s", got.Error(), row.want)
			}
			if !errors.Is(got, row.err) {
				t.Errorf("the failure does not unwrap to the error it says: %v", got)
			}
		})
	}
}

// TestWrapKeepsEverythingTheErrorCarries holds the wrapped failure to the
// matching a caller does on the operating system's error: the reason stays an
// errors.Is match, and the path error stays reachable with errors.As.
func TestWrapKeepsEverythingTheErrorCarries(t *testing.T) {
	t.Parallel()
	pathErr := &fs.PathError{Op: "mkdir", Path: "/ws/cache/x", Err: fs.ErrPermission}
	got := Wrap("create the cache directory", "/ws/cache/x", pathErr)
	if !errors.Is(got, fs.ErrPermission) {
		t.Errorf("the operating system's reason is no longer matchable: %v", got)
	}
	var reached *fs.PathError
	if !errors.As(got, &reached) || reached != pathErr {
		t.Errorf("the path error is no longer reachable: %v", got)
	}
}

// TestWrapOfNoFailureIsNoFailure pins the nil case, so a caller that wraps
// unconditionally cannot turn success into an error.
func TestWrapOfNoFailureIsNoFailure(t *testing.T) {
	t.Parallel()
	if got := Wrap("create the cache directory", "/ws/cache/x", nil); got != nil {
		t.Errorf("Wrap of nil = %v; want nil", got)
	}
}

// TestWrapOverARealFailure drives the operating system itself: creating a
// directory beneath a regular file fails at that file, a place other than the
// directory asked for, so the sentence says both and names no call. The reason
// is read off the error the system returned rather than written here, because
// its words are the platform's.
func TestWrapOverARealFailure(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	inTheWay := filepath.Join(dir, "in-the-way")
	if err := os.WriteFile(inTheWay, []byte("a file\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(inTheWay, "slot")
	raw := os.MkdirAll(target, 0o755)
	if raw == nil {
		t.Fatalf("creating %s beneath a regular file succeeded; the fixture proves nothing", target)
	}
	var pathErr *fs.PathError
	if !errors.As(raw, &pathErr) {
		t.Fatalf("the operating system did not answer with a path error: %v", raw)
	}
	got := Wrap("create the cache directory", target, raw).Error()
	want := "create the cache directory " + target + ": " + pathErr.Path + ": " + pathErr.Err.Error()
	if filepath.Clean(pathErr.Path) == filepath.Clean(target) {
		want = "create the cache directory " + target + ": " + pathErr.Err.Error()
	}
	if got != want {
		t.Errorf("Wrap said\n  %s\nwant\n  %s", got, want)
	}
}
