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
use serde_json::value::to_raw_value;
use serde_json::Value;

/// `fixture` builds a `LinterInput` with all fields default-populated.
/// Callers override the fields their specific rule exercises. The
/// `proposed` / `previous` values arrive as parsed `Value`s and are
/// re-encoded to the wire `RawValue` representation the input carries.
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
        proposed: to_raw_value(&proposed).expect("encode proposed"),
        previous: previous.map(|v| to_raw_value(&v).expect("encode previous")),
        source_path: source_path.into().unwrap_or_default(),
        policies: policies.unwrap_or_default(),
    }
}
