// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

// Package filefailure says a failed file-system operation the way its reader can
// act on it: what the program was doing, with which path, and then the operating
// system's own reason.
//
// The text an operating-system error carries on its own opens with the name of the
// system call that failed — mkdir, open, GetFileAttributesEx — and repeats the path
// the surrounding message has usually named already. The call's name is the
// platform's vocabulary rather than the product's, and on some systems the reason
// after it is written in the user's own language, so on its own the line says
// neither what was being attempted nor why the user is reading about it. Saying the
// act and the path once, then the reason, keeps every fact the reader can use and
// drops the one they cannot.
package filefailure

import (
	"errors"
	"io/fs"
	"path/filepath"
)

// Wrap returns err, the failure of a file-system operation on path, said as what
// the program was doing — doing is a phrase such as "create the cache directory" —
// then path, then the operating system's reason.
//
// Where err is itself the operating system's path error, the name of the call that
// failed is left out, and the location that error names is said only where it is
// not path: an operation on path can fail at another place on the way, such as a
// parent directory it had to create, and that place is where the reader looks. Any
// other error is said whole, since words a caller put around a path error are part
// of what happened.
//
// The returned error unwraps to err, so errors.Is and errors.As reach everything
// err carries — the path error and the operating system's reason included. A nil
// err returns nil.
func Wrap(doing, path string, err error) error {
	if err == nil {
		return nil
	}
	return &failure{doing: doing, path: path, err: err}
}

// failure is a file-system failure said in the reader's terms; see Wrap.
type failure struct {
	doing string
	path  string
	err   error
}

func (f *failure) Error() string {
	return f.doing + " " + f.path + ": " + reason(f.path, f.err)
}

func (f *failure) Unwrap() error { return f.err }

// reason is what the operating system said about err, without the name of the call
// that failed.
//
// Only an err that IS the path error is taken apart. A path error found further
// down a chain is left to the chain's own text, because the frames above it carry
// words the reader would otherwise lose.
func reason(path string, err error) string {
	var pathErr *fs.PathError
	if !errors.As(err, &pathErr) || error(pathErr) != err {
		return err.Error()
	}
	if filepath.Clean(pathErr.Path) == filepath.Clean(path) {
		return pathErr.Err.Error()
	}
	return pathErr.Path + ": " + pathErr.Err.Error()
}
