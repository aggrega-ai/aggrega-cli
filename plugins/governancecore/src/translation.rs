// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//! Translation from a policy's own decision keys to the verdict severities the
//! governance socket reports from.
//!
//! A policy names its decisions in its own vocabulary — conventionally
//! collecting its messages under `deny`, `warn` and `audit` — while the socket
//! reports from one closed set of severities. This module is where one becomes
//! the other, and it is deliberately free of any policy-engine type so every
//! engine adapter shares one translation rather than restating it.
//!
//! The translation is data an operator supplies through the attachment's
//! settings, not a rule compiled into an adapter. It defaults to the convention
//! above, so a policy written for that convention needs no settings at all.
//!
//! Determinism. A translation is an ordered list of rules and the emitted
//! diagnostics follow it: rule order first, then the order the engine yields a
//! rule's messages. Nothing here iterates a hash map, so identical input yields
//! an identical diagnostic sequence.

use aggrega_pdk_rust::{Diagnostic, SEVERITY_HALT, SEVERITY_INFO, SEVERITY_WARN};
use serde::Deserialize;
use serde_json::value::RawValue;

/// The settings field a translation is authored under.
pub const SETTINGS_FIELD: &str = "translation";

/// One translation rule: a decision key the policy author declares, and the
/// severity each message collected under that key is reported at.
#[derive(Debug, Clone, Deserialize, PartialEq, Eq)]
pub struct Rule {
    /// The decision key as the policy author spells it.
    pub key: String,
    /// The severity the key's messages are reported at, from the closed set the
    /// socket shares.
    pub severity: String,
}

/// An ordered list of translation rules.
///
/// The order is the emission order, so it is part of the translation rather than
/// an implementation detail: a caller reads [`Translation::rules`] and walks it
/// in sequence.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Translation {
    rules: Vec<Rule>,
}

impl Default for Translation {
    fn default() -> Self {
        Self::conventional()
    }
}

impl Translation {
    /// The translation applied when an attachment supplies none: the
    /// conventional decision-key names, mapped onto the socket's
    /// severities — `deny` stops the build, `warn` surfaces alongside a
    /// build that still completes, and `audit` records the observation.
    ///
    /// These names are the convention independently of which policy language
    /// expresses them, which is why the default lives here rather than in an
    /// engine adapter: a policy written for that convention evaluates identically
    /// in either engine, and whether or not the operator configures a
    /// translation.
    pub fn conventional() -> Self {
        Self {
            rules: vec![
                Rule {
                    key: "deny".to_string(),
                    severity: SEVERITY_HALT.to_string(),
                },
                Rule {
                    key: "warn".to_string(),
                    severity: SEVERITY_WARN.to_string(),
                },
                Rule {
                    key: "audit".to_string(),
                    severity: SEVERITY_INFO.to_string(),
                },
            ],
        }
    }

    /// Builds a translation from an explicit rule list, rejecting a rule whose
    /// severity is outside the socket's closed set and a key declared twice.
    ///
    /// Both are rejected rather than resolved: an unknown severity would be
    /// reported as a value no reader can interpret, and a duplicate key would
    /// report every message under it more than once, so silently taking the
    /// first or the last would report a verdict count the policy never
    /// expressed.
    pub fn new(rules: Vec<Rule>) -> anyhow::Result<Self> {
        for (index, rule) in rules.iter().enumerate() {
            if !is_known_severity(&rule.severity) {
                anyhow::bail!(
                    "translation rule for decision key {:?} names severity {:?}, which is not one of {:?}, {:?} or {:?}",
                    rule.key,
                    rule.severity,
                    SEVERITY_HALT,
                    SEVERITY_WARN,
                    SEVERITY_INFO
                );
            }
            if rules[..index].iter().any(|earlier| earlier.key == rule.key) {
                anyhow::bail!(
                    "translation declares decision key {:?} more than once; each key is translated by exactly one rule",
                    rule.key
                );
            }
        }
        Ok(Self { rules })
    }

