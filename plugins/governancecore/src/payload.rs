// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//! Reading the payload a role delivers into its own shape.
//!
//! Each role delivers a payload with a declared shape, and reading it through
//! that shape rather than picking fields out by hand is what keeps a plugin and
//! everything else on the wire agreeing about what the payload holds.
//!
//! Two things are read here: the payload itself, through the shape its role
//! declares, and the attachment's settings field out of that payload. Both are
//! free of any policy-engine type, so every engine shares one reader rather than
//! each carrying its own opinion about what the wire may contain.
//!
//! # Tolerating an absent value where the payload declares a list
//!
//! A payload's shape declares certain list fields as always present, so an empty
//! one is declared to arrive as an empty list. Some senders instead write the
//! absent value when they have no entries, which does not match that shape.
//!
//! This module accepts the absent value for those fields anyway, and the reason is
//! that refusing it would fail an ordinary build over a field the policy never
//! reads — a build with no routing paths to enumerate, or a Manifest that
//! registers no annotation keys, are both perfectly normal, and neither is the
//! author's mistake to fix.
//!
//! **This tolerance is a compensation, not a property of the payload's shape.** It
//! belongs here only while a sender can still write the absent value; once every
//! sender writes an empty list, reading these fields becomes ordinary and this
//! accommodation should go rather than being left as a second opinion about what
//! the payload may contain.
//!
//! Everything else about the payload is read exactly as declared — a field of the
//! wrong kind, or a missing field that is not one of these named lists, is still
//! refused. The accommodation is narrowed to named fields rather than applied to
//! the payload wholesale, so it cannot quietly absorb a shape mismatch somewhere
//! else.

use serde::de::DeserializeOwned;

/// Reads a payload into `T`, treating each named list field as empty when it
/// arrives as the absent value.
///
/// `empty_when_absent` names the payload's own always-present list fields. Naming
/// them explicitly, rather than walking the payload for absent values, keeps the
/// accommodation to exactly the fields a sender is known to write as the absent
/// value, so no other shape mismatch is absorbed with them.
pub fn read<T: DeserializeOwned>(
    payload: &serde_json::Value,
    empty_when_absent: &[&str],
) -> anyhow::Result<T> {
    let mut normalised = payload.clone();
    if let Some(object) = normalised.as_object_mut() {
        for field in empty_when_absent {
            match object.get(*field) {
                Some(serde_json::Value::Null) | None => {
                    object.insert((*field).to_string(), serde_json::Value::Array(Vec::new()));
                }
                Some(_) => {}
            }
        }
    }
    Ok(serde_json::from_value(normalised)?)
}

/// Reads an attachment's settings as a JSON value for a policy to consume,
/// yielding an empty object when the attachment declares none.
///
/// The settings are carried through exactly as the attachment wrote them.
/// Anything a guest reads out of the settings for its own use — a translation,
/// say — is read out of this same object rather than removed from it, so a policy
/// that reads its own settings sees precisely what the attachment declared.
///
/// Settings that are present but unreadable as JSON yield the empty object
/// rather than failing. The case cannot actually arise — a raw value is
/// constructed only from text that parses, which the tests below assert — so this
/// is a total function rather than a tolerance: it lets a caller read settings
/// without handling an error that has no way to occur. Failing instead would put
/// an unreachable error path in front of every caller.
pub fn settings_value(settings: Option<&serde_json::value::RawValue>) -> serde_json::Value {
    match settings {
        Some(raw) => serde_json::from_str(raw.get()).unwrap_or_else(|_| serde_json::json!({})),
        None => serde_json::json!({}),
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use serde::Deserialize;

    #[derive(Debug, Deserialize, PartialEq, Eq)]
    struct Shape {
        entries: Vec<String>,
        name: String,
    }

    #[test]
    fn a_list_that_arrived_as_a_list_is_read_as_written() {
        let parsed: Shape = read(
            &serde_json::json!({"entries": ["a"], "name": "n"}),
            &["entries"],
        )
        .expect("the payload is read");
        assert_eq!(parsed.entries, vec!["a".to_string()]);
    }

    #[test]
    fn a_named_list_that_arrived_absent_is_read_as_empty() {
        let parsed: Shape = read(
            &serde_json::json!({"entries": serde_json::Value::Null, "name": "n"}),
            &["entries"],
        )
        .expect("the payload is read");
        assert!(parsed.entries.is_empty());
    }

    #[test]
    fn a_named_list_that_is_missing_entirely_is_read_as_empty() {
        let parsed: Shape =
            read(&serde_json::json!({"name": "n"}), &["entries"]).expect("the payload is read");
        assert!(parsed.entries.is_empty());
    }

    #[test]
    fn an_unnamed_field_is_still_read_exactly_as_declared() {
        // The narrowing covers the named lists only, so an absent value
        // elsewhere is still refused rather than quietly absorbed.
        let outcome: anyhow::Result<Shape> = read(
            &serde_json::json!({"entries": [], "name": serde_json::Value::Null}),
            &["entries"],
        );
        assert!(outcome.is_err(), "{outcome:?}");
    }

    #[test]
    fn a_named_list_carrying_the_wrong_kind_of_value_is_still_refused() {
        let outcome: anyhow::Result<Shape> = read(
            &serde_json::json!({"entries": "not a list", "name": "n"}),
            &["entries"],
        );
        assert!(outcome.is_err(), "{outcome:?}");
    }

    #[test]
    fn absent_settings_read_as_an_empty_object() {
        assert_eq!(settings_value(None), serde_json::json!({}));
    }

    #[test]
    fn settings_are_read_exactly_as_the_attachment_wrote_them() {
        let raw = serde_json::value::RawValue::from_string(r#"{"max_nodes":5}"#.to_string())
            .expect("test JSON is valid");
        assert_eq!(
            settings_value(Some(&raw)),
            serde_json::json!({"max_nodes": 5})
        );
    }

    #[test]
    fn a_settings_value_cannot_carry_text_that_is_not_readable_json() {
        // Why settings_value's fallback is unreachable rather than merely
        // unexercised: a raw value is constructed only from text that parses, so
        // no caller can hand it unreadable content. Asserted here because it is
        // the reason the fallback yields an empty object instead of failing — if
        // this ever stops holding, the fallback becomes reachable and the choice
        // needs revisiting.
        assert!(serde_json::value::RawValue::from_string("{".to_string()).is_err());
    }

    #[test]
    fn a_payload_that_is_not_an_object_is_read_unchanged() {
        let outcome: anyhow::Result<Shape> = read(&serde_json::json!([1, 2]), &["entries"]);
        assert!(outcome.is_err(), "{outcome:?}");
    }
}
