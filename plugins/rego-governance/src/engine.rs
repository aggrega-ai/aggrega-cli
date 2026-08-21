// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//! The embedded Rego evaluator and the wiring that feeds it.
//!
//! Evaluation happens entirely inside the plugin: the engine is compiled in, so
//! nothing outside runs a policy engine or needs to understand the policy
//! language. The attachment's own files become the policy modules, its settings
//! become the policy's `data` document, and the payload the socket delivers
//! becomes the policy's `input` document.
//!
//! Reading decisions. Every package the attached files declare is evaluated to
//! its object, and each translation rule's decision key is read off that object.
//! Reading a key off an evaluated object rather than querying the rule path
//! matters: querying a rule a policy does not declare is itself an error and is
//! indistinguishable from a real failure, whereas an absent key is simply an
//! absent verdict.
//!
//! Loud failure. When evaluating a package fails — most often because the policy
//! calls a builtin this engine does not implement — the failure propagates rather
//! than being reported as a verdict. That distinction is the point: "this policy
//! could not be evaluated" must never read as "this policy allows the build".

use aggrega_abi::PolicySource;
use aggrega_pdk_rust::Diagnostic;
use regorus::Value;
use serde_json::value::RawValue;

use crate::translation::{translate_into, Translation};

/// The module path used for an attached file that arrives without one.
const UNNAMED_SOURCE_PATH: &str = "policy.rego";

/// Reads an attachment's settings as a JSON value for the policy's `data`
/// document, yielding an empty object when the attachment declares none.
///
/// The settings are carried through exactly as the attachment wrote them. The
/// translation is read out of the same object rather than removed from it, so a
/// policy that reads its own settings sees precisely what it saw before a
/// translation was configurable.
pub fn settings_value(settings: Option<&RawValue>) -> serde_json::Value {
    match settings {
        Some(raw) => serde_json::from_str(raw.get()).unwrap_or_else(|_| serde_json::json!({})),
        None => serde_json::json!({}),
    }
}

/// Evaluates the attached policies against one input document and translates the
/// decisions they reach into diagnostics.
///
/// Determinism. Packages are evaluated in the order the engine reports them,
/// translation rules in their declared order, and a rule's messages in the order
/// the evaluated collection yields — a set yields its members sorted, an array
/// keeps author order. Nothing here iterates a hash map.
pub fn evaluate(
    sources: &[PolicySource],
    settings: Option<&RawValue>,
    input: &serde_json::Value,
    translation: &Translation,
) -> anyhow::Result<Vec<Diagnostic>> {
    let mut engine = regorus::Engine::new();

    // Each attached file is a policy module; its content is the raw policy text.
    for source in sources {
        let path = if source.path.is_empty() {
            UNNAMED_SOURCE_PATH
        } else {
            source.path.as_str()
        };
        engine
            .add_policy(path.to_string(), source.content.clone())
            .map_err(|err| anyhow::anyhow!("policy source {path:?} did not load: {err}"))?;
    }

    // The attachment's settings become the policy's data document.
    if let Some(raw) = settings {
        let text = raw.get().trim();
        if text != "null" {
            engine.add_data(Value::from_json_str(text)?)?;
        }
    }

    // The socket's payload becomes the policy's input document.
    engine.set_input(Value::from_json_str(&serde_json::to_string(input)?)?);

    let mut results = Vec::new();
    let mut seen: Vec<String> = Vec::new();
    for package in engine.get_packages()? {
        // A package is reported once per module that declares it, so two files
        // sharing a package would otherwise be evaluated twice and report every
        // decision twice. Evaluating each distinct package once, in first-seen
        // order, keeps the reported decisions matching what the policy expressed.
        if seen.contains(&package) {
            continue;
        }
        seen.push(package.clone());
        let evaluated = eval_package(&mut engine, &package)?;
        translate_into(
            translation,
            |key| messages_for(&evaluated, key),
            &mut results,
        )?;
    }
    Ok(results)
}

/// Evaluates one package reference to its object value. A package that evaluates
/// to nothing is undefined and contributes no decision.
fn eval_package(engine: &mut regorus::Engine, package: &str) -> anyhow::Result<Value> {
    let results = engine.eval_query(package.to_string(), false)?;
    Ok(results
        .result
        .first()
        .and_then(|r| r.expressions.first())
        .map(|e| e.value.clone())
        .unwrap_or(Value::Undefined))
}

