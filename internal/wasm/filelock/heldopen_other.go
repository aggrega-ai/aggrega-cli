// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//go:build !windows

package filelock

// HeldOpen reports whether err is a removal or rename refused because another
// handle holds the file open. Off Windows it never is: removing or renaming onto
// a file another process holds open succeeds, the name goes, and the open handle
// keeps reading the bytes it opened. So every error stays what it is, and a
// caller asking this question changes nothing about what it does on these
// platforms.
func HeldOpen(err error) bool {
	return false
}