    /// Reads the translation out of an attachment's settings, falling back to
    /// [`Translation::conventional`] when the settings carry no translation
    /// field.
    ///
    /// The rest of the settings object is left alone: it is the policy's own
    /// data, and this reads one field out of it rather than claiming the whole
    /// object.
    pub fn from_settings(settings: Option<&RawValue>) -> anyhow::Result<Self> {
        let Some(raw) = settings else {
            return Ok(Self::conventional());
        };
        let object: serde_json::Value = serde_json::from_str(raw.get())?;
        let Some(field) = object.get(SETTINGS_FIELD) else {
            return Ok(Self::conventional());
        };
        let rules: Vec<Rule> = serde_json::from_value(field.clone()).map_err(|err| {
            anyhow::anyhow!(
                "the {SETTINGS_FIELD} setting is not a list of decision-key rules: {err}"
            )
        })?;
        if rules.is_empty() {
            anyhow::bail!(
                "the {SETTINGS_FIELD} setting is empty; a translation declares at least one decision key, or is omitted to accept the default"
            );
        }
        Self::new(rules)
    }

    /// The translation's rules, in emission order.
    pub fn rules(&self) -> &[Rule] {
        &self.rules
    }
}

/// Reports whether a severity is one the socket's closed set admits.
fn is_known_severity(severity: &str) -> bool {
    matches!(severity, SEVERITY_HALT | SEVERITY_WARN | SEVERITY_INFO)
}

/// The JSON Schema fragment that types the translation setting, built from the
/// same severity constants [`Translation::new`] validates against so the shape
/// checked ahead of the policy running and the shape this module accepts have one
/// definition.
///
/// The schema constrains the translation field only and leaves the rest of the
/// settings object open, because the remaining fields are the policy's own data
/// and nothing outside the policy declares a shape for them.
///
/// It admits an attachment that carries **no** settings at all, as well as one
/// that carries a settings object. That is not cosmetic: settings are optional
/// for this plugin — a policy written to the conventional collection names needs
/// none — and a settings value is checked against this schema whether or not the
/// attachment declares one, so a schema admitting only an object would refuse
/// every attachment that configured nothing.
pub fn settings_schema() -> String {
    format!(
        r#"{{"type":["object","null"],"properties":{{"{field}":{{"type":"array","minItems":1,"items":{{"type":"object","properties":{{"key":{{"type":"string","minLength":1}},"severity":{{"type":"string","enum":["{halt}","{warn}","{info}"]}}}},"required":["key","severity"],"additionalProperties":false}}}}}}}}"#,
        field = SETTINGS_FIELD,
        halt = SEVERITY_HALT,
        warn = SEVERITY_WARN,
        info = SEVERITY_INFO,
    )
}

