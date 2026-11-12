// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

//! Reading a policy's label decisions, and the order they are reported in.
//!
//! Attached to shape labels, a policy decides which labels the build's nodes
//! should carry. It reports them the same way it reports a verdict — as the value
//! one decision key collects — except that what this key collects is a decision
//! rather than a message:
//!
//! ```cel
//! {
//!   "labels": input.graph.nodes.map(n, {
//!     "node": n,
//!     "key": "compliance.example.com/tier",
//!     "value": input.compilation_context.environment == "production" ? "gold" : "bronze",
//!   }),
//! }
//! ```
//!
//! A decision names the node it applies to, the label key, and the value the
//! label takes. Adding a key that is already present replaces its value. A
//! decision may instead ask for a key to be dropped:
//!
//! ```cel
//! {
//!   "labels": input.graph.nodes
//!               .filter(n, "draft.example.com/scratch" in input.graph.nodes[n].labels)
//!               .map(n, {"node": n, "key": "draft.example.com/scratch", "op": "remove"}),
//! }
//! ```
//!
//! One decision needs no list around it, and a file that names no `labels` key
//! decides no labels — so one policy set can serve this role and a verdict role
//! from the same files, each role reading only the key it is about.
//!
//! # What a policy reads
//!
//! `input.graph.nodes` is the build's nodes keyed by name, each carrying the
//! component it instantiates as `.component`, the version resolved for it as
//! `.resolved_version`, and its current labels as `.labels`.
//! `input.compilation_context` carries the Release's name and its environment,
//! so a policy can decide differently per environment. The attachment's own files
//! are at `input.sources` and its settings at `input.settings`.
//!
//! Reading a label a node may not carry is a failure rather than an empty value,
//! which is why the second example asks whether the key is present before reading
//! it. That is the expression language's own rule about reading an absent key, and
//! it is loud: a policy that reaches for a label no node carries reports that it
//! could not be evaluated instead of quietly deciding nothing.
//!
//! # Ordering: a total order over the decisions, computed here
//!
//! The decisions are reported in ascending order of what they name — the node
//! first, then the label key, then whether the label is added or dropped, then the
//! value. That is a total order over what a decision can express, so identical
//! input yields an identical sequence of changes.
//!
//! It is computed here rather than taken from the policy because there is no order
//! to take: the idiom above walks the build's nodes, and a comprehension over a
//! map walks it in an order the evaluator does not fix. A change to a build's
//! labels shapes the rendered output, so an arbitrary order would mean the same
//! input rendering differently from one build to the next.
//!
//! Two decisions naming one node and one label key with different values are both
//! reported, in ascending order of value. The build applies changes in the order
//! they are reported, so the last of them is the value the label ends up with —
//! and because the order is the values' own, which value that is does not depend
//! on how the policy was written out.

use governancecore::labels::{operation, patch, LabelChange, DECISION_KEY, OP_ADD};
use serde_json::value::RawValue;

use crate::engine::{CollectedDecision, DecisionShape};

/// The field names a label decision may name.
const NODE: &str = "node";
const KEY: &str = "key";
const VALUE: &str = "value";
const OP: &str = "op";

/// What a label decision names, and the sentence every refusal under this key
/// ends with.
pub const SHAPE: DecisionShape = DecisionShape {
    key: DECISION_KEY,
    fields: &[NODE, KEY, VALUE, OP],
    summary: "A label decision names \"node\", \"key\" and \"value\", and may name \"op\" as \
              \"remove\" to drop the label instead.",
};

/// Reads a policy's label decisions and returns the label changes the build
/// applies, one per decision, in the total order this module documents.
pub fn changes(decisions: &[CollectedDecision]) -> anyhow::Result<Vec<Box<RawValue>>> {
    let mut read: Vec<LabelChange> = decisions
        .iter()
        .map(decision)
        .collect::<anyhow::Result<Vec<LabelChange>>>()?;
    read.sort_by(|left, right| order(left).cmp(&order(right)));
    read.iter().map(patch).collect()
}

/// Reads one decision into the change it asks for.
///
/// The operation is resolved here rather than at emission, so an operation outside
/// the two this surface offers is refused before anything is ordered, and so the
/// ordering compares the operation a decision asks for rather than whether the
/// author happened to write it out.
fn decision(collected: &CollectedDecision) -> anyhow::Result<LabelChange> {
    collected.admit_fields(&SHAPE)?;
    let mut change = LabelChange {
        node: collected.text(&SHAPE, NODE)?,
        key: collected.text(&SHAPE, KEY)?,
        value: collected.optional_text(&SHAPE, VALUE)?,
        op: collected.optional_text(&SHAPE, OP)?,
    };
    change.op = Some(operation(&change)?.to_string());
    Ok(change)
}

