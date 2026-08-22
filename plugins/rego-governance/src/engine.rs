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
//! Rule syntax, and the one module this evaluator refuses. Rego is written in
//! two rule syntaxes, and both are accepted here, so a policy set assembled
//! before the current syntax existed evaluates alongside one written today. Each
//! module chooses for itself: a module in the current syntax declares
//! `import rego.v1`, and a module in the older syntax declares nothing, because
//! it is recognised by not parsing as the current one. A module that *does*
//! parse as the current syntax while omitting the import is refused, named, and
//! told what to add. It is refused rather than evaluated because its rules parse
//! under both syntaxes and the two can reach opposite decisions from the same
//! text — a body that never holds contributes no decision under the current
//! syntax and contributes one under the older syntax — so evaluating it would
//! mean choosing a meaning on the author's behalf, and choosing wrong inverts a
//! verdict with nothing to show for it. The refusal costs one module that means
//! the same either way, a module with no rule bodies at all, and the cost of
//! that is a one-line addition.
//!
//! Loud failure. When evaluating a package fails — most often because the policy
//! calls a builtin this engine does not implement — the failure propagates rather
//! than being reported as a decision. That distinction is the point: "this policy
//! could not be evaluated" must never read as "this policy allows the build".

use aggrega_pdk_rust::Diagnostic;
use governancecore::sources::Source;
use governancecore::translation::{translate_into, Translation};
use regorus::Value;
use serde_json::value::RawValue;

use crate::bundle;

/// The module path used for an attached file that arrives without one.
const UNNAMED_SOURCE_PATH: &str = "policy.rego";

/// The import path a module declares to say it is written in the current rule
/// syntax.
const CURRENT_SYNTAX_IMPORT_PATH: &str = "rego.v1";

/// One package the attached policies declared, evaluated to its object.
#[derive(Debug, Clone)]
pub struct EvaluatedPackage {
    /// The package reference, as the policy declared it.
    pub package: String,
    /// What the package evaluated to.
    pub object: Value,
}

/// One decision a policy reached, carrying the package that reached it.
///
/// The package travels with the decision because a decision is interpreted a
/// layer away from where it was read, and a decision key is read in *every*
/// attached package. Without the package name, a rule that happens to share a
/// decision key's name in an unrelated helper file reports as a malformed
/// decision, which sends the reader to inspect the decisions they meant to
/// write instead of the rule that collided with them.
#[derive(Debug, Clone)]
pub struct CollectedDecision {
    /// The package the decision was collected from.
    pub package: String,
    /// The decision as the policy expressed it.
    pub value: serde_json::Value,
}

/// Splits a leading keyword off a module-header line, requiring whitespace after
/// it so a longer word starting with the same letters is not read as the keyword.
fn after_keyword<'a>(line: &'a str, keyword: &str) -> Option<&'a str> {
    let rest = line.strip_prefix(keyword)?;
    if rest.starts_with(char::is_whitespace) {
        Some(rest.trim())
    } else {
        None
    }
}

/// Whether a module's header declares the import that marks it as written in the
/// current rule syntax.
///
/// Only the header is read — the package declaration and the import lines that
/// may follow it, which is the only place the policy language admits an import.
/// Reading the whole module would let the same words appearing inside a rule be
/// taken for a declaration, and that is the one direction of error that matters
/// here: a missed declaration produces a refusal the author can act on in one
/// line, while an imagined one admits precisely the module this check exists to
/// refuse.
fn declares_current_syntax(content: &str) -> bool {
    let mut in_header = false;
    for line in content.lines() {
        let line = line.trim();
        if line.is_empty() || line.starts_with('#') {
            continue;
        }
        if !in_header {
            if after_keyword(line, "package").is_none() {
                return false;
            }
            in_header = true;
            continue;
        }
        match after_keyword(line, "import") {
            Some(path) if path == CURRENT_SYNTAX_IMPORT_PATH => return true,
            // Another import: still in the header, keep reading.
            Some(_) => continue,
            // The header ends at the first line that is not an import.
            None => return false,
        }
    }
    false
}

/// Whether the engine reads a module as the current rule syntax.
///
/// The engine is the discriminator, so nothing about the module's text is
/// inferred: a module written in the older syntax is one the current syntax
/// cannot parse. The probe engine is built fresh, used once and dropped, so it
/// shares no state with the engine that evaluates.
fn parses_as_current_syntax(path: &str, content: &str) -> bool {
    let mut probe = regorus::Engine::new();
    probe
        .add_policy(path.to_string(), content.to_string())
        .is_ok()
}

