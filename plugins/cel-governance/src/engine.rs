// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

//! The embedded expression evaluator and the wiring that feeds it.
//!
//! Evaluation happens entirely inside the plugin: the evaluator is compiled in,
//! so nothing outside runs a policy engine or needs to understand the expression
//! language. The payload the socket delivers becomes the `input` the expressions
//! read.
//!
//! Each attached file is one expression, except for a file named as a rules
//! document, which is read as the expression it stands for before anything is
//! evaluated — see [`crate::rules`]. So whichever form a policy is written in,
//! what reaches the evaluator is one expression, evaluated once, admitted once,
//! and ordered by one rule.
//!
//! # What an expression evaluates to
//!
//! Expression languages have no rule names to collect decisions under, so the
//! decisions are the value the expression produces: a map from decision key to
//! what that key collects. At a verdict role a key collects the messages reached
//! under it; at a role that shapes the build it collects the decisions to apply,
//! each an object naming what it applies to. Both are read here; what a key's
//! contents mean is the reading role's own business.
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
//! every file, and a role reads only the key it is about. A key may carry a list of
//! what it collects, a single one of them, or the null value for none.
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
//! The decisions a role that shapes the build collects are a different case, and
//! this module deliberately does not order them: what a total order over them is
//! depends on what a decision under that key can express, so each of those roles
//! establishes and documents its own. What this module guarantees them is that the
//! sequence it hands over is the sequence the expressions produced, unshuffled by
//! anything here.
//!
//! # Failure is never a silent allow
//!
//! A policy that cannot be read, cannot be evaluated, or does not produce
//! decisions in the documented shape makes this plugin fail rather than return a
//! verdict. That covers a malformed rules document as much as an unreadable
//! expression. The failure is reported as a build fault naming the file. It is
//! deliberately distinct from a decision that stops the build: "this policy could
//! not be evaluated" must never read as "this policy allows the build".

