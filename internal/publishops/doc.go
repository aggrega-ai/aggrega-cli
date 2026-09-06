// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package publishops is the shared publication layer that front ends drive to
// publish a catalog entity, publish a standalone data artifact, sign an
// already-published artifact, and attach a deprecation marker. It single-sources
// those operations so every front end runs one implementation rather than its own
// copy.
//
// The four belong together because the credential boundary is one boundary. They
// share a destination, a signing path, and the identity resolution that path
// needs; settling that resolution once, here, is what stops it coming to mean
// four slightly different things.
//
// # It runs only when it is asked, on every surface
//
// Publishing reaches registries and writes: it lands artifact bytes other people
// resolve, attaches signatures, and records withdrawals. Every one of those is
// intended, and none of them may happen because somebody was typing.
//
// The rule is a property of the invocation path rather than of any one caller: a
// registry-reaching or state-writing operation runs on EXPLICIT INVOCATION and
// never as a side effect of validating, inspecting or answering a question about a
// workspace. It is stated here once, over the whole layer, rather than per
// operation, because an enumeration of the operations it covers is what goes
// stale. What makes a write acceptable is that somebody asked for it, not which
// surface asked.
//
// These operations reach further than most: what they publish changes what every
// consumer of that registry resolves, and a signature cannot be recalled from
// anyone who already fetched it. That is a reason to be explicit about invocation,
// not a reason to withhold the capability from a caller that asked.
//
// # It holds no terminal, and never prompts
//
// Identity is resolved from a supplied or ambient credential source. Where a
// terminal would have opened a browser or asked for a password, the operation
// refuses loudly and names what to supply — it never blocks waiting for an answer
// that will never come, because a hang is the observable failure that posture
// exists to prevent.
//
// The one rung that needs a person is supplied by the caller and defaults to
// absent: SignerRequest.AcquireIdentityToken is nil for every caller with nobody
// watching, and the shared identity ladder then produces its own refusal.
// Supplying it is a caller's declaration that a person is watching and can answer.
// The interactive acquisition itself lives in the command line, not here.
//
// The same holds for the one fork the deprecation content cannot settle from its
// inputs: DeprecateConfig.Prompt is nil for an unattended caller and the missing
// content HALTS, naming what to supply.
//
// # Its diagnostics carry no command name
//
// A caller invoking this layer may never have typed a command, so the text of a
// failure names what went wrong and not the verb a person would have run. Each
// surface adds its own name at its own boundary, which is what keeps one wording
// correct on all of them.
package publishops
