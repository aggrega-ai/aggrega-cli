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
//! its object, and each decision key is read off that object. Reading a key off
//! an evaluated object rather than querying the rule path matters: querying a
//! rule a policy does not declare is itself an error and is indistinguishable
//! from a real failure, whereas an absent key is simply an absent decision.
//!
//! One evaluation, four roles. [`evaluated_packages`] is the whole engine
//! contract: it loads the policy, evaluates it, and hands back each package's
//! object. What is read off those objects is the only thing that differs between
//! what the plugin is attached at — messages under a severity-mapped key when it
//! reports verdicts, decision objects under a named key when it computes a
//! change — so the loading, the data wiring, the determinism argument and the
//! failure behaviour are written once and shared.
//!
//! Loud failure. When evaluating a package fails — most often because the policy
//! calls a builtin this engine does not implement — the failure propagates rather
//! than being reported as a decision. That distinction is the point: "this policy
//! could not be evaluated" must never read as "this policy allows the build".

use aggrega_abi::{AnnotationSource, MutatorSource, PolicySource};
use aggrega_pdk_rust::Diagnostic;
use regorus::Value;
use serde_json::value::RawValue;

use crate::bundle;
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

/// One attached file as the evaluator reads it: a name and its text.
///
/// Each of the plugin's roles receives its own source type on the wire, all with
/// the same two fields, so this borrowed view is what lets one evaluator serve
/// every role without copying content or restating the loading rules per role.
#[derive(Debug, Clone, Copy)]
pub struct Source<'a> {
    /// The attached file's name.
    pub path: &'a str,
    /// The file's text, exactly as it was attached.
    pub content: &'a str,
}

impl<'a> From<&'a PolicySource> for Source<'a> {
    fn from(source: &'a PolicySource) -> Self {
        Source {
            path: source.path.as_str(),
            content: source.content.as_str(),
        }
    }
}

impl<'a> From<&'a MutatorSource> for Source<'a> {
    fn from(source: &'a MutatorSource) -> Self {
        Source {
            path: source.path.as_str(),
            content: source.content.as_str(),
        }
    }
}

impl<'a> From<&'a AnnotationSource> for Source<'a> {
    fn from(source: &'a AnnotationSource) -> Self {
        Source {
            path: source.path.as_str(),
            content: source.content.as_str(),
        }
    }
}

/// Loads the attached policies, evaluates them, and returns each distinct
/// package's evaluated object in first-seen order.
///
/// This is the single evaluation core every role shares. Callers differ only in
/// what they read off the returned objects.
///
/// Attached files are either policy text or an encoded archive holding a whole
/// policy set; an archive's own name says which, and its policy files and data
/// documents are read out of it in place. The attachment's settings and any data
/// an archive carried become the policy's `data` document, and the payload the
/// role delivers becomes its `input` document.
///
/// Determinism. Packages come back in the order the engine reports them, which
/// follows the order the files were added, and the files arrive in a fixed
/// order. Nothing here iterates a hash map.
pub fn evaluated_packages(
    sources: &[Source<'_>],
    settings: Option<&RawValue>,
    input: &serde_json::Value,
) -> anyhow::Result<Vec<Value>> {
    let mut engine = regorus::Engine::new();
    let mut archived_data: Vec<serde_json::Value> = Vec::new();

    for source in sources {
        let path = if source.path.is_empty() {
            UNNAMED_SOURCE_PATH
        } else {
            source.path
        };
        if bundle::is_encoded_archive(path) {
            let opened = bundle::read(path, source.content)?;
            for module in opened.modules {
                engine
                    .add_policy(module.path.clone(), module.content)
                    .map_err(|err| {
                        anyhow::anyhow!(
                            "attached archive {path:?} member {:?} did not load as a policy: {err}",
                            module.path
                        )
                    })?;
            }
            archived_data.extend(opened.data);
            continue;
        }
        engine
            .add_policy(path.to_string(), source.content.to_string())
            .map_err(|err| anyhow::anyhow!("policy source {path:?} did not load: {err}"))?;
    }

    // An archive's data documents become part of the policy's data document,
    // added before the attachment's settings so a settings key that collides
    // with one the archive carried is reported against the settings.
    for document in archived_data {
        engine.add_data(Value::from_json_str(&serde_json::to_string(&document)?)?)?;
    }

    // The attachment's settings become the policy's data document.
    if let Some(raw) = settings {
        let text = raw.get().trim();
        if text != "null" {
            engine.add_data(Value::from_json_str(text)?)?;
        }
    }

    // The role's payload becomes the policy's input document.
    engine.set_input(Value::from_json_str(&serde_json::to_string(input)?)?);

    let mut evaluated = Vec::new();
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
        evaluated.push(eval_package(&mut engine, &package)?);
    }
    Ok(evaluated)
}

