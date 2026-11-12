// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

// Package alphanumeric states one character alphabet — ASCII letters and digits
// — and the derivation that folds an arbitrary string into it.
//
// The alphabet is a character class rather than any one target's invention, and
// this package is named for the class rather than for any caller: what a caller
// uses the alphabet FOR is that caller's own statement, made where the caller
// lives. What every caller shares is the alphabet, and sharing it is the point —
// a caller asking whether a character is admitted and a caller deriving a name
// for the same target cannot come to disagree about what the target accepts,
// which is a property that holds only while there is one statement of it.
//
// The fold is one derivation over that alphabet rather than the only thing built
// on it, and it is lossy: it drops what the alphabet refuses, so two inputs
// differing only in the dropped characters reach one name. A caller that needs a
// name to tell two authored inputs apart wants an encoding rather than a fold,
// and the alphabet is what both are stated against.
//
// The fold ran as several hand-written copies before this package existed: some
// as Go, the rest as a template pipeline restated in each catalog entity that
// needed it. They agreed on ordinary input and were never compared, which is the
// shape a divergence hides in — nothing fails while two copies agree, so nothing
// reports the day they stop. One home removes the question.
//
// Its dependencies are the standard library alone. That is deliberate and it is
// load-bearing: a WebAssembly guest imports the alphabet and the host's template
// layer imports the fold, so anything heavier here would have to compile for
// both.
package alphanumeric
