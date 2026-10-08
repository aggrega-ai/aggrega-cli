// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//go:build !windows

package main

// nativeConsoleControl reports that this platform's terminals interpret escape
// sequences without being asked, so there is no console mode to turn on.
func nativeConsoleControl() *consoleControl { return nil }