use crate::rules;
use aggrega_pdk_rust::Diagnostic;
use cel::objects::{Key, KeyRef, Map};
use cel::{Context, Program, Value};
use governancecore::sources::Source;
use governancecore::translation::{translate_into, Translation};
use std::borrow::Cow;

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
        // A rules document is read as the expression it stands for, and that
        // expression is compiled and evaluated here like any other. Translating
        // rather than evaluating a second form is what keeps one compilation, one
        // evaluation, one result-shape admission and one ordering rule.
        let expression: Cow<'_, str> = if rules::is_rules_document(path) {
            Cow::Owned(rules::expression_source(path, source.content)?)
        } else {
            Cow::Borrowed(source.content)
        };
        let program = Program::compile(&expression).map_err(|err| {
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

/// What a decision under one key must name, as it is reported back to whoever
/// wrote it.
///
/// A decision that shapes a build is an object rather than a message, so a
/// refusal has to say which fields were expected. Carrying that alongside the key
/// rather than restating it in each message is what makes every refusal under one
/// key read the same way.
pub struct DecisionShape {
    /// The decision key a policy collects these decisions under.
    pub key: &'static str,
    /// Every field name a decision under this key may name. A decision naming
    /// anything else is refused rather than having the extra field ignored: a
    /// misspelled field would otherwise read as a decision that left it out.
    pub fields: &'static [&'static str],
    /// The sentence saying what a decision under this key names. It is appended to
    /// every refusal, so a reader is told the shape rather than only the fault.
    pub summary: &'static str,
}

/// One decision a policy collected under a decision key, with the file it came
/// from.
///
/// The fields are read by name, one at a time, and never iterated — the evaluator
/// holds a decision's fields in a map whose walk order it does not fix, so
/// reading by name is what keeps identical input producing an identical result.
/// The one place the fields are walked is the unknown-field check, which reports
/// what it found in sorted order for the same reason.
#[derive(Debug, Clone)]
pub struct CollectedDecision {
    /// The attached file's name, as the decision is attributed to it.
    pub path: String,
    /// The decision's own fields.
    fields: Map,
}

impl CollectedDecision {
    /// Reads a field a decision must name, requiring it to be text.
    ///
    /// A field left out, or named as the null value, is refused rather than read
    /// as empty: a coordinate or a key nobody wrote is not something a policy
    /// meant to ask for, while the empty string is a value an author can write
    /// deliberately.
    pub fn text(&self, shape: &DecisionShape, field: &str) -> anyhow::Result<String> {
        match self.fields.get(&KeyRef::String(field)) {
            Some(Value::String(text)) => Ok(text.to_string()),
            Some(Value::Null) | None => anyhow::bail!(
                "policy source {:?} collected a decision under {:?} that names no {field:?}. {}",
                self.path,
                shape.key,
                shape.summary
            ),
            Some(other) => Err(self.not_text(shape, field, other)),
        }
    }

    /// Reads a field a decision may leave out, requiring it to be text when it is
    /// named.
    pub fn optional_text(
        &self,
        shape: &DecisionShape,
        field: &str,
    ) -> anyhow::Result<Option<String>> {
        match self.fields.get(&KeyRef::String(field)) {
            Some(Value::String(text)) => Ok(Some(text.to_string())),
            Some(Value::Null) | None => Ok(None),
            Some(other) => Err(self.not_text(shape, field, other)),
        }
    }

    /// Reads a field a decision must name, of any shape the JSON model holds, as
    /// the JSON it is carried as.
    ///
    /// It is for a field whose shape is judged downstream rather than here, so it
    /// passes a string, a number, a boolean, a list or a map through as written.
    /// Left out, or named as the null value, it is refused as [`Self::text`]
    /// refuses it, because a field nobody wrote is not something a policy meant to
    /// ask for. A value the JSON model cannot hold — bytes, a timestamp, a
    /// duration, a type — is refused rather than coerced into one it can, so the
    /// value that travels is the one the expression produced.
    ///
    /// A map's keys are written in sorted order at every depth: the evaluator holds
    /// a map's entries in an order it does not fix, so sorting is what keeps
    /// identical input producing an identical value.
    pub fn json(&self, shape: &DecisionShape, field: &str) -> anyhow::Result<serde_json::Value> {
        match self.fields.get(&KeyRef::String(field)) {
            Some(Value::Null) | None => anyhow::bail!(
                "policy source {:?} collected a decision under {:?} that names no {field:?}. {}",
                self.path,
                shape.key,
                shape.summary
            ),
            Some(value) => json_of(value).map_err(|kind| {
                anyhow::anyhow!(
                    "policy source {:?} collected a decision under {:?} naming {field:?} with a \
                     value of type {kind} inside it, which has no JSON form. {}",
                    self.path,
                    shape.key,
                    shape.summary
                )
            }),
        }
    }

    /// Requires a decision to name only the fields its shape defines.
    ///
    /// The fields are walked here and nowhere else, and what is reported is sorted
    /// — so a decision carrying two unrecognised fields is refused with the same
    /// message every time rather than in whichever order the evaluator happened to
    /// hold them.
    pub fn admit_fields(&self, shape: &DecisionShape) -> anyhow::Result<()> {
        let mut unknown: Vec<String> = self
            .fields
            .map
            .keys()
            .map(field_name)
            .filter(|name| !shape.fields.contains(&name.as_str()))
            .map(|name| format!("{name:?}"))
            .collect();
        if unknown.is_empty() {
            return Ok(());
        }
        unknown.sort();
        anyhow::bail!(
            "policy source {:?} collected a decision under {:?} naming {}, which a decision does \
             not define. {}",
            self.path,
            shape.key,
            unknown.join(", "),
            shape.summary
        )
    }

    /// The refusal for a field named as something other than text.
    fn not_text(&self, shape: &DecisionShape, field: &str, value: &Value) -> anyhow::Error {
        anyhow::anyhow!(
            "policy source {:?} collected a decision under {:?} naming {field:?} as a value of \
             type {kind}; every field of a decision is text. {}",
            self.path,
            shape.key,
            shape.summary,
            kind = value.type_of()
        )
    }
}

/// Converts an evaluated value into the JSON model, or names the type of the
/// first value inside it that has no JSON form.
///
/// A map is written with its keys sorted, whatever order the evaluator held them
/// in, so the result depends on the value alone. Unlike the evaluator's own
/// conversion, which writes bytes as base64 text and a timestamp as a date
/// string, nothing here changes a value's type to give it a JSON form.
fn json_of(value: &Value) -> Result<serde_json::Value, String> {
    Ok(match value {
        Value::Null => serde_json::Value::Null,
        Value::Bool(flag) => serde_json::Value::Bool(*flag),
        Value::Int(number) => serde_json::Value::from(*number),
        Value::UInt(number) => serde_json::Value::from(*number),
        Value::Float(number) => serde_json::Number::from_f64(*number)
            .map(serde_json::Value::Number)
            .ok_or_else(|| "double holding no finite number".to_string())?,
        Value::String(text) => serde_json::Value::String(text.to_string()),
        Value::List(items) => serde_json::Value::Array(
            items.iter().map(json_of).collect::<Result<Vec<_>, _>>()?,
        ),
        Value::Map(map) => {
            let mut entries: Vec<(String, &Value)> =
                map.map.iter().map(|(key, entry)| (field_name(key), entry)).collect();
            entries.sort_by(|left, right| left.0.cmp(&right.0));
            let mut object = serde_json::Map::new();
            for (key, entry) in entries {
                object.insert(key, json_of(entry)?);
            }
            serde_json::Value::Object(object)
        }
        other => return Err(other.type_of().to_string()),
    })
}

/// One decision field name, as an author wrote it.
///
/// A field name is text in every decision this plugin reads, but the language
/// allows other scalars as map keys, so a decision naming one is reported by the
/// name it wrote rather than by anything internal.
fn field_name(key: &Key) -> String {
    match key {
        Key::String(name) => name.to_string(),
        Key::Int(number) => number.to_string(),
        Key::Uint(number) => number.to_string(),
        Key::Bool(flag) => flag.to_string(),
    }
}

/// Evaluates the attached expressions against one input document and returns
/// every decision collected under one key, across every file. This is what the
/// plugin reports from when it is attached to shape a build rather than to render
/// a verdict.
///
/// A file that does not name the key contributes nothing, so a policy set split
/// across files need not name it in every one.
///
/// The sequence returned is the order the expressions produced, and a
/// comprehension over a map does not fix that order — so a caller that emits these
/// establishes its own total order over them first. Each of the two callers
/// documents the order it establishes.
pub fn decisions(
    sources: &[Source<'_>],
    input: &serde_json::Value,
    shape: &DecisionShape,
) -> anyhow::Result<Vec<CollectedDecision>> {
    let mut collected = Vec::new();
    for evaluated in evaluated_sources(sources, input)? {
        for fields in decision_objects(&evaluated, shape)? {
            collected.push(CollectedDecision {
                path: evaluated.path.clone(),
                fields,
            });
        }
    }
    Ok(collected)
}

/// Reads the decisions one key collects off an evaluated expression, requiring
/// each to be an object.
///
/// A key the expression did not name, or named as null, collects nothing — an
/// absent decision rather than a failure. A key naming a single decision needs no
/// list around it. Anything else is a malformed policy and fails rather than
/// contributing a coerced or dropped decision.
fn decision_objects(
    evaluated: &EvaluatedSource,
    shape: &DecisionShape,
) -> anyhow::Result<Vec<Map>> {
    let key = shape.key;
    let Some(value) = evaluated.decisions.get(&KeyRef::String(key)) else {
        return Ok(Vec::new());
    };
    match value {
        Value::Null => Ok(Vec::new()),
        Value::Map(single) => Ok(vec![single.clone()]),
        Value::List(items) => items
            .iter()
            .map(|item| match item {
                Value::Map(fields) => Ok(fields.clone()),
                other => anyhow::bail!(
                    "policy source {:?} collected a value of type {kind} under decision key \
                     {key:?} rather than a decision. {}",
                    evaluated.path,
                    shape.summary,
                    kind = other.type_of()
                ),
            })
            .collect(),
        other => anyhow::bail!(
            "policy source {:?} collected decision key {key:?} as a value of type {kind}; a \
             decision key carries a list of decisions, a single decision, or null for none. {}",
            evaluated.path,
            shape.summary,
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

    /// The rules document and the hand-written expression used to prove the two
    /// forms decide identically. They are written to be equivalent by hand, and
    /// the test below asserts they are equivalent in fact.
    const EQUIVALENT_DOCUMENT: &str = "\
- key: deny
  expression: size(input.graph.spec.nodes) > 1
  message: this build declares more than one component
- key: warn
  expression: size(input.graph.spec.nodes) > 2
  message: this build declares more than two components
- key: deny
  expression: has(input.settings.max_nodes)
  message: a component ceiling is configured
";

    const EQUIVALENT_EXPRESSION: &str = r#"{
      "deny": (size(input.graph.spec.nodes) > 1
                 ? ["this build declares more than one component"] : [])
            + (has(input.settings.max_nodes)
                 ? ["a component ceiling is configured"] : []),
      "warn": size(input.graph.spec.nodes) > 2
                ? ["this build declares more than two components"] : [],
    }"#;

    #[test]
    fn a_rules_document_reaches_the_decisions_its_rules_name() {
        let out = evaluate(
            &[Source {
                path: "policy.yaml",
                content: "\
- key: deny
  expression: size(input.graph.spec.nodes) > 1
  message: more than one component
- key: warn
  expression: size(input.graph.spec.nodes) > 5
  message: more than five components
- key: audit
  expression: \"true\"
  message: this build was examined
",
            }],
            &graph(&["a", "b"]),
            &Translation::conventional(),
        )
        .expect("a well-formed rules document evaluates");
        let seen: Vec<(&str, &str)> = out
            .iter()
            .map(|d| (d.severity.as_str(), d.message.as_str()))
            .collect();
        assert_eq!(
            seen,
            vec![
                (SEVERITY_HALT, "more than one component"),
                (SEVERITY_INFO, "this build was examined"),
            ]
        );
    }

    #[test]
    fn a_rules_document_and_the_expression_it_stands_for_decide_identically() {
        // The proof that a rules document is read rather than evaluated by a
        // second path: the decisions are compared as bytes, not by inspection.
        let mut input = graph(&["web", "api", "worker"]);
        input["settings"] = serde_json::json!({ "max_nodes": 4 });

        let from_document = evaluate(
            &[Source {
                path: "policy.yaml",
                content: EQUIVALENT_DOCUMENT,
            }],
            &input,
            &Translation::conventional(),
        )
        .expect("the rules document evaluates");
        let from_expression = evaluate(
            &[Source {
                path: "policy.cel",
                content: EQUIVALENT_EXPRESSION,
            }],
            &input,
            &Translation::conventional(),
        )
        .expect("the expression evaluates");

        let document_bytes =
            serde_json::to_string(&from_document).expect("the decisions serialize");
        let expression_bytes =
            serde_json::to_string(&from_expression).expect("the decisions serialize");
        assert_eq!(document_bytes, expression_bytes);
        assert!(
            !from_document.is_empty(),
            "the comparison is only worth something over decisions that were reached"
        );
    }

    #[test]
    fn the_two_forms_mix_within_one_policy_set() {
        let out = evaluate(
            &[
                Source {
                    path: "a.cel",
                    content: r#"{"deny": ["from the expression"]}"#,
                },
                Source {
                    path: "b.yaml",
                    content: "- key: deny\n  expression: \"true\"\n  message: from the document\n",
                },
            ],
            &graph(&["x"]),
            &Translation::conventional(),
        )
        .expect("both forms evaluate");
        let seen: Vec<&str> = out.iter().map(|d| d.message.as_str()).collect();
        assert_eq!(seen, vec!["from the expression", "from the document"]);
    }

    #[test]
    fn a_rules_document_may_be_named_with_either_suffix() {
        for path in ["policy.yaml", "policy.yml"] {
            let out = evaluate(
                &[Source {
                    path,
                    content: "- key: deny\n  expression: \"true\"\n  message: reached\n",
                }],
                &graph(&["x"]),
                &Translation::conventional(),
            )
            .expect("a rules document evaluates");
            assert_eq!(out.len(), 1, "{path}: {out:?}");
        }
    }

    #[test]
    fn a_file_named_for_neither_form_is_read_as_an_expression() {
        // An attachment whose files carry no recognised suffix is read as
        // expressions, so naming a form is not something an attachment has to do
        // to be read at all.
        let out = evaluate(
            &[Source {
                path: "policy",
                content: r#"{"deny": ["reached"]}"#,
            }],
            &graph(&["x"]),
            &Translation::conventional(),
        )
        .expect("a file named for neither form is an expression");
        assert_eq!(out.len(), 1, "{out:?}");
    }

    #[test]
    fn a_document_in_a_file_named_for_an_expression_is_refused_by_name() {
        // The name decides the form in both directions, so a rules document saved
        // under the wrong name fails loudly rather than being sniffed and read
        // anyway. A document's own outermost value is a map, and so is an
        // expression's, so there is no shape to sniff.
        let err = evaluate(
            &[Source {
                path: "policy.cel",
                content: "- key: deny\n  expression: \"true\"\n  message: reached\n",
            }],
            &graph(&["x"]),
            &Translation::conventional(),
        )
        .expect_err("a rules document under an expression's name is not read as one");
        let text = err.to_string();
        assert!(text.contains("policy.cel"), "{text}");
        assert!(text.contains("not a readable expression"), "{text}");
    }

    #[test]
    fn a_malformed_rules_document_is_refused_by_name_at_the_intake() {
        let err = evaluate(
            &[Source {
                path: "limits.yaml",
                content: "- key: deny\n  expression: \"size(input) >\"\n  message: broken\n",
            }],
            &graph(&["x"]),
            &Translation::conventional(),
        )
        .expect_err("a rule whose expression cannot be read is not a verdict");
        let text = err.to_string();
        assert!(text.contains("limits.yaml"), "{text}");
        assert!(text.contains("rule 1"), "{text}");
        assert!(text.contains("cannot be read"), "{text}");
    }

    #[test]
    fn a_rules_document_message_is_reported_exactly_as_authored() {
        // The operator's own wording, carried through translation, compilation and
        // evaluation, and reported. Each of these characters either ends the
        // generated literal early or changes what it decodes to.
        let authored = "a \"quoted\" word, a back\\slash, and a\nline break";
        let out = evaluate(
            &[Source {
                path: "policy.yaml",
                content: "- key: deny\n  expression: \"true\"\n  \
                          message: \"a \\\"quoted\\\" word, a back\\\\slash, and a\\nline break\"\n",
            }],
            &graph(&["x"]),
            &Translation::conventional(),
        )
        .expect("an awkward message is still a message");
        assert_eq!(out.len(), 1, "{out:?}");
        assert_eq!(out[0].message, authored);
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
