// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package alphanumeric states one character alphabet — ASCII letters and digits
// — and the derivation that folds an arbitrary string into it.
//
// The alphabet is a character class rather than any one target's invention, and
// this package is named for the class rather than for any caller: what a caller
// uses the alphabet FOR is that caller's own statement, made where the caller
// lives. What every caller shares is the fold, and sharing it is the point — the
// same authored string reaches the same folded name whichever surface folded it,
// which is a property that holds only while there is one implementation of it.
//
// The fold ran as several hand-written copies before this package existed: some
// as Go, the rest as a template pipeline restated in each catalog entity that
// needed it. They agreed on ordinary input and were never compared, which is the
// shape a divergence hides in — nothing fails while two copies agree, so nothing
// reports the day they stop. One home removes the question.
//
// Its dependencies are the standard library alone. That is deliberate and it is
// load-bearing: both a WebAssembly guest and the host's template layer import
// it, so anything heavier here would have to compile for both.
package alphanumeric
