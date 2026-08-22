// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//! The embedded expression evaluator and the wiring that feeds it.
//!
//! Evaluation happens entirely inside the plugin: the evaluator is compiled in,
//! so nothing outside runs a policy engine or needs to understand the expression
//! language. Each attached file is one expression; the payload the socket
//! delivers becomes the `input` the expressions read.
//!
//! # What an expression evaluates to
//!
//! Expression languages have no rule names to collect decisions under, so the
//! decisions are the value the expression produces: a map from decision key to
//! the messages reached under it.
//!
//! ```cel
//! {
//!   "deny": input.graph.spec.nodes
//!             .filter(n, input.graph.spec.nodes[n].resolved_version == "latest")
//!             .map(n, "component " + n + " tracks the latest version; pin it"),
//!   "warn": size(input.graph.spec.nodes) > 20 ? ["this build declares a lot of components"] : [],
//! }
//! ```
//!
//! A key the map omits reached no decision, which is an absent verdict rather
//! than a failure — so a policy set split across files need not name every key in
//! every file. A key may carry a list of messages, a single message, or the null
//! value for none of them.
//!
//! An expression that evaluates to anything other than a map is refused, by name,
//! with the shape to write. A bare comparison is the mistake this catches:
//! `size(input.graph.spec.nodes) <= 5` names no decision key and carries no
//! message, so reading its `false` as a rejection would mean inventing a message
//! the author never wrote — and inventing one is the silent decision this refusal
//! exists to prevent.
//!
//! # Ordering, and why the messages are sorted
//!
//! The messages under one decision key are reported in sorted order. This is a
//! guarantee about the plugin's output rather than a presentation choice, and the
//! reason is specific: a comprehension over a map — `.filter()`, `.map()`,
//! `.all()`, `.exists()` — walks that map in an order the evaluator does not fix,
//! so the same expression over the same build can produce the same messages in a
//! different sequence each time. Nothing in the resulting list distinguishes that
//! case from a list the author wrote out by hand, so reporting "the order the
//! expression produced" would mean reporting an arbitrary one — on precisely the
//! idiom a policy is most likely to be written in. Sorting makes identical input
//! yield an identical report.
//!
//! Across keys the order is the translation's declared rule order, and across
//! files it is the order the host supplied them in, which is sorted by name.
//! Nothing here iterates a map.
//!
//! # Failure is never a silent allow
//!
//! An expression that cannot be read, cannot be evaluated, or does not produce
//! decisions in the documented shape makes this plugin fail rather than return a
//! verdict. The failure is reported as a build fault naming the file. It is
//! deliberately distinct from a decision that stops the build: "this policy could
//! not be evaluated" must never read as "this policy allows the build".

use aggrega_pdk_rust::Diagnostic;
use cel::objects::{KeyRef, Map};
use cel::{Context, Program, Value};
use governancecore::sources::Source;
use governancecore::translation::{translate_into, Translation};

/// The name a policy addresses the role's payload through.
///
/// One name for the whole payload, so a policy reads the build graph as
/// `input.graph`, the rendered output as `input.artifacts`, the attachment's own
/// files as `input.sources`, and an operator-supplied threshold as
/// `input.settings.<key>` — the same names whichever role delivered it.
const INPUT_VARIABLE: &str = "input";

/// The name reported for an attached file that arrives without one.
const UNNAMED_SOURCE_PATH: &str = "policy.cel";

/// One attached expression, evaluated to the decisions it reached.
#[derive(Debug, Clone)]
pub struct EvaluatedSource {
    /// The attached file's name, as the decisions are attributed to it.
    pub path: String,
    /// What the expression evaluated to: decision keys mapped to their messages.
    decisions: Map,
}

