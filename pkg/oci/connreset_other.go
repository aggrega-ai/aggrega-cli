// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//go:build !windows

package oci

import "syscall"

// connectionResetErrors are the values a connection the peer reset is reported
// as on this platform.
var connectionResetErrors = []error{syscall.ECONNRESET}
