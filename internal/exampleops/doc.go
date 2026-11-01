// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package exampleops is the shared layer every front end drives to hand a reader
// one of the runnable examples the published documentation carries: the list of
// the examples there are, and one example written out into a new directory with
// the commands its page gives for building it.
//
// The examples are read from the documentation bundle — the same signed artifact
// the documentation surfaces read — acquired through the one acquisition every
// such surface shares, under the reading workspace's own signature check. A
// bundle that check does not admit is never read. Which version is read follows
// the reader's own: the bundle published for it, or the newest one published at
// or below it where none was.
//
// Two front ends asked for the same example therefore write the same files and
// report the same next commands, because both call into here rather than reading
// the bundle their own way. The account a reader is shown — of the list, of an
// example written, of an earlier version read — is composed here too, so a
// terminal and an assistant read the same words; a front end answering with
// structured fields carries the same facts as fields. The rules for a request
// naming the capability's fields — which fields go together — are stated here
// once, so every front end taking those fields refuses a mixed request alike.
package exampleops
