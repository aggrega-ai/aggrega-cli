// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package haltref resolves a halting condition's code to what the compiler
// declares about it and to the published entry that teaches the authoring fix.
//
// # The two halves, and why they come from different places
//
// A code a reader is holding has to be turned into two things, and only one of
// them is inside the binary.
//
// The condition itself — its two codes and the sentence every diagnostic naming
// it opens with — is declared beside the identity in the compiler's own
// registry, so [Resolve] answers from the binary and needs nothing else. That is
// what lets a caller answer offline, with no cache and no network, and it is why
// a reader always gets something back for a code that exists.
//
// The explanation is prose: what the condition means, what causes it and what to
// change. That is written for a person rather than derived from a declaration, so
// it lives on the published halting-conditions page and is served from the
// documentation bundle. [Entries] reads it out of that page's bytes; the caller
// supplies them, because where the page comes from is the caller's concern and
// this package reaches for nothing.
//
// # The correspondence is read, never re-derived
//
// The page carries a generated region pairing each condition's short code with
// the entry that documents it. That region is rendered from the same
// declarations [Resolve] reads, and a drift gate refuses a page whose region has
// stopped matching them — so reading it is reading a checked correspondence
// rather than trusting prose.
//
// This package declares where that region is: [PagePath] and [RegionID] are the
// page and the region's id, and the generator that writes the region reads both
// from here. One declaration, two users — the writer and the reader of a region
// have to agree about which region it is, and a second declaration is what
// drifts. [HeadingLevel] is here for the same reason: both sides have to agree
// about what opens a section, or a region naming an entry would resolve to the
// wrong prose.
package haltref
