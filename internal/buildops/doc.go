// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package buildops is the shared build-orchestration layer that front ends drive
// to compile a Release into artifacts on disk. It single-sources that operation,
// so every front end runs one implementation rather than its own copy.
//
// The layer composes the filesystem-touching steps around the in-memory
// computation in internal/compilerops: the tolerant catalog load, the lockfile
// step, the external-input gate, the rendered-artifact write and the trace
// write. The render itself, and the gate sequence in front of it, stay in
// compilerops, which every rendering surface is a transport over.
//
// # It presents a render; it does not insist on performing one
//
// A build renders the Release once. Where the lockfile step filled the lockfile,
// that fill has already rendered the Release — finding out which external inputs
// a Release consumes is a render, not a declaration to read — and this layer
// takes the render it performed and puts its artifacts where they belong. Where
// nothing filled, there is no render to take and this layer performs one. Which
// of the two happened is settled by whether a render came back, so no caller
// chooses and nothing can be set to disagree with what actually ran.
//
// Nothing is merged back into the lockfile on either path. Acquisition and the
// lockfile write belong to the pull step, which is why a build is a read of the
// closure rather than an extension of it.
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