/// What one change is ordered by: everything a decision can express, in the order
/// a reader would compare them.
///
/// A decision that drops a label carries no value, and orders as though its value
/// were empty — there is no value to compare and the operation has already
/// separated it from an addition.
fn order(change: &LabelChange) -> (&str, &str, &str, &str) {
    (
        change.node.as_str(),
        change.key.as_str(),
        change.op.as_deref().unwrap_or(OP_ADD),
        change.value.as_deref().unwrap_or(""),
    )
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::engine::decisions;
    use governancecore::sources::Source;

    /// Evaluates one expression against a build of the named nodes and returns
    /// the change documents it decided, so a test asserts on what the build would
    /// apply rather than on an intermediate shape.
    fn applied(policy: &str, nodes: &[&str]) -> anyhow::Result<Vec<serde_json::Value>> {
        let collected = decisions(
            &[Source {
                path: "policy.cel",
                content: policy,
            }],
            &graph(nodes),
            &SHAPE,
        )?;
        Ok(changes(&collected)?
            .iter()
            .map(|change| serde_json::from_str(change.get()).expect("a change is valid JSON"))
            .collect())
    }

    /// The label-transform payload for a build of the named nodes, each carrying
    /// one label, built from the payload's own type so a field added to the wire
    /// shape does not leave this fixture describing a build the guest would
    /// reject.
    fn graph(nodes: &[&str]) -> serde_json::Value {
        let mut input = aggrega_abi::MutatorInput::default();
        for name in nodes {
            let mut node = aggrega_abi::MutatorNode::default();
            node.labels.insert(
                "component.aggrega.ai/type".to_string(),
                "server".to_string(),
            );
            input.graph.nodes.insert((*name).to_string(), node);
        }
        let mut payload = serde_json::to_value(&input).expect("the payload serializes");
        payload["sources"] = serde_json::json!([]);
        payload["settings"] = serde_json::json!({});
        payload
    }

    fn paths(applied: &[serde_json::Value]) -> Vec<String> {
        applied
            .iter()
            .map(|change| change[0]["path"].as_str().expect("a path").to_string())
            .collect()
    }

    #[test]
    fn a_decision_becomes_one_label_change() {
        let out = applied(
            r#"{"labels": [{"node": "app", "key": "tier", "value": "gold"}]}"#,
            &["app"],
        )
        .expect("the decision is read");
        assert_eq!(
            out,
            vec![serde_json::json!([
                {"op": "add", "path": "/nodes/app/labels/tier", "value": "gold"}
            ])]
        );
    }

    #[test]
    fn one_decision_needs_no_list_around_it() {
        let out = applied(
            r#"{"labels": {"node": "app", "key": "tier", "value": "gold"}}"#,
            &["app"],
        )
        .expect("a single decision is read");
        assert_eq!(paths(&out), vec!["/nodes/app/labels/tier"]);
    }

    #[test]
    fn a_file_that_names_no_label_decisions_decides_nothing() {
        assert!(applied(r#"{"deny": ["a verdict, not a label"]}"#, &["app"])
            .expect("a file about something else decides no labels")
            .is_empty());
        assert!(applied(r#"{"labels": null}"#, &["app"])
            .expect("a key named as null decides nothing")
            .is_empty());
    }

    #[test]
    fn a_decision_computed_over_the_build_names_every_node() {
        let out = applied(
            r#"{"labels": input.graph.nodes.map(n, {"node": n, "key": "tier", "value": "gold"})}"#,
            &["web", "api", "db"],
        )
        .expect("the decisions are read");
        assert_eq!(
            paths(&out),
            vec![
                "/nodes/api/labels/tier",
                "/nodes/db/labels/tier",
                "/nodes/web/labels/tier",
            ]
        );
    }

    #[test]
    fn a_decision_can_read_a_label_the_node_carries() {
        let out = applied(
            r#"{"labels": input.graph.nodes
                  .filter(n, "component.aggrega.ai/type" in input.graph.nodes[n].labels)
                  .map(n, {"node": n, "key": "kind", "value": input.graph.nodes[n].labels["component.aggrega.ai/type"]})}"#,
            &["web"],
        )
        .expect("the decisions are read");
        assert_eq!(out[0][0]["value"], serde_json::json!("server"));
    }

    #[test]
    fn the_reported_order_is_ascending_over_what_a_decision_names() {
        // Written out in a deliberately unhelpful order, and reported in the
        // canonical one. The value is the last thing compared, so the two
        // decisions naming one label are adjacent and ascending by value.
        let out = applied(
            r#"{"labels": [
                 {"node": "web", "key": "b", "value": "2"},
                 {"node": "api", "key": "z", "value": "9"},
                 {"node": "web", "key": "a", "value": "zulu"},
                 {"node": "web", "key": "a", "value": "alpha"},
                 {"node": "api", "key": "z", "op": "remove"}
               ]}"#,
            &["web", "api"],
        )
        .expect("the decisions are read");
        let seen: Vec<(String, String)> = out
            .iter()
            .map(|change| {
                (
                    change[0]["path"].as_str().expect("a path").to_string(),
                    change[0]["value"].as_str().unwrap_or("").to_string(),
                )
            })
            .collect();
        assert_eq!(
            seen,
            vec![
                ("/nodes/api/labels/z".to_string(), "9".to_string()),
                ("/nodes/api/labels/z".to_string(), String::new()),
                ("/nodes/web/labels/a".to_string(), "alpha".to_string()),
                ("/nodes/web/labels/a".to_string(), "zulu".to_string()),
                ("/nodes/web/labels/b".to_string(), "2".to_string()),
            ]
        );
    }

    #[test]
    fn a_decision_that_is_not_an_object_is_refused_by_name() {
        let err = applied(r#"{"labels": ["just a string"]}"#, &["app"])
            .expect_err("a decision of the wrong shape is refused");
        let message = err.to_string();
        assert!(message.contains("policy.cel"), "{message}");
        assert!(message.contains("type string"), "{message}");
        assert!(message.contains("\"node\""), "{message}");
    }

    #[test]
    fn a_decision_key_that_is_not_a_collection_is_refused_by_name() {
        let err = applied(r#"{"labels": 3}"#, &["app"])
            .expect_err("a decision key holding a number is refused");
        let message = err.to_string();
        assert!(message.contains("labels"), "{message}");
        assert!(message.contains("type int"), "{message}");
    }

    #[test]
    fn a_decision_missing_a_field_is_refused_rather_than_defaulted() {
        let err = applied(
            r#"{"labels": [{"key": "tier", "value": "gold"}]}"#,
            &["app"],
        )
        .expect_err("a decision with no node is refused");
        assert!(err.to_string().contains("\"node\""), "{err}");
    }

    #[test]
    fn a_misspelled_field_is_refused_rather_than_ignored() {
        // The whole decision would otherwise read as one that left the field out,
        // which is the kind of silent miss an author cannot see.
        let err = applied(
            r#"{"labels": [{"node": "app", "key": "tier", "values": "gold", "nodes": "app"}]}"#,
            &["app"],
        )
        .expect_err("an undefined field is refused");
        let message = err.to_string();
        assert!(message.contains("\"nodes\""), "{message}");
        assert!(message.contains("\"values\""), "{message}");
        assert!(
            message.find("\"nodes\"") < message.find("\"values\""),
            "unrecognised fields are reported in sorted order: {message}"
        );
    }

    #[test]
    fn a_non_string_field_is_refused_rather_than_stringified() {
        let err = applied(
            r#"{"labels": [{"node": "app", "key": "tier", "value": 3}]}"#,
            &["app"],
        )
        .expect_err("a value that is not text is refused");
        let message = err.to_string();
        assert!(message.contains("type int"), "{message}");
        assert!(message.contains("is text"), "{message}");
    }

    #[test]
    fn an_unknown_operation_is_refused() {
        let err = applied(
            r#"{"labels": [{"node": "app", "key": "x", "value": "v", "op": "replace"}]}"#,
            &["app"],
        )
        .expect_err("an operation outside the two is refused");
        assert!(err.to_string().contains("replace"), "{err}");
    }

    #[test]
    fn adding_a_label_with_no_value_is_refused() {
        let err = applied(r#"{"labels": [{"node": "app", "key": "x"}]}"#, &["app"])
            .expect_err("adding with no value is refused");
        assert!(err.to_string().contains("value"), "{err}");
    }

    #[test]
    fn a_label_key_carrying_the_path_separator_addresses_one_label() {
        let out = applied(
            r#"{"labels": [{"node": "app", "key": "component.aggrega.ai/type", "value": "cache"}]}"#,
            &["app"],
        )
        .expect("the decision is read");
        assert_eq!(
            paths(&out),
            vec!["/nodes/app/labels/component.aggrega.ai~1type"]
        );
    }

    #[test]
    fn reading_the_same_decisions_is_repeatable() {
        let policy =
            r#"{"labels": input.graph.nodes.map(n, {"node": n, "key": "tier", "value": "gold"})}"#;
        let nodes = ["delta", "alpha", "charlie", "bravo"];
        let first = applied(policy, &nodes).expect("read");
        let second = applied(policy, &nodes).expect("read");
        assert_eq!(first, second);
        assert_eq!(first.len(), 4, "{first:?}");
    }
}
