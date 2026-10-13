// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//! Reading the attachment's settings out of the payload a role delivers.
//!
//! Each role's payload is read through the generated codec of the shape that role
//! declares, so a plugin and everything else on the wire agree about what the
//! payload holds: a list the shape declares always present arrives as a list,
//! empty when there are no entries, because the encoder writes the declared shape
//! rather than leaving the field out. What is left to read here is the one field
//! every role carries for its policy to consume — the attachment's settings —
//! and it is read free of any policy-engine type, so every engine shares one
//! reader rather than each carrying its own opinion about it.

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
}
