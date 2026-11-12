// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

//! Turning a decided label into the label change the build applies.
//!
//! A plugin attached to shape labels reports which labels the build's nodes
//! should carry. A decided label names the node it applies to, the label key, and
//! either the value the label takes or that the key is dropped. This module turns
//! one of those into the change instruction the build reads, and it is the same
//! work whichever language the decision was expressed in — so it lives here, once,
//! rather than in each plugin that reaches it.
//!
//! # Why a decision names labels rather than writing change instructions
//!
//! The change is expressed to the build as a document of edit instructions
//! addressed by path, and those paths need escaping — an Aggrega label key
//! routinely contains a `/`, which is the path separator, so a hand-written path
//! would address a nested location instead of one label. A decision therefore
//! names the label it means and this module addresses it, which is also how the
//! other shipped label-shaping plugins read their configuration. The escaping is
//! then written once, here, rather than in every policy and every plugin.
//!
//! # A decision may only change labels, and it cannot reach further
//!
//! Every instruction this module emits addresses one label on one node, so a
//! decision has no way to express a change to anything else — the node's
//! component, its resolved version, the set of nodes, or a value that is not a
//! string. That confinement is what the invited transformation is bounded to: an
//! operator attaching a plugin to shape labels has declared exactly that, and
//! nothing more. A decision that names a node the build does not have, or asks to
//! drop a label the node does not carry, is refused by the build with a message
//! naming the instruction — it is not silently ignored.

use serde_json::value::RawValue;

/// The decision key a policy collects its label decisions under.
///
/// One name across every governance plugin, so a policy set moved between
/// languages collects its label decisions under the same key in both.
pub const DECISION_KEY: &str = "labels";

/// Adding a label, or replacing the value of one already present. The operation a
/// decision that names none asks for.
pub const OP_ADD: &str = "add";

/// Dropping a label the node carries.
pub const OP_REMOVE: &str = "remove";

/// One decided label, exactly as a policy expressed it.
///
/// The operation and the value are optional because a decision may leave both
/// implicit: naming no operation asks for the label to be added, and dropping a
/// label needs no value. Which combinations are usable is decided by
/// [`operation`] and [`patch`] rather than by this shape, so a decision is read
/// into it before it is judged and the refusal can name what was actually
/// written.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct LabelChange {
    /// The name of the node the decision applies to.
    pub node: String,
    /// The label key the decision is about.
    pub key: String,
    /// The value the label takes. Required when adding, unused when dropping.
    pub value: Option<String>,
    /// Whether the label is added or dropped. Absent means added.
    pub op: Option<String>,
}

/// One edit instruction, as the build reads it.
#[derive(Debug, Clone, serde::Serialize)]
struct Instruction {
    op: String,
    path: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    value: Option<String>,
}

/// The operation a decision asks for, refusing anything outside the two.
///
/// A decision naming no operation asks for the label to be added, which is the
/// common case and the one a policy leaves implicit. An operation outside the two
/// is refused rather than treated as an addition: a decision asking for something
/// this surface cannot express is a mistake to report, not one to reinterpret.
pub fn operation(change: &LabelChange) -> anyhow::Result<&str> {
    match change.op.as_deref().unwrap_or(OP_ADD) {
        OP_ADD => Ok(OP_ADD),
        OP_REMOVE => Ok(OP_REMOVE),
        other => anyhow::bail!(
            "the {DECISION_KEY:?} decision for node {:?} label {:?} names \"op\" {other:?}, which is not one of {OP_ADD:?} or {OP_REMOVE:?}",
            change.node,
            change.key
        ),
    }
}

/// Builds the change instruction one decided label asks for.
///
/// Adding a label with no value is refused rather than defaulted to the empty
/// string: a label whose value nobody wrote is not something a policy meant to
/// ask for, and the empty string is a value an author can write deliberately.
pub fn patch(change: &LabelChange) -> anyhow::Result<Box<RawValue>> {
    let op = operation(change)?;
    let value = match op {
        OP_REMOVE => None,
        _ => Some(change.value.clone().ok_or_else(|| {
            anyhow::anyhow!(
                "the {DECISION_KEY:?} decision for node {:?} label {:?} names no \"value\"; a label value is a string",
                change.node,
                change.key
            )
        })?),
    };
    let instruction = Instruction {
        op: op.to_string(),
        path: format!(
            "/nodes/{}/labels/{}",
            escape(&change.node),
            escape(&change.key)
        ),
        value,
    };
    let document = serde_json::to_string(&[instruction])?;
    Ok(RawValue::from_string(document)?)
}