/// Appends one diagnostic per message each translation rule collects.
///
/// `messages_for` resolves a decision key to the messages the policy collected
/// under it, yielding an empty list for a key the policy does not declare — an
/// undeclared key is an absent verdict, not a failure. The closure is the only
/// engine-specific part of translation, which is what lets a second engine reuse
/// this module unchanged.
///
/// Emission order is rule order, then the order `messages_for` yields.
pub fn translate_into<F>(
    translation: &Translation,
    mut messages_for: F,
    out: &mut Vec<Diagnostic>,
) -> anyhow::Result<()>
where
    F: FnMut(&str) -> anyhow::Result<Vec<String>>,
{
    for rule in translation.rules() {
        for message in messages_for(&rule.key)? {
            out.push(Diagnostic {
                severity: rule.severity.clone(),
                message,
                ..Default::default()
            });
        }
    }
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;

    fn raw(json: &str) -> Box<RawValue> {
        RawValue::from_string(json.to_string()).expect("test JSON is valid")
    }

    #[test]
    fn absent_settings_yield_the_conventional_translation() {
        let translation = Translation::from_settings(None).expect("absent settings are accepted");
        assert_eq!(translation, Translation::conventional());
    }

    #[test]
    fn settings_without_a_translation_field_yield_the_conventional_translation() {
        let settings = raw(r#"{"max_nodes":5}"#);
        let translation =
            Translation::from_settings(Some(&settings)).expect("policy data alone is accepted");
        assert_eq!(translation, Translation::conventional());
    }

    #[test]
    fn the_conventional_translation_is_the_decision_key_convention() {
        let rules = Translation::conventional();
        let pairs: Vec<(&str, &str)> = rules
            .rules()
            .iter()
            .map(|r| (r.key.as_str(), r.severity.as_str()))
            .collect();
        assert_eq!(
            pairs,
            vec![
                ("deny", SEVERITY_HALT),
                ("warn", SEVERITY_WARN),
                ("audit", SEVERITY_INFO),
            ]
        );
    }

    #[test]
    fn an_override_replaces_the_default_and_keeps_its_declared_order() {
        let settings = raw(
            r#"{"translation":[{"key":"blocked","severity":"halt"},{"key":"noted","severity":"info"}]}"#,
        );
        let translation = Translation::from_settings(Some(&settings))
            .expect("a well-formed override is accepted");
        let pairs: Vec<(&str, &str)> = translation
            .rules()
            .iter()
            .map(|r| (r.key.as_str(), r.severity.as_str()))
            .collect();
        assert_eq!(pairs, vec![("blocked", "halt"), ("noted", "info")]);
    }

    #[test]
    fn an_unknown_severity_is_refused() {
        let settings = raw(r#"{"translation":[{"key":"blocked","severity":"deny"}]}"#);
        let err = Translation::from_settings(Some(&settings))
            .expect_err("a severity outside the closed set is refused");
        assert!(err.to_string().contains("blocked"), "{err}");
    }

    #[test]
    fn a_duplicate_decision_key_is_refused() {
        let settings = raw(
            r#"{"translation":[{"key":"blocked","severity":"halt"},{"key":"blocked","severity":"warn"}]}"#,
        );
        let err = Translation::from_settings(Some(&settings))
            .expect_err("a key declared twice is refused");
        assert!(err.to_string().contains("more than once"), "{err}");
    }

    #[test]
    fn an_empty_translation_is_refused() {
        let settings = raw(r#"{"translation":[]}"#);
        let err = Translation::from_settings(Some(&settings))
            .expect_err("an empty translation is refused");
        assert!(err.to_string().contains("at least one"), "{err}");
    }

    #[test]
    fn translation_emits_rule_order_then_message_order() {
        let translation = Translation::conventional();
        let mut out = Vec::new();
        translate_into(
            &translation,
            |key| {
                Ok(match key {
                    "deny" => vec!["b".to_string(), "a".to_string()],
                    "audit" => vec!["c".to_string()],
                    _ => vec![],
                })
            },
            &mut out,
        )
        .expect("translation succeeds");
        let seen: Vec<(&str, &str)> = out
            .iter()
            .map(|d| (d.severity.as_str(), d.message.as_str()))
            .collect();
        assert_eq!(
            seen,
            vec![
                (SEVERITY_HALT, "b"),
                (SEVERITY_HALT, "a"),
                (SEVERITY_INFO, "c"),
            ]
        );
    }

    #[test]
    fn the_declared_schema_admits_an_attachment_that_configures_nothing() {
        // Settings are optional for this plugin, and a settings value is checked
        // against this schema whether or not the attachment declares one. A
        // schema admitting only an object would therefore refuse every
        // attachment that configured nothing — which is the common case.
        let schema = settings_schema();
        let parsed: serde_json::Value =
            serde_json::from_str(&schema).expect("the declared schema is valid JSON");
        let admitted = parsed["type"]
            .as_array()
            .expect("the declared schema admits more than one kind of value");
        assert!(
            admitted.contains(&serde_json::json!("null")),
            "an attachment with no settings must be admitted: {schema}"
        );
        assert!(
            admitted.contains(&serde_json::json!("object")),
            "an attachment with settings must be admitted: {schema}"
        );
    }

    #[test]
    fn the_declared_schema_admits_the_closed_severity_set_only() {
        let schema = settings_schema();
        let parsed: serde_json::Value =
            serde_json::from_str(&schema).expect("the declared schema is valid JSON");
        let enumerated =
            &parsed["properties"][SETTINGS_FIELD]["items"]["properties"]["severity"]["enum"];
        assert_eq!(
            enumerated,
            &serde_json::json!([SEVERITY_HALT, SEVERITY_WARN, SEVERITY_INFO])
        );
    }
}
