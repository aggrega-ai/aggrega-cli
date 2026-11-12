// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

// Package validateops holds the compute cores for the correctness-checking
// scopes: one entity in isolation, a raw plugin binary against its self-report,
// and the whole catalog. Each core loads and checks the requested scope and
// returns a structured result — the ordered diagnostic lines, the pass/fail
// verdict, and the scope-specific detail (rendered artifacts, advisory notices,
// per-gate errors) — without writing to any output stream. Callers render the
// result: a command-line front-end prints it to stdout and stderr; a
// programmatic front-end reports it however it likes.
//
// The cores receive already-resolved inputs (workspace root, cache directory,
// catalog search directories, memory caps) rather than discovering them, so the
// package stays free of front-end concerns and can be shared by any caller.
package validateops
