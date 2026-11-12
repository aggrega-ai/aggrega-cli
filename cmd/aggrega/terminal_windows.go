// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

//go:build windows

package main

import "golang.org/x/sys/windows"

// nativeConsoleControl is the Windows console's mode, read and written through
// the console API. A Windows console interprets escape sequences only once a
// program turns on virtual-terminal processing for it, and an older console
// refuses to.
func nativeConsoleControl() *consoleControl {
	return &consoleControl{
		getMode: func(fd uintptr) (uint32, error) {
			var mode uint32
			err := windows.GetConsoleMode(windows.Handle(fd), &mode)
			return mode, err
		},
		setMode: func(fd uintptr, mode uint32) error {
			return windows.SetConsoleMode(windows.Handle(fd), mode)
		},
		escapes: windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING,
	}
}