/// Refuses a module whose decisions would depend on which rule syntax it was
/// read as, and admits every other module unchanged.
///
/// See this module's own documentation for why this class is refused rather than
/// evaluated under one syntax or the other.
fn admit(path: &str, content: &str) -> anyhow::Result<()> {
    if !parses_as_current_syntax(path, content) || declares_current_syntax(content) {
        return Ok(());
    }
    anyhow::bail!(
        "policy source {path:?} must declare `import rego.v1`. Both Rego rule syntaxes are \
         accepted, and this module parses as either one — so without the import its rules could \
         be read in the older syntax, where the same text can reach the opposite decision. Add \
         `import rego.v1` on its own line below the package declaration."
    )
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
/// Both rule syntaxes are accepted, module by module, and a module whose meaning
/// would depend on which one it was read as is refused before anything is
/// evaluated. An archive's members are held to the same rule as an attached
/// file, since an archive is a way of delivering modules rather than a different
/// kind of policy.
///
/// Determinism. Packages come back in the order the engine reports them, which
/// follows the order the files were added, and the files arrive in a fixed
/// order. Nothing here iterates a hash map.
pub fn evaluated_packages(
    sources: &[Source<'_>],
    settings: Option<&RawValue>,
    input: &serde_json::Value,
) -> anyhow::Result<Vec<EvaluatedPackage>> {
    let mut engine = regorus::Engine::new();
    // Accept the older rule syntax as well as the current one. A module
    // declaring `import rego.v1` still parses as the current syntax, because the
    // engine builds a parser per module and the import turns it on for that
    // module alone.
    engine.set_rego_v0(true);
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
                admit(&module.path, &module.content)
                    .map_err(|err| anyhow::anyhow!("attached archive {path:?} member {err}"))?;
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
        admit(path, source.content)?;
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
        let object = eval_package(&mut engine, &package)?;
        evaluated.push(EvaluatedPackage { package, object });
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
/// decision collected under `key`, across every package, in package order then
/// collection order — each paired with the package that reached it. This is what
/// the plugin reads when it is attached to compute a change rather than to report
/// a verdict.
///
/// A package that does not declare `key` contributes nothing, so a policy set
/// split across packages need not declare the key in all of them.
pub fn decisions(
    sources: &[Source<'_>],
    settings: Option<&RawValue>,
    input: &serde_json::Value,
    key: &str,
) -> anyhow::Result<Vec<CollectedDecision>> {
    let mut collected = Vec::new();
    for evaluated in evaluated_packages(sources, settings, input)? {
        for value in values_for(&evaluated, key)? {
            collected.push(CollectedDecision {
                package: evaluated.package.clone(),
                value,
            });
        }
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

/// Reads the members one decision key collects off an evaluated package.
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
fn collection_members<'a>(
    evaluated: &'a EvaluatedPackage,
    key: &str,
) -> anyhow::Result<Vec<&'a Value>> {
    let Ok(object) = evaluated.object.as_object() else {
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
    anyhow::bail!(
        "package {:?} collects {key:?} as neither a set nor an array; a decision key holds a \
         collection of decisions. Every attached package is read for {key:?}, so a rule of that \
         name collecting anything else must be renamed.",
        evaluated.package
    )
}

/// Reads the messages one decision key collects off an evaluated package.
///
/// A key holding anything other than message strings is a malformed policy and
/// fails rather than contributing a stringified or dropped message.
fn messages_for(evaluated: &EvaluatedPackage, key: &str) -> anyhow::Result<Vec<String>> {
    collection_members(evaluated, key)?
        .into_iter()
        .map(|member| message(&evaluated.package, key, member))
        .collect()
}

/// Reads the decisions one decision key collects off an evaluated package, as
/// values a role can interpret.
fn values_for(evaluated: &EvaluatedPackage, key: &str) -> anyhow::Result<Vec<serde_json::Value>> {
    collection_members(evaluated, key)?
        .into_iter()
        .map(|member| {
            serde_json::to_value(member).map_err(|err| {
                anyhow::anyhow!(
                    "package {:?} collected a decision under {key:?} that cannot be read: {err}",
                    evaluated.package
                )
            })
        })
        .collect()
}

/// Reads one collected message, requiring it to be a string so a malformed
/// collection fails rather than reporting an uninterpretable message.
fn message(package: &str, key: &str, value: &Value) -> anyhow::Result<String> {
    Ok(value
        .as_string()
        .map_err(|err| {
            anyhow::anyhow!(
                "package {package:?} collected a non-string message under {key:?}: {err}. Every \
                 attached package is read for {key:?}, so a rule of that name collecting \
                 anything else must be renamed."
            )
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

import rego.v1

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

import rego.v1

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
                "package proof\n\nimport rego.v1\n\ndeny contains msg if {\n  input.replicas > 3\n  msg := \"nope\"\n}\n",
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

import rego.v1

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

import rego.v1

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

import rego.v1

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
        let policy = "package proof\n\nimport rego.v1\n\ndeny := \"not a collection\"\n";
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
    fn a_malformed_decision_key_names_the_package_that_collected_it() {
        // The measured confusion this guards against: the key is read in every
        // attached package, so a rule sharing its name in an unrelated helper
        // file failed the whole attachment with a message about the shape of a
        // decision, sending the reader to inspect their real decisions.
        let helper = Source {
            path: "helpers.rego",
            content: "package helpers\n\nimport rego.v1\n\ndeny := \"a helper's own string\"\n",
        };
        let err = evaluate(
            &[helper],
            None,
            &serde_json::json!({}),
            &Translation::conventional(),
        )
        .expect_err("a malformed decision key fails");
        assert!(
            err.to_string().contains("helpers"),
            "the diagnostic must name the package that collected it: {err}"
        );
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
            content:
                "package proof\n\nimport rego.v1\n\ndeny contains msg if { msg := \"from a\" }\n",
        };
        let b = Source {
            path: "b.rego",
            content:
                "package proof\n\nimport rego.v1\n\naudit contains msg if { msg := \"from b\" }\n",
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
            content:
                "package alpha\n\nimport rego.v1\n\ndeny contains msg if { msg := \"alpha\" }\n",
        };
        let second = Source {
            path: "b.rego",
            content: "package beta\n\nimport rego.v1\n\ndeny contains msg if { msg := \"beta\" }\n",
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

    #[test]
    fn a_collected_decision_carries_the_package_that_reached_it() {
        let policy = r#"
package tagging

import rego.v1

labels contains decision if {
    decision := {"node": "web", "key": "tier", "value": "gold"}
}
"#;
        let collected = decisions(&[source(policy)], None, &serde_json::json!({}), "labels")
            .expect("evaluation succeeds");
        assert_eq!(collected.len(), 1, "{collected:?}");
        assert_eq!(collected[0].package, "data.tagging");
    }

    // ---- Rule syntax ----------------------------------------------------
    //
    // A policy written in the older rule syntax evaluates, a policy written in
    // the current one evaluates when it says so, and the one module whose
    // decisions would depend on which syntax read it is refused. The refusal
    // tests below fail outright if the guard is removed, because without it the
    // same policies evaluate and reach a decision.

    /// A body that never holds, collecting a literal message. Under the current
    /// rule syntax this contributes no decision; under the older one it
    /// contributes one — so the same text reaches opposite verdicts, and neither
    /// outcome is an error.
    ///
    /// The literal message is deliberate. A body binding a variable instead
    /// (`msg := "inverted"`) is rejected loudly under the older syntax, because
    /// the variable is unsafe there — which would make these tests pass on an
    /// engine error rather than on the guard. With a literal there is nothing
    /// loud to fall back on: without the guard the verdict simply inverts, which
    /// is what the guard exists to prevent and what makes its removal visible
    /// here.
    const INVERTING_BODY: &str = "deny contains \"inverted\" if {\n    1 > 2\n}\n";

    #[test]
    fn a_policy_in_the_older_rule_syntax_evaluates() {
        let policy = "package proof\n\ndeny[msg] {\n    input.replicas > 3\n    msg := \"too many replicas\"\n}\n";
        let results = evaluate(
            &[source(policy)],
            None,
            &serde_json::json!({ "replicas": 5 }),
            &Translation::conventional(),
        )
        .expect("a policy in the older rule syntax evaluates");
        let seen: Vec<(&str, &str)> = results
            .iter()
            .map(|d| (d.severity.as_str(), d.message.as_str()))
            .collect();
        assert_eq!(seen, vec![(SEVERITY_HALT, "too many replicas")]);
    }

    #[test]
    fn a_policy_declaring_the_current_rule_syntax_is_read_in_it() {
        let policy = format!("package proof\n\nimport rego.v1\n\n{INVERTING_BODY}");
        let results = evaluate(
            &[source(&policy)],
            None,
            &serde_json::json!({}),
            &Translation::conventional(),
        )
        .expect("a declared module evaluates");
        assert!(
            results.is_empty(),
            "a body that never holds reaches no decision in the current rule syntax: {results:?}"
        );
    }

    #[test]
    fn a_policy_that_does_not_say_which_rule_syntax_it_is_written_in_is_refused() {
        let policy = format!("package proof\n\n{INVERTING_BODY}");
        let err = evaluate(
            &[source(&policy)],
            None,
            &serde_json::json!({}),
            &Translation::conventional(),
        )
        .expect_err("a module that parses as either rule syntax is refused, not evaluated");
        let message = err.to_string();
        assert!(
            message.contains("policy.rego"),
            "the refusal names the module: {message}"
        );
        assert!(
            message.contains("import rego.v1"),
            "the refusal says what to add: {message}"
        );
    }

    #[test]
    fn a_module_with_no_rule_bodies_is_refused_too() {
        // The one refusal that costs something: this module means the same in
        // either rule syntax. It is refused because the check asks what the
        // module declares rather than guessing from its text, and the ask is one
        // line. Recorded as a test so the cost is visible rather than surprising.
        let err = evaluate(
            &[source("package proof\n")],
            None,
            &serde_json::json!({}),
            &Translation::conventional(),
        )
        .expect_err("an undeclared module is refused whether or not it has rule bodies");
        assert!(err.to_string().contains("import rego.v1"), "{err}");
    }

    #[test]
    fn the_two_rule_syntaxes_evaluate_side_by_side_in_one_attachment() {
        let older = Source {
            path: "older.rego",
            content: "package older\n\ndeny[msg] {\n    msg := \"from the older syntax\"\n}\n",
        };
        let current = Source {
            path: "current.rego",
            content: "package current\n\nimport rego.v1\n\ndeny contains msg if {\n    msg := \"from the current syntax\"\n}\n",
        };
        let results = evaluate(
            &[older, current],
            None,
            &serde_json::json!({}),
            &Translation::conventional(),
        )
        .expect("both rule syntaxes evaluate in one attachment");
        let seen: Vec<&str> = results.iter().map(|d| d.message.as_str()).collect();
        assert_eq!(
            seen,
            vec!["from the older syntax", "from the current syntax"]
        );
    }

    #[test]
    fn an_archive_member_that_does_not_say_which_rule_syntax_it_uses_is_refused() {
        // An archive is a way of delivering modules, so its members are held to
        // the same rule. The archive branch of the loading loop is a second load
        // site, and a guard covering only the first would leave it open.
        let policy = format!("package proof\n\n{INVERTING_BODY}");
        let archive = crate::bundle::encoded_archive(&[("p.rego", policy.as_str())]);
        let err = evaluate(
            &[Source {
                path: "bundle.tar.gz.base64",
                content: &archive,
            }],
            None,
            &serde_json::json!({}),
            &Translation::conventional(),
        )
        .expect_err("an archive member is refused on the same terms as an attached file");
        let message = err.to_string();
        assert!(
            message.contains("bundle.tar.gz.base64"),
            "the refusal names the archive: {message}"
        );
        assert!(
            message.contains("p.rego"),
            "the refusal names the member: {message}"
        );
        assert!(
            message.contains("import rego.v1"),
            "the refusal says what to add: {message}"
        );
    }

    #[test]
    fn an_archive_member_in_the_older_rule_syntax_evaluates() {
        let archive = crate::bundle::encoded_archive(&[(
            "p.rego",
            "package proof\n\ndeny[msg] {\n    msg := \"from an archived older-syntax module\"\n}\n",
        )]);
        let results = evaluate(
            &[Source {
                path: "bundle.tar.gz.base64",
                content: &archive,
            }],
            None,
            &serde_json::json!({}),
            &Translation::conventional(),
        )
        .expect("an archived module in the older rule syntax evaluates");
        let seen: Vec<&str> = results.iter().map(|d| d.message.as_str()).collect();
        assert_eq!(seen, vec!["from an archived older-syntax module"]);
    }

    #[test]
    fn the_declaration_is_read_from_the_header_and_not_from_a_rule_body() {
        // Only the module header can carry an import, so text resembling one
        // inside a rule must not be taken for a declaration. Getting this
        // direction wrong would admit exactly the module the guard refuses.
        let policy = "package proof\n\ndeny contains msg if {\n    msg := `import rego.v1`\n}\n";
        assert!(
            !declares_current_syntax(policy),
            "an import inside a rule body is not a declaration"
        );
        let header = "# a leading comment\npackage proof\n\nimport data.helpers\nimport rego.v1\n\ndeny contains msg if { msg := \"x\" }\n";
        assert!(
            declares_current_syntax(header),
            "a declaration is found past comments, blank lines and other imports"
        );
        assert!(
            !declares_current_syntax("package proof\n\nimport rego.v1beta1\n"),
            "a longer import path is not the declaration"
        );
    }
}
