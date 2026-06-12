// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//! Per-workspace flag access for the publishing-linter rule set.
//!
//! The PDK ships the [`Policies`](aggrega_pdk_rust::Policies) struct
//! mirroring `workspaceconfig.PublishingDisciplineConfig` in the Go
//! host; this module re-exports it for convenient access from rule
//! modules and provides defaulted-getters for fields rule modules
//! frequently consult.
//!
//! Schema contract: the wire shape is pinned by
//! `aggrega-schemas/json/wasm-linter-v1alpha1.schema.json`
//! at `#/$defs/Policies`. A host-side round-trip test asserts a
//! Go-marshaled `PublishingDisciplineConfig` deserialises into the
//! Rust `Policies` struct without field loss.

pub use aggrega_pdk_rust::Policies;

/// `effective` returns the workspace policies from the linter input
/// with `None` resolved to the default Policies value (every flag
/// `false`, no narrowing exemptions). Rule modules consult this
/// shape rather than re-implementing the None-vs-defaulted dance.
pub fn effective(maybe: Option<&Policies>) -> Policies {
    maybe.cloned().unwrap_or_default()
}