/// Evaluates the attached policies against one input document and translates the
/// decisions they reach into diagnostics. This is what the plugin reports when it
/// is attached to render a verdict.
///
/// Determinism. Packages are evaluated in the order the engine reports them,
/// translation rules in their declared order, and a rule's messages in the order
/// the evaluated collection yields — a set yields its members sorted, an array
/// keeps author order. Nothing here iterates a hash map.
pub fn evaluate(
    sources: &[Source<'_>],
    settings: Option<&RawValue>,
    input: &serde_json::Value,
    translation: &Translation,
) -> anyhow::Result<Vec<Diagnostic>> {
    let mut results = Vec::new();
    for evaluated in evaluated_packages(sources, settings, input)? {
        translate_into(
            translation,
            |key| messages_for(&evaluated, key),
            &mut results,
        )?;
    }
    Ok(results)
}

/// Evaluates the attached policies against one input document and returns every
/// decision object collected under `key`, across every package, in package order
/// then collection order. This is what the plugin reads when it is attached to
/// compute a change rather than to report a verdict.
///
/// A package that does not declare `key` contributes nothing, so a policy set
/// split across packages need not declare the key in all of them.
pub fn decisions(
    sources: &[Source<'_>],
    settings: Option<&RawValue>,
    input: &serde_json::Value,
    key: &str,
) -> anyhow::Result<Vec<serde_json::Value>> {
    let mut collected = Vec::new();
    for evaluated in evaluated_packages(sources, settings, input)? {
        collected.extend(values_for(&evaluated, key)?);
    }
    Ok(collected)
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

/// Reads the members one decision key collects off an evaluated package object.
///
/// An absent or undefined key yields nothing, so a policy that does not declare a
/// key simply reaches no decision under it. A key holding anything that is not a
/// collection is a malformed policy and fails rather than contributing a single
/// coerced entry.
///
/// A set yields its members in sorted order and an array keeps author order.
/// Either is a deterministic sequence, which is why this is the one place a
/// decision key is read: every role reads its decisions through it and so shares
/// the same ordering guarantee.
fn collection_members<'a>(package_object: &'a Value, key: &str) -> anyhow::Result<Vec<&'a Value>> {
    let Ok(object) = package_object.as_object() else {
        return Ok(Vec::new());
    };
    let Some(value) = object.get(&Value::from(key)) else {
        return Ok(Vec::new());
    };
    if value == &Value::Undefined {
        return Ok(Vec::new());
    }
    if let Ok(set) = value.as_set() {
        return Ok(set.iter().collect());
    }
    if let Ok(array) = value.as_array() {
        return Ok(array.iter().collect());
    }
    anyhow::bail!("decision key {key:?} must hold a set or array of decisions")
}

/// Reads the messages one decision key collects off an evaluated package object.
///
/// A key holding anything other than message strings is a malformed policy and
/// fails rather than contributing a stringified or dropped message.
fn messages_for(package_object: &Value, key: &str) -> anyhow::Result<Vec<String>> {
    collection_members(package_object, key)?
        .into_iter()
        .map(|member| message(key, member))
        .collect()
}

/// Reads the decisions one decision key collects off an evaluated package
/// object, as values a role can interpret.
fn values_for(package_object: &Value, key: &str) -> anyhow::Result<Vec<serde_json::Value>> {
    collection_members(package_object, key)?
        .into_iter()
        .map(|member| {
            serde_json::to_value(member).map_err(|err| {
                anyhow::anyhow!(
                    "decision key {key:?} collected a decision that cannot be read: {err}"
                )
            })
        })
        .collect()
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

    fn source(content: &str) -> Source<'_> {
        Source {
            path: "policy.rego",
            content,
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
        let a = Source {
            path: "a.rego",
            content: "package proof\n\ndeny contains msg if { msg := \"from a\" }\n",
        };
        let b = Source {
            path: "b.rego",
            content: "package proof\n\naudit contains msg if { msg := \"from b\" }\n",
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
        let first = Source {
            path: "a.rego",
            content: "package alpha\n\ndeny contains msg if { msg := \"alpha\" }\n",
        };
        let second = Source {
            path: "b.rego",
            content: "package beta\n\ndeny contains msg if { msg := \"beta\" }\n",
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
