// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package windowshost answers, once for the whole process, whether the names it
// writes land on a Windows file system.
//
// A name somebody else chose — an archive entry, a draft an importer returned, a
// rendered file's name — can hold a segment Windows cannot hold as written: a
// name it reserves for a device, or one ending in a full stop or a space. Windows
// maps such a segment to a device or does not keep it as written, so a writer on
// Windows refuses the name before anything is written, while a writer anywhere
// else writes it as it is. Which of the two applies is a property of the machine
// the process runs on, and [Running] is the one place it is read.
//
// It is a package of its own so that the answer can be stated for a test in any
// package: [SetForTest] lets a test on Linux show what a writer does when the
// process runs on Windows, and what it does when it does not.
package windowshost

import (
	"runtime"
	"sync/atomic"
)

// windows holds the answer [Running] gives. It is set once from the platform the
// program was built for, and moved only by [SetForTest].
var windows atomic.Bool

func init() { windows.Store(runtime.GOOS == "windows") }

// Running reports whether this process writes the names it is handed onto a
// Windows file system: true where the program was built for Windows, and false
// everywhere else.
func Running() bool { return windows.Load() }

// SetForTest makes [Running] report windows until the returned function restores
// what it reported before.
//
// It exists so a test in another package can show, on any machine, what a writer
// does when the process runs on Windows and when it does not. It is test-only:
// nothing in the product calls it. The answer is the process's, so a test calling
// it does not run in parallel with a test that writes a name beneath a directory.
func SetForTest(windowsHost bool) (restore func()) {
	prev := windows.Swap(windowsHost)
	return func() { windows.Store(prev) }
}
