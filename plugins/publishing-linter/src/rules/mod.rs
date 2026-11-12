// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

//! Rule modules registered by the publishing-linter plugin's
//! dispatcher.
//!
//! Each rule module exposes a public `classify(input: &LinterInput) ->
//! Vec<Diagnostic>` function. The dispatcher in `lib.rs` routes
//! the plugin's `lint` entry point to one of these modules based on
//! `LinterInput.rule_id`. Adding a new rule means: author a module
//! here, add a `pub mod` line below, and register the `rule_id` arm
//! in the dispatcher's match.

pub mod annotation_prefix;
pub mod category_vs_directory;
pub mod connection_credential_matches_target_key;
pub mod dynamic_refs;
pub mod id_overrides;
pub mod metrics_credential_annotation_matches_provides;
pub mod name_shape;
pub mod plugin_default_limits;
pub mod provides_declared_by_component;
pub mod schema_library_walker;
pub mod secret_slot_declares_sensitive;
pub mod x_aggrega_discouragement;
