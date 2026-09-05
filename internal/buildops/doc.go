// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package buildops is the shared build-orchestration layer that front ends drive
// to compile a Release into artifacts on disk. It single-sources the two
// operations a front end needs — building a Release, and pinning the external
// inputs that Release's render consumes — so every front end runs one
// implementation rather than its own copy.
//
// The layer composes the filesystem-touching steps around the in-memory
// computation in internal/compilerops: the tolerant catalog load, the lockfile
// step, the external-input gate, the rendered-artifact write, the trace write,
// and the merge of newly-pinned external inputs back into the lockfile. The
// render itself, and the gate sequence in front of it, stay in compilerops,
// which every rendering surface is a transport over.
//
// It takes explicit inputs (a resolved workspace, a Release path, output
// writers) and returns typed errors. It holds no session state and reads no
// front-end fields: a caller assembles the inputs from whatever state it holds
// and adapts the result back into its own wire shapes.
//
// It holds no terminal. A caller's progress surface, advisory voice and failure
// annotation arrive as optional callbacks on Reporter and on Config, so a
// surface with no terminal supplies none of them and the layer stays silent
// rather than writing to a stream nobody reads. The dependency fill the
// automatic-pull step performs arrives the same way, through Config.Pull: filling
// the lockfile is the pull operation's own job, and the surface that owns the
// pull supplies it.
//
// It holds no provider-specific or cloud-specific logic. It orchestrates and it
// writes files; the domain intelligence stays in the plugin guests.
package buildops