/// Escapes one path segment so a name containing the path separator addresses a
/// single location rather than a nested one.
///
/// A label key such as `component.aggrega.ai/type` carries a `/`, and a node's
/// name can too, so escaping is what keeps a decision about one label from
/// addressing something else entirely. The escape character is itself escaped
/// first, so a separator rewritten into an escape sequence is not escaped twice.
fn escape(segment: &str) -> String {
    segment.replace('~', "~0").replace('/', "~1")
}

#[cfg(test)]
mod tests {
    use super::*;

    fn change(node: &str, key: &str, value: Option<&str>, op: Option<&str>) -> LabelChange {
        LabelChange {
            node: node.to_string(),
            key: key.to_string(),
            value: value.map(str::to_string),
            op: op.map(str::to_string),
        }
    }

    fn document(change: &LabelChange) -> serde_json::Value {
        let emitted = patch(change).expect("the decision is read");
        serde_json::from_str(emitted.get()).expect("an emitted change is valid JSON")
    }

    #[test]
    fn a_decision_becomes_one_label_change() {
        assert_eq!(
            document(&change("main/app", "tier", Some("gold"), None)),
            serde_json::json!([
                {"op": "add", "path": "/nodes/main~1app/labels/tier", "value": "gold"}
            ])
        );
    }

    #[test]
    fn a_label_key_carrying_the_path_separator_addresses_one_label() {
        // The canonical Aggrega label keys carry a "/", so this is the ordinary
        // case rather than an edge case: unescaped it would address a nested
        // location and the change would be refused.
        assert_eq!(
            document(&change(
                "app",
                "component.aggrega.ai/type",
                Some("server"),
                None
            ))[0]["path"],
            serde_json::json!("/nodes/app/labels/component.aggrega.ai~1type")
        );
    }

    #[test]
    fn an_escape_character_in_a_name_is_escaped_first() {
        assert_eq!(
            document(&change("a~b", "c/d", Some("v"), None))[0]["path"],
            serde_json::json!("/nodes/a~0b/labels/c~1d")
        );
    }

    #[test]
    fn a_decision_can_drop_a_label_and_carries_no_value() {
        let emitted = document(&change("app", "scratch", None, Some(OP_REMOVE)));
        assert_eq!(emitted[0]["op"], serde_json::json!("remove"));
        assert!(emitted[0].get("value").is_none(), "{emitted}");
    }

    #[test]
    fn a_dropped_label_carries_no_value_even_when_the_decision_named_one() {
        // Dropping a label has no value to write, so a value named alongside it
        // is not carried into the instruction rather than being refused: the
        // decision is unambiguous about what it asks for.
        let emitted = document(&change("app", "scratch", Some("ignored"), Some(OP_REMOVE)));
        assert!(emitted[0].get("value").is_none(), "{emitted}");
    }

    #[test]
    fn every_emitted_path_addresses_a_label_and_nothing_else() {
        // The confinement is structural: whatever a decision names, the emitted
        // path is rooted at one node's label set. A decision naming something
        // that looks like another location is escaped into a label name.
        for one in [
            change("app", "x", Some("1"), None),
            change("../component", "../resolved_version", Some("2"), None),
            change("app", "y", None, Some(OP_REMOVE)),
        ] {
            let emitted = document(&one);
            let path = emitted[0]["path"].as_str().expect("a path is a string");
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
    fn adding_a_label_with_no_value_is_refused() {
        let err =
            patch(&change("app", "x", None, None)).expect_err("adding with no value is refused");
        assert!(err.to_string().contains("value"), "{err}");
    }

    #[test]
    fn an_unknown_operation_is_refused() {
        let err = patch(&change("app", "x", Some("v"), Some("replace")))
            .expect_err("an operation outside the two is refused");
        let message = err.to_string();
        assert!(message.contains("replace"), "{message}");
        assert!(message.contains("add"), "{message}");
        assert!(message.contains("remove"), "{message}");
    }

    #[test]
    fn the_operation_a_decision_leaves_implicit_is_adding() {
        assert_eq!(
            operation(&change("app", "x", Some("v"), None)).expect("the operation is read"),
            OP_ADD
        );
        assert_eq!(
            operation(&change("app", "x", None, Some(OP_REMOVE))).expect("the operation is read"),
            OP_REMOVE
        );
    }

    #[test]
    fn reading_a_decision_is_repeatable() {
        let one = change("app", "a/b", Some("1"), None);
        assert_eq!(document(&one), document(&one));
    }
}
