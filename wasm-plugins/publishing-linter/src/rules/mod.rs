// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//! Rule modules registered by the publishing-linter plugin's
//! dispatcher.
//!
//! Each rule module exposes a public `classify(input: &LinterInput) ->
//! Vec<LinterFinding>` function. The dispatcher in `lib.rs` routes
//! the plugin's `lint` entry point to one of these modules based on
//! `LinterInput.rule_id`. Adding a new rule means: author a module
//! here, add a `pub mod` line below, and register the `rule_id` arm
//! in the dispatcher's match.

pub mod annotation_prefix;
pub mod name_shape;