/// Reads the messages one decision key collects off an evaluated package object.
///
/// An absent or undefined key yields nothing, so a policy that does not declare a
/// key the translation names simply reaches no decision under it. A key holding
/// anything other than a collection of message strings is a malformed policy and
/// fails rather than contributing a stringified or dropped message.
fn messages_for(package_object: &Value, key: &str) -> anyhow::Result<Vec<String>> {
    let Ok(object) = package_object.as_object() else {
        return Ok(Vec::new());
    };
    let Some(value) = object.get(&Value::from(key)) else {
        return Ok(Vec::new());
    };
    if value == &Value::Undefined {
        return Ok(Vec::new());
    }
    // A set yields its members in sorted order; an array keeps author order.
    // Either is a deterministic sequence.
    if let Ok(set) = value.as_set() {
        return set.iter().map(|m| message(key, m)).collect();
    }
    if let Ok(array) = value.as_array() {
        return array.iter().map(|m| message(key, m)).collect();
    }
    anyhow::bail!("decision key {key:?} must hold a set or array of message strings")
}

/// Reads one collected message, requiring it to be a string so a malformed
/// collection fails rather than reporting an uninterpretable message.
fn message(key: &str, value: &Value) -> anyhow::Result<String> {
    Ok(value
        .as_string()
        .map_err(|err| {
            anyhow::anyhow!("decision key {key:?} collected a non-string message: {err}")
        })?
        .to_string())
}

#[cfg(test)]
mod tests {
    use super::*;
    use aggrega_pdk_rust::{SEVERITY_HALT, SEVERITY_INFO, SEVERITY_WARN};

    fn source(content: &str) -> PolicySource {
        PolicySource {
            path: "policy.rego".to_string(),
            content: content.to_string(),
        }
    }

    fn raw(json: &str) -> Box<RawValue> {
        RawValue::from_string(json.to_string()).expect("test JSON is valid")
    }

    const CONVENTIONAL_POLICY: &str = r#"
package proof

deny contains msg if {
    input.replicas > 3
    msg := "too many replicas"
}

warn contains msg if {
    input.replicas > 1
    msg := "more than one replica"
}

audit contains msg if {
    msg := "policy evaluated"
}
"#;

    #[test]
    fn the_default_translation_reports_the_convention_severities() {
        let results = evaluate(
            &[source(CONVENTIONAL_POLICY)],
            None,
            &serde_json::json!({ "replicas": 5 }),
            &Translation::conventional(),
        )
        .expect("evaluation succeeds");
        let seen: Vec<(&str, &str)> = results
            .iter()
            .map(|d| (d.severity.as_str(), d.message.as_str()))
            .collect();
        assert_eq!(
            seen,
            vec![
                (SEVERITY_HALT, "too many replicas"),
                (SEVERITY_WARN, "more than one replica"),
                (SEVERITY_INFO, "policy evaluated"),
            ]
        );
    }

    #[test]
    fn an_undeclared_decision_key_contributes_nothing() {
        let policy = r#"
package proof

audit contains msg if {
    msg := "only an audit here"
}
"#;
        let results = evaluate(
            &[source(policy)],
            None,
            &serde_json::json!({}),
            &Translation::conventional(),
        )
        .expect("an undeclared key is not a failure");
        assert_eq!(results.len(), 1);
        assert_eq!(results[0].severity, SEVERITY_INFO);
    }

    #[test]
    fn a_policy_reaching_no_decision_allows() {
        let results = evaluate(
            &[source(
                "package proof\n\ndeny contains msg if {\n  input.replicas > 3\n  msg := \"nope\"\n}\n",
            )],
            None,
            &serde_json::json!({ "replicas": 1 }),
            &Translation::conventional(),
        )
        .expect("evaluation succeeds");
        assert!(results.is_empty(), "{results:?}");
    }