/// Compiles and evaluates every attached expression against one input document,
/// in the order the files were supplied.
///
/// The input document is bound once and shared by every expression, so a policy
/// set of many files reads one projection of the build rather than one per file.
///
/// A file that cannot be read as an expression, cannot be evaluated, or does not
/// produce decisions in the documented shape stops the whole evaluation. Every
/// file is held to that before any verdict is reported, so a policy set does not
/// half-run.
pub fn evaluated_sources(
    sources: &[Source<'_>],
    input: &serde_json::Value,
) -> anyhow::Result<Vec<EvaluatedSource>> {
    let mut context = Context::default();
    context.add_variable(INPUT_VARIABLE, input).map_err(|err| {
        anyhow::anyhow!("the payload could not be read as a value a policy can address: {err}")
    })?;

    let mut evaluated = Vec::with_capacity(sources.len());
    for source in sources {
        let path = if source.path.is_empty() {
            UNNAMED_SOURCE_PATH
        } else {
            source.path
        };
        let program = Program::compile(source.content).map_err(|err| {
            anyhow::anyhow!("policy source {path:?} is not a readable expression: {err}")
        })?;
        let value = program.execute(&context).map_err(|err| {
            anyhow::anyhow!("policy source {path:?} could not be evaluated: {err}")
        })?;
        evaluated.push(EvaluatedSource {
            path: path.to_string(),
            decisions: admit(path, value)?,
        });
    }
    Ok(evaluated)
}

/// Requires an expression to have produced decisions in the documented shape, and
/// names what it produced instead when it did not.
///
/// The type name in the message is the expression language's own name for it, so
/// the author reads back a word from the language they wrote in.
fn admit(path: &str, value: Value) -> anyhow::Result<Map> {
    if let Value::Map(map) = value {
        return Ok(map);
    }
    anyhow::bail!(
        "policy source {path:?} evaluated to a value of type {kind}, and a policy source \
         evaluates to its decisions: a map from each decision key to the messages reached under \
         it, as in `{{\"deny\": [\"...\"]}}`. A key carries a list of messages, a single message, \
         or null for none. Wrap the expression in the decision key its result belongs to.",
        kind = value.type_of()
    )
}

/// Evaluates the attached expressions against one input document and translates
/// the decisions they reached into diagnostics. This is what the plugin reports
/// when it is attached to render a verdict.
///
/// Determinism. Files are evaluated in the order the host supplied them, which is
/// sorted by name; translation rules in their declared order; and a key's messages
/// sorted. Nothing here iterates a map.
pub fn evaluate(
    sources: &[Source<'_>],
    input: &serde_json::Value,
    translation: &Translation,
) -> anyhow::Result<Vec<Diagnostic>> {
    let mut results = Vec::new();
    for evaluated in evaluated_sources(sources, input)? {
        translate_into(
            translation,
            |key| messages_for(&evaluated, key),
            &mut results,
        )?;
    }
    Ok(results)
}

/// Reads the messages one decision key collects off an evaluated expression, in
/// sorted order.
///
/// A key the expression did not name, or named as null, collects nothing — an
/// absent decision rather than a failure. A key holding anything other than
/// messages is a malformed policy and fails rather than contributing a
/// stringified or dropped message.
fn messages_for(evaluated: &EvaluatedSource, key: &str) -> anyhow::Result<Vec<String>> {
    let Some(value) = evaluated.decisions.get(&KeyRef::String(key)) else {
        return Ok(Vec::new());
    };
    let mut messages = match value {
        Value::Null => Vec::new(),
        Value::String(single) => vec![single.to_string()],
        Value::List(items) => items
            .iter()
            .map(|item| message(&evaluated.path, key, item))
            .collect::<anyhow::Result<Vec<String>>>()?,
        other => anyhow::bail!(
            "policy source {:?} collected decision key {key:?} as a value of type {kind}; a \
             decision key carries a list of messages, a single message, or null for none.",
            evaluated.path,
            kind = other.type_of()
        ),
    };
    // See this module's ordering note: a comprehension over a map yields an order
    // the evaluator does not fix, so sorting is what makes identical input yield
    // an identical report.
    messages.sort();
    Ok(messages)
}

/// Reads one collected message, requiring it to be text so a malformed decision
/// fails rather than reporting an uninterpretable message.
fn message(path: &str, key: &str, value: &Value) -> anyhow::Result<String> {
    match value {
        Value::String(text) => Ok(text.to_string()),
        other => anyhow::bail!(
            "policy source {path:?} collected a value of type {kind} rather than a message under \
             decision key {key:?}; every entry under a decision key is the text reported for it.",
            kind = other.type_of()
        ),
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use aggrega_pdk_rust::{SEVERITY_HALT, SEVERITY_INFO, SEVERITY_WARN};

    fn source(content: &str) -> Source<'_> {
        Source {
            path: "policy.cel",
            content,
        }
    }

    fn graph(nodes: &[&str]) -> serde_json::Value {
        let mut graph = aggrega_abi::IRGraph::default();
        for name in nodes {
            graph
                .spec
                .nodes
                .insert((*name).to_string(), aggrega_abi::IRNode::default());
        }
        serde_json::json!({
            "graph": serde_json::to_value(&graph).expect("a default graph serializes"),
            "precalculated_paths": [],
            "sources": [],
            "settings": {},
        })
    }

    fn diagnostics(policy: &str, input: &serde_json::Value) -> Vec<Diagnostic> {
        evaluate(&[source(policy)], input, &Translation::conventional())
            .expect("evaluation succeeds")
    }

    #[test]
    fn a_decision_map_reports_one_diagnostic_per_message() {
        let out = diagnostics(
            r#"{"deny": ["pin a version", "add a probe"]}"#,
            &graph(&["web"]),
        );
        let seen: Vec<(&str, &str)> = out
            .iter()
            .map(|d| (d.severity.as_str(), d.message.as_str()))
            .collect();
        assert_eq!(
            seen,
            vec![
                (SEVERITY_HALT, "add a probe"),
                (SEVERITY_HALT, "pin a version"),
            ]
        );
    }

    #[test]
    fn a_single_message_needs_no_list() {
        let out = diagnostics(r#"{"deny": "pin a version"}"#, &graph(&["web"]));
        assert_eq!(out.len(), 1, "{out:?}");
        assert_eq!(out[0].message, "pin a version");
    }

    #[test]
    fn an_expression_reads_the_build_graph() {
        let policy =
            r#"{"deny": size(input.graph.spec.nodes) > 1 ? ["more than one component"] : []}"#;
        assert!(diagnostics(policy, &graph(&["a"])).is_empty());
        let out = diagnostics(policy, &graph(&["a", "b"]));
        assert_eq!(out.len(), 1, "{out:?}");
        assert_eq!(out[0].message, "more than one component");
        assert_eq!(out[0].severity, SEVERITY_HALT);
    }

    #[test]
    fn an_expression_reads_an_operator_supplied_threshold() {
        let policy = r#"{"warn": size(input.graph.spec.nodes) > input.settings.max_nodes ? ["too many"] : []}"#;
        let mut input = graph(&["a", "b", "c"]);
        input["settings"] = serde_json::json!({ "max_nodes": 2 });
        let out = diagnostics(policy, &input);
        assert_eq!(out.len(), 1, "{out:?}");
        assert_eq!(out[0].severity, SEVERITY_WARN);
    }

    #[test]
    fn a_key_the_expression_omits_reaches_no_decision() {
        let out = diagnostics(r#"{"audit": ["noted"]}"#, &graph(&["a"]));
        assert_eq!(out.len(), 1, "{out:?}");
        assert_eq!(out[0].severity, SEVERITY_INFO);
    }

    #[test]
    fn a_key_carrying_null_reaches_no_decision() {
        let out = diagnostics(r#"{"deny": null, "warn": ["noted"]}"#, &graph(&["a"]));
        assert_eq!(out.len(), 1, "{out:?}");
        assert_eq!(out[0].severity, SEVERITY_WARN);
    }

    #[test]
    fn an_empty_decision_map_allows() {
        assert!(diagnostics("{}", &graph(&["a"])).is_empty());
    }

    #[test]
    fn a_key_the_translation_does_not_name_is_not_reported() {
        // The translation is the whole set of keys read, so a key outside it is
        // not a decision this plugin reports — which is what lets an operator
        // narrow the read to their own vocabulary.
        let out = diagnostics(r#"{"blocked": ["nope"]}"#, &graph(&["a"]));
        assert!(out.is_empty(), "{out:?}");
    }

    #[test]
    fn messages_are_sorted_so_identical_input_yields_an_identical_report() {
        // The expression comprehends over a map, whose iteration order the
        // evaluator does not fix, so this is the case sorting exists for.
        let policy = r#"{"deny": input.graph.spec.nodes.map(n, "component " + n)}"#;
        let input = graph(&["delta", "alpha", "charlie", "bravo", "echo"]);
        let first: Vec<String> = diagnostics(policy, &input)
            .into_iter()
            .map(|d| d.message)
            .collect();
        assert_eq!(
            first,
            vec![
                "component alpha",
                "component bravo",
                "component charlie",
                "component delta",
                "component echo",
            ]
        );
    }

    #[test]
    fn every_attached_file_contributes_in_the_order_supplied() {
        let out = evaluate(
            &[
                Source {
                    path: "a.cel",
                    content: r#"{"deny": ["from a"]}"#,
                },
                Source {
                    path: "b.cel",
                    content: r#"{"deny": ["from b"]}"#,
                },
            ],
            &graph(&["x"]),
            &Translation::conventional(),
        )
        .expect("evaluation succeeds");
        let seen: Vec<&str> = out.iter().map(|d| d.message.as_str()).collect();
        assert_eq!(seen, vec!["from a", "from b"]);
    }

    #[test]
    fn keys_are_reported_in_the_translations_declared_order() {
        let out = diagnostics(
            r#"{"audit": ["c"], "warn": ["b"], "deny": ["a"]}"#,
            &graph(&["x"]),
        );
        let seen: Vec<(&str, &str)> = out
            .iter()
            .map(|d| (d.severity.as_str(), d.message.as_str()))
            .collect();
        assert_eq!(
            seen,
            vec![
                (SEVERITY_HALT, "a"),
                (SEVERITY_WARN, "b"),
                (SEVERITY_INFO, "c"),
            ]
        );
    }

    #[test]
    fn an_unreadable_expression_fails_and_names_the_file_and_the_position() {
        let err = evaluate(
            &[Source {
                path: "broken.cel",
                content: "{\"deny\": [",
            }],
            &graph(&["a"]),
            &Translation::conventional(),
        )
        .expect_err("an unreadable expression fails rather than allowing");
        let text = err.to_string();
        assert!(text.contains("broken.cel"), "{text}");
        assert!(
            text.contains("not a readable expression"),
            "the failure says what went wrong: {text}"
        );
        assert!(
            text.contains("<input>:1:"),
            "the failure locates the position: {text}"
        );
    }

    #[test]
    fn an_expression_that_cannot_be_evaluated_fails_rather_than_allowing() {
        let err = evaluate(
            &[source(r#"{"deny": [input.graph.absent_field]}"#)],
            &graph(&["a"]),
            &Translation::conventional(),
        )
        .expect_err("an evaluation failure is not a verdict");
        let text = err.to_string();
        assert!(text.contains("could not be evaluated"), "{text}");
        assert!(text.contains("policy.cel"), "{text}");
    }

    #[test]
    fn a_bare_comparison_is_refused_with_the_shape_to_write() {
        let err = evaluate(
            &[source("size(input.graph.spec.nodes) <= 5")],
            &graph(&["a"]),
            &Translation::conventional(),
        )
        .expect_err("a bare comparison names no decision key and no message");
        let text = err.to_string();
        assert!(text.contains("evaluated to a value of type bool"), "{text}");
        assert!(
            text.contains("\"deny\""),
            "the refusal shows the shape to write: {text}"
        );
    }

    #[test]
    fn a_decision_key_carrying_the_wrong_kind_of_value_is_refused() {
        let err = evaluate(
            &[source(r#"{"deny": 3}"#)],
            &graph(&["a"]),
            &Translation::conventional(),
        )
        .expect_err("a decision key carries messages");
        assert!(err.to_string().contains("as a value of type int"), "{err}");
    }

    #[test]
    fn a_non_message_entry_under_a_decision_key_is_refused() {
        let err = evaluate(
            &[source(r#"{"deny": ["fine", 7]}"#)],
            &graph(&["a"]),
            &Translation::conventional(),
        )
        .expect_err("every entry under a decision key is a message");
        let text = err.to_string();
        assert!(text.contains("collected a value of type int"), "{text}");
        assert!(text.contains("policy.cel"), "{text}");
    }

    #[test]
    fn an_attachment_with_no_files_reaches_no_decision() {
        let out = evaluate(&[], &graph(&["a"]), &Translation::conventional())
            .expect("no attached files is not a failure");
        assert!(out.is_empty());
    }

    #[test]
    fn a_file_arriving_without_a_name_is_still_named_in_a_failure() {
        let err = evaluate(
            &[Source {
                path: "",
                content: "1 +",
            }],
            &graph(&["a"]),
            &Translation::conventional(),
        )
        .expect_err("an unreadable expression fails");
        assert!(err.to_string().contains("policy.cel"), "{err}");
    }

    #[test]
    fn an_operator_translation_replaces_the_conventional_keys() {
        let translation = Translation::new(vec![governancecore::translation::Rule {
            key: "blocked".to_string(),
            severity: SEVERITY_HALT.to_string(),
        }])
        .expect("a well-formed translation is accepted");
        let out = evaluate(
            &[source(r#"{"blocked": ["nope"], "deny": ["ignored"]}"#)],
            &graph(&["a"]),
            &translation,
        )
        .expect("evaluation succeeds");
        let seen: Vec<(&str, &str)> = out
            .iter()
            .map(|d| (d.severity.as_str(), d.message.as_str()))
            .collect();
        assert_eq!(seen, vec![(SEVERITY_HALT, "nope")]);
    }
}
