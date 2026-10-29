// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package guestwire holds the assertions that hold across every provider guest
// rather than within one: that the provider render socket's failure transport
// stays in step with the halting conditions the provider guest cores publish,
// and that no provider guest links the host's plugin loader or the WebAssembly
// runtime beneath it. It declares nothing of its own.
//
// It exists as a package because of where the assertions can be written. Each
// property is about the SOCKET or about every guest at once rather than about any
// one guest core, so no single core is its subject. The guest cores are internal
// to plugins/, which puts any code that reads all of them under plugins/, and
// there is no aggregating package there. That left two alternatives, both worse:
// one core's test importing its siblings, which reads as arbitrary to whoever
// opens it, or one copy of the same scan in each core, which is the
// dis-unification a shared home removes.
package guestwire