    #[test]
    fn settings_reach_the_policy_data_document() {
        let policy = r#"
package proof

deny contains msg if {
    input.replicas > data.max_replicas
    msg := "over the configured ceiling"
}
"#;
        let settings = raw(r#"{"max_replicas":2}"#);
        let results = evaluate(
            &[source(policy)],
            Some(&settings),
            &serde_json::json!({ "replicas": 4 }),
            &Translation::conventional(),
        )
        .expect("evaluation succeeds");
        assert_eq!(results.len(), 1, "{results:?}");
        assert_eq!(results[0].severity, SEVERITY_HALT);
    }

    #[test]
    fn a_configured_translation_reads_a_custom_decision_key() {
        let policy = r#"
package proof

blocked contains msg if {
    msg := "a custom decision key fired"
}
"#;
        let settings = raw(r#"{"translation":[{"key":"blocked","severity":"halt"}]}"#);
        let translation =
            Translation::from_settings(Some(&settings)).expect("the override is well-formed");
        let results = evaluate(
            &[source(policy)],
            Some(&settings),
            &serde_json::json!({}),
            &translation,
        )
        .expect("evaluation succeeds");
        assert_eq!(results.len(), 1, "{results:?}");
        assert_eq!(results[0].severity, SEVERITY_HALT);
        assert_eq!(results[0].message, "a custom decision key fired");
    }

    #[test]
    fn a_configured_translation_stops_reading_the_default_keys() {
        let settings = raw(r#"{"translation":[{"key":"blocked","severity":"halt"}]}"#);
        let translation =
            Translation::from_settings(Some(&settings)).expect("the override is well-formed");
        let results = evaluate(
            &[source(CONVENTIONAL_POLICY)],
            Some(&settings),
            &serde_json::json!({ "replicas": 5 }),
            &translation,
        )
        .expect("evaluation succeeds");
        assert!(
            results.is_empty(),
            "an override replaces the default rather than extending it: {results:?}"
        );
    }

    #[test]
    fn an_unimplemented_builtin_fails_rather_than_allowing() {
        // A builtin this engine does not implement must surface as a failure. If
        // the engine ever implements it, this policy evaluates and reaches a
        // decision instead — which is also not a silent allow, so the assertion
        // admits either outcome and rejects only "no decision, no error".
        let policy = r#"
package proof

deny contains msg if {
    net.lookup_ip_addr("example.com")
    msg := "resolved"
}
"#;
        let outcome = evaluate(
            &[source(policy)],
            None,
            &serde_json::json!({}),
            &Translation::conventional(),
        );
        match outcome {
            Err(_) => {}
            Ok(results) => assert!(
                !results.is_empty(),
                "an unimplemented builtin must not produce a silent allow"
            ),
        }
    }

    #[test]
    fn a_decision_key_holding_a_non_collection_fails_loudly() {
        let policy = "package proof\n\ndeny := \"not a collection\"\n";
        let err = evaluate(
            &[source(policy)],
            None,
            &serde_json::json!({}),
            &Translation::conventional(),
        )
        .expect_err("a malformed decision key fails");
        assert!(err.to_string().contains("deny"), "{err}");
    }

    #[test]
    fn a_source_that_does_not_load_fails_loudly() {
        let err = evaluate(
            &[source("this is not rego at all {{{")],
            None,
            &serde_json::json!({}),
            &Translation::conventional(),
        )
        .expect_err("an unloadable source fails");
        assert!(err.to_string().contains("policy.rego"), "{err}");
    }

    #[test]
    fn two_files_sharing_a_package_report_each_decision_once() {
        let a = PolicySource {
            path: "a.rego".to_string(),
            content: "package proof\n\ndeny contains msg if { msg := \"from a\" }\n".to_string(),
        };
        let b = PolicySource {
            path: "b.rego".to_string(),
            content: "package proof\n\naudit contains msg if { msg := \"from b\" }\n".to_string(),
        };
        let results = evaluate(
            &[a, b],
            None,
            &serde_json::json!({}),
            &Translation::conventional(),
        )
        .expect("evaluation succeeds");
        let seen: Vec<&str> = results.iter().map(|d| d.message.as_str()).collect();
        assert_eq!(seen, vec!["from a", "from b"], "each decision once");
    }

    #[test]
    fn packages_are_evaluated_in_the_order_their_sources_arrive() {
        // The host supplies sources sorted by path, and each becomes a module in
        // that order, so the reported decisions follow the source order.
        let first = PolicySource {
            path: "a.rego".to_string(),
            content: "package alpha\n\ndeny contains msg if { msg := \"alpha\" }\n".to_string(),
        };
        let second = PolicySource {
            path: "b.rego".to_string(),
            content: "package beta\n\ndeny contains msg if { msg := \"beta\" }\n".to_string(),
        };
        let results = evaluate(
            &[first, second],
            None,
            &serde_json::json!({}),
            &Translation::conventional(),
        )
        .expect("evaluation succeeds");
        let seen: Vec<&str> = results.iter().map(|d| d.message.as_str()).collect();
        assert_eq!(seen, vec!["alpha", "beta"]);
    }

    #[test]
    fn evaluation_is_repeatable() {
        let run = || {
            evaluate(
                &[source(CONVENTIONAL_POLICY)],
                None,
                &serde_json::json!({ "replicas": 5 }),
                &Translation::conventional(),
            )
            .expect("evaluation succeeds")
            .iter()
            .map(|d| format!("{}:{}", d.severity, d.message))
            .collect::<Vec<_>>()
        };
        assert_eq!(run(), run());
    }
}
