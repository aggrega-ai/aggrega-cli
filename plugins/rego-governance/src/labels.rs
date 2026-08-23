// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//! Turning a policy's label decisions into the label changes the build applies.
//!
//! Attached to shape labels, a policy decides which labels the build's nodes
//! should carry. It collects one decision per label under `labels`:
//!
//! ```rego
//! package tagging
//!
//! import rego.v1
//!
//! labels contains {"node": id, "key": "compliance.example.com/tier", "value": "gold"} if {
//!     some id, node in input.graph.nodes
//!     node.labels["component.aggrega.ai/type"] == "server"
//! }
//! ```
//!
//! A decision names the node it applies to, the label key, and the value the
//! label takes. Adding a key that is already present replaces its value. A
//! decision may instead ask for a key to be dropped:
//!
//! ```rego
//! labels contains {"node": id, "key": "draft.example.com/scratch", "op": "remove"} if {
//!     some id, node in input.graph.nodes
//!     node.labels["draft.example.com/scratch"]
//! }
//! ```
//!
//! # What a policy reads
//!
//! `input.graph.nodes` is the build's nodes keyed by name, each carrying the
//! component it instantiates as `.component`, the version resolved for it as
//! `.resolved_version`, and its current labels as `.labels`.
//! `input.compilation_context` carries the deployment's name and its
//! environment, so a policy can decide differently per environment. The
//! attachment's own files are at `input.sources` and its settings at
//! `input.settings`, and the settings are also the policy's `data` document.

use governancecore::labels::{patch, LabelChange, OP_ADD, OP_REMOVE};
use serde::Deserialize;
use serde_json::value::RawValue;

use crate::engine::CollectedDecision;

/// The decision key a policy collects its label decisions under.
///
/// One definition, in the shared core, reached through this module: it is the key
/// an operator writes, and an authored name has exactly one spelling.
pub use governancecore::labels::DECISION_KEY;

/// One label decision as a policy expresses it.
///
/// A field name the decision does not declare is refused rather than ignored: a
/// misspelled `value` would otherwise read as a decision with no value at all,
/// which is the kind of silent miss a policy author cannot see.
#[derive(Debug, Clone, Deserialize, PartialEq, Eq)]
#[serde(deny_unknown_fields)]
struct Decision {
    /// The name of the node the decision applies to.
    node: String,
    /// The label key the decision is about.
    key: String,
    /// The value the label takes. Required when adding, unused when dropping.
    #[serde(default)]
    value: Option<String>,
    /// Whether the label is added or dropped. Absent means added.
    #[serde(default)]
    op: Option<String>,
}

/// Hands a decision this engine read to the shared change builder.
///
/// The two shapes carry the same four field names and differ only in how they are
/// read: this engine's decisions arrive as a JSON document and are deserialized
/// whole, so the intake belongs to the engine while the change the decision asks
/// for does not.
impl From<Decision> for LabelChange {
    fn from(decision: Decision) -> Self {
        LabelChange {
            node: decision.node,
            key: decision.key,
            value: decision.value,
            op: decision.op,
        }
    }
}

/// Reads a policy's label decisions and returns the label changes the build
/// applies, one per decision, in the order the decisions arrived.
///
/// The order is the policy's: decisions come back in package order and then in
/// the order the collection yields, so identical input yields an identical
/// sequence of changes.
pub fn changes(decisions: &[CollectedDecision]) -> anyhow::Result<Vec<Box<RawValue>>> {
    let mut changes = Vec::with_capacity(decisions.len());
    for collected in decisions {
        let decision: Decision = serde_json::from_value(collected.value.clone()).map_err(|err| {
            anyhow::anyhow!(
                "package {:?} collected a decision under {DECISION_KEY:?} that is not a label decision: {err}. A decision names \"node\", \"key\" and \"value\", and may name \"op\" as {OP_ADD:?} or {OP_REMOVE:?}. Every attached package is read for {DECISION_KEY:?}, so a rule of that name collecting anything else must be renamed.",
                collected.package
            )
        })?;
        changes.push(patch(&LabelChange::from(decision))?);
    }
    Ok(changes)
}

#[cfg(test)]
mod tests {
    use super::*;

    fn decisions(values: serde_json::Value) -> Vec<CollectedDecision> {
        collected_by("data.tagging", values)
    }

    /// The decisions one named package reached, as the evaluator hands them over.
    fn collected_by(package: &str, values: serde_json::Value) -> Vec<CollectedDecision> {
        values
            .as_array()
            .expect("the test supplies a list of decisions")
            .iter()
            .map(|value| CollectedDecision {
                package: package.to_string(),
                value: value.clone(),
            })
            .collect()
    }

    fn documents(changes: &[Box<RawValue>]) -> Vec<serde_json::Value> {
        changes
            .iter()
            .map(|c| serde_json::from_str(c.get()).expect("an emitted change is valid JSON"))
            .collect()
    }

    #[test]
    fn a_decision_becomes_one_label_change() {
        let out = changes(&decisions(serde_json::json!([
            {"node": "main/app", "key": "tier", "value": "gold"}
        ])))
        .expect("the decision is read");
        assert_eq!(
            documents(&out),
            vec![serde_json::json!([
                {"op": "add", "path": "/nodes/main~1app/labels/tier", "value": "gold"}
            ])]
        );
    }

