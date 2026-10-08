// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package lintops is the shared publishing-discipline layer that language front
// ends drive to run the linter against explicit inputs. It single-sources the
// three lint operations a front end needs — checking one catalog entity against
// the publishing rules, checking every entity a Release's closure reaches together
// with the rules that read that whole Release, and checking every entity under the
// catalog directories — so every front end runs one implementation rather than its
// own copy.
//
// The layer takes explicit inputs (a resolved workspace, an entity or Release
// path, output writers) and returns typed errors. It holds no session state and
// reads no front-end fields: a caller assembles the inputs from whatever state
// it holds and adapts the results back into its own wire shapes. Output is split
// across two writers — a success confirmation on Out and diagnostic findings on
// DiagOut — so a positive signal and its advisories travel on separate streams.
package lintops
