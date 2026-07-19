// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package plugintype is the canonical home for the Wasm plugin-type
// vocabulary and the per-type metadata every other package reads. It is a
// leaf package: it imports nothing from within this module, so it can sit
// below the host loader, the workspace-config surface, and the CLI without
// an import cycle, and each of those reads one table rather than re-spelling
// the type names, minimum-export rules, socket names, tiers, mechanisms, and
// load profiles for itself.
//
// The vocabulary is the set of recognized plugin types (provider, linter,
// feature-recipe-pipeline step, mutator, policy, CLI, parser) plus a reserved
// entry for a not-yet-realized type. Each entry records the type's canonical
// string, its minimum-export rule (an exact export name or a name prefix), the
// workspace socket it attaches at when it is a socket type, its reproducibility
// tier, its attachment mechanism, and the host-function load profile it loads
// under. Entries enumerate in a fixed author-defined order so any consumer that
// iterates the registry emits deterministic output.
package plugintype