    #[test]
    fn a_label_key_carrying_the_path_separator_addresses_one_label() {
        // The canonical Aggrega label keys carry a "/", so this is the ordinary
        // case rather than an edge case: unescaped it would address a nested
        // location and the change would be refused.
        let out = changes(&decisions(serde_json::json!([
            {"node": "app", "key": "component.aggrega.ai/type", "value": "server"}
        ])))
        .expect("the decision is read");
        assert_eq!(
            documents(&out)[0][0]["path"],
            serde_json::json!("/nodes/app/labels/component.aggrega.ai~1type")
        );
    }

    #[test]
    fn an_escape_character_in_a_name_is_escaped_first() {
        let out = changes(&decisions(serde_json::json!([
            {"node": "a~b", "key": "c/d", "value": "v"}
        ])))
        .expect("the decision is read");
        assert_eq!(
            documents(&out)[0][0]["path"],
            serde_json::json!("/nodes/a~0b/labels/c~1d")
        );
    }

    #[test]
    fn a_decision_can_drop_a_label_and_carries_no_value() {
        let out = changes(&decisions(serde_json::json!([
            {"node": "app", "key": "scratch", "op": "remove"}
        ])))
        .expect("the decision is read");
        let document = &documents(&out)[0][0];
        assert_eq!(document["op"], serde_json::json!("remove"));
        assert!(document.get("value").is_none(), "{document}");
    }

    #[test]
    fn every_emitted_path_addresses_a_label_and_nothing_else() {
        // The confinement is structural: whatever a policy names, the emitted
        // path is rooted at one node's label set. A decision naming something
        // that looks like another location is escaped into a label name.
        let out = changes(&decisions(serde_json::json!([
            {"node": "app", "key": "x", "value": "1"},
            {"node": "../component", "key": "../resolved_version", "value": "2"},
            {"node": "app", "key": "y", "op": "remove"}
        ])))
        .expect("the decisions are read");
        for document in documents(&out) {
            let path = document[0]["path"].as_str().expect("a path is a string");
            let segments: Vec<&str> = path.split('/').collect();
            assert_eq!(
                segments.len(),
                5,
                "a change addresses exactly /nodes/<node>/labels/<key>: {path}"
            );
            assert_eq!(segments[1], "nodes", "{path}");
            assert_eq!(segments[3], "labels", "{path}");
        }
    }

    #[test]
    fn a_decision_that_is_not_a_label_decision_is_refused() {
        let err = changes(&decisions(serde_json::json!(["just a string"])))
            .expect_err("a decision of the wrong shape is refused");
        assert!(err.to_string().contains("labels"), "{err}");
    }

    #[test]
    fn a_decision_of_the_wrong_shape_names_the_package_that_collected_it() {
        // A rule sharing the decision key's name in an unrelated helper package
        // fails the whole attachment. Without the package name the message reads
        // as a malformed label decision, which sends the reader to inspect the
        // decisions they meant to write rather than the rule that collided.
        let err = changes(&collected_by(
            "data.helpers",
            serde_json::json!(["a helper's own string"]),
        ))
        .expect_err("a decision of the wrong shape is refused");
        let message = err.to_string();
        assert!(
            message.contains("data.helpers"),
            "the diagnostic names the offending package: {message}"
        );
        assert!(
            message.contains("renamed"),
            "the diagnostic says what to do about a colliding rule name: {message}"
        );
    }

    #[test]
    fn adding_a_label_with_no_value_is_refused() {
        let err = changes(&decisions(serde_json::json!([{"node": "app", "key": "x"}])))
            .expect_err("adding with no value is refused");
        assert!(err.to_string().contains("value"), "{err}");
    }

    #[test]
    fn a_non_string_value_is_refused_rather_than_stringified() {
        let err = changes(&decisions(
            serde_json::json!([{"node": "app", "key": "x", "value": 3}]),
        ))
        .expect_err("a value that is not a string is refused");
        assert!(err.to_string().contains("labels"), "{err}");
    }

    #[test]
    fn an_unknown_operation_is_refused() {
        let err = changes(&decisions(
            serde_json::json!([{"node": "app", "key": "x", "op": "replace"}]),
        ))
        .expect_err("an operation outside the two is refused");
        let message = err.to_string();
        assert!(message.contains("replace"), "{message}");
        assert!(message.contains("add"), "{message}");
    }

    #[test]
    fn decisions_keep_the_order_they_arrived_in() {
        let out = changes(&decisions(serde_json::json!([
            {"node": "b", "key": "k", "value": "1"},
            {"node": "a", "key": "k", "value": "2"}
        ])))
        .expect("the decisions are read");
        let paths: Vec<String> = documents(&out)
            .iter()
            .map(|d| d[0]["path"].as_str().expect("a path").to_string())
            .collect();
        assert_eq!(
            paths,
            vec!["/nodes/b/labels/k", "/nodes/a/labels/k"],
            "the policy's order is preserved rather than re-sorted"
        );
    }

    #[test]
    fn reading_decisions_is_repeatable() {
        let input = decisions(serde_json::json!([
            {"node": "app", "key": "a/b", "value": "1"},
            {"node": "app", "key": "c", "op": "remove"}
        ]));
        let first = documents(&changes(&input).expect("read"));
        let second = documents(&changes(&input).expect("read"));
        assert_eq!(first, second);
    }
}
