// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package plugincondition declares the generic conditions a plugin reports a
// failure under: the kinds of failure a plugin names when it stops a build, which
// the compiler declares so that every code a reader meets resolves.
//
// A provider guest core raises these conditions from inside its own module, and
// the host's plugin socket presents the one a guest named over its own taxonomy.
// Both sides therefore link this package, and that is why it holds the
// declarations and nothing else: it imports no WebAssembly runtime and nothing
// that reaches one, so linking it adds no host machinery to a guest.
package plugincondition
