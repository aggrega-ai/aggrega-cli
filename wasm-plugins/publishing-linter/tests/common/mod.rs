// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//! Shared LinterInput fixture construction for rule test files.
//!
//! Each rule test populates only the fields the rule cares about;
//! defaults for the rest come from this helper. Adding a new
//! LinterInput field requires updating only this file (the default
//! value) plus the test fixtures of the rule that consumes the new
//! field.

#![allow(dead_code)]

use aggrega_pdk_rust::{LinterInput, Policies};
use serde_json::Value;

/// `fixture` builds a `LinterInput` with all fields default-populated.
/// Callers override the fields their specific rule exercises.
pub fn fixture(
    entity_kind: &str,
    proposed: Value,
    previous: Option<Value>,
    policies: Option<Policies>,
    source_path: impl Into<Option<String>>,
) -> LinterInput {
    LinterInput {
        rule_id: String::new(),
        entity_kind: entity_kind.to_string(),
        proposed,
        previous,
        parent: None,
        source_path: source_path.into(),
        policies,
    }
}

/// `fixture_with_parent` is `fixture` plus a populated `parent` slot —
/// the convenience form for narrowing-rule tests.
pub fn fixture_with_parent(
    entity_kind: &str,
    proposed: Value,
    parent: Value,
    policies: Option<Policies>,
) -> LinterInput {
    LinterInput {
        rule_id: String::new(),
        entity_kind: entity_kind.to_string(),
        proposed,
        previous: None,
        parent: Some(parent),
        source_path: None,
        policies,
    }
}
