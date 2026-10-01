// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//go:build windows

package filelock

import (
	"errors"

	"golang.org/x/sys/windows"
)

// HeldOpen reports whether err is the refusal Windows returns when a file cannot
// be removed, or renamed, because another handle holds it open without allowing
// that.
//
// Every file opened through the standard library on Windows is opened without
// that allowance, so while another process is reading a file nothing can remove
// it. On unix the same removal succeeds and the open handle keeps reading, which
// is why a caller that sees this refusal treats the file as in use and leaves it
// in place rather than reporting a failure.
//
// Only the sharing refusal counts. A file the caller has no permission to remove
// is refused with a different code, and that stays a failure: reading it as "in
// use" would hide a fault behind an explanation that is not true.
func HeldOpen(err error) bool {
	return errors.Is(err, windows.ERROR_SHARING_VIOLATION)
}
