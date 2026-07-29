// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//! Shared parse of the wire `RawValue` input fields into owned
//! `serde_json::Value` trees.
//!
//! The linter input carries `proposed` and `previous` as
//! `Box<RawValue>` — the verbatim JSON text the host serialised, kept
//! unparsed at the ABI boundary (mirroring the Go host's
//! `json.RawMessage`). Rules that inspect entity structure need an
//! owned `Value` tree; this module is the single place that performs
//! that parse so the `serde_json::from_str(raw.get())` call is not
//! scattered across every rule.

use serde_json::value::RawValue;
use serde_json::Value;

/// `parse` turns a wire `RawValue` field into an owned `serde_json::Value`.
///
/// The host always sends valid JSON (it serialised the value before
/// handing it across the ABI boundary), so a parse failure indicates a
/// host/ABI contract breach rather than authored content. Every rule
/// treats a missing or empty structure as "nothing to check" (returning
/// no findings), so a parse failure degrades to `Value::Null` rather than
/// panicking inside a guest call — the rule then sees an absent structure
/// and stays silent, which is the safe, meaning-preserving outcome.
pub fn parse(raw: &RawValue) -> Value {
    serde_json::from_str(raw.get()).unwrap_or(Value::Null)
}

/// `parse_opt` parses an optional wire `RawValue` field, returning `None`
/// when the field is absent. A parse failure degrades to `None` for the
/// same reason `parse` degrades to `Value::Null`: rules treat an absent
/// reference as "nothing to compare against" and stay silent.
pub fn parse_opt(raw: &Option<Box<RawValue>>) -> Option<Value> {
    raw.as_ref()
        .and_then(|r| serde_json::from_str(r.get()).ok())
}
