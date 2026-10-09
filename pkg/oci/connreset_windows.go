// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//go:build windows

package oci

import "syscall"

// connectionResetErrors are the values a connection the peer reset is reported
// as on this platform. The socket layer reports a reset as WSAECONNRESET, which
// is a different value from ECONNRESET here, so both are listed.
var connectionResetErrors = []error{syscall.ECONNRESET, syscall.WSAECONNRESET}
