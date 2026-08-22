// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//! Turning a policy's annotation decisions into the annotations the build adds.
//!
//! Attached to inject annotations, a policy decides which annotations the
//! deployment's components should carry. It collects one decision per annotation
//! under `annotations`:
//!
//! ```rego
//! package tagging
//!
//! import rego.v1
//!
//! annotations contains {
//!     "boundary_path": target.boundary_path,
//!     "component_name": target.component_name,
//!     "annotation_key": "backup.example.com/schedule",
//!     "annotation_value": "nightly",
//! } if {
//!     some target in input.targets
//!     not target.annotations["backup.example.com/schedule"]
//! }
//! ```
//!
//! A decision names the component it applies to — by the boundary it sits under
//! and its own name, the coordinate each target already carries — plus the
//! annotation key and its value. The names a decision uses are the names it read
//! its targets under, so the whole convention is learnable from the input.
//!
//! # What a policy reads
//!
//! `input.targets` is every component the policy may annotate, each carrying its
//! `.boundary_path`, its `.component_name` and the `.annotations` it already
//! has — so a policy can decide what to add without displacing a value someone
//! authored. `input.binding_keys` is the annotation keys the deployment has
//! registered a meaning for: an annotation under one of those keys goes on to
//! expand into an attached feature, so a policy reads the list to inject keys
//! that will take effect. The attachment's own files are at `input.sources` and
//! its settings at `input.settings`, and the settings are also the policy's
//! `data` document.
//!
//! # A decision may only add an annotation to a component that exists
//!
//! An annotation decision carries a coordinate, a key and a string value, and
//! there is no other field it could carry — so a policy has no way to express
//! any other change. A decision naming a component the deployment does not have
//! is refused by the build, loudly, naming the coordinate and the annotation. An
//! operator attaching a plugin to inject annotations has declared that
//! injection and nothing further.

use aggrega_abi::AnnotationInjection;

use crate::engine::CollectedDecision;

/// The decision key a policy collects its annotation decisions under.
pub const DECISION_KEY: &str = "annotations";

/// Reads a policy's annotation decisions and returns the annotations the build
/// adds, in the order the decisions arrived.
///
/// A decision is read straight into the shape the build applies, so the field
/// names a policy writes are the field names the build reads and there is no
/// second spelling of them to keep in step. A decision missing one of them is
/// refused rather than defaulted, because an annotation with an empty key or an
/// empty coordinate is not something a policy meant to ask for.
pub fn injections(decisions: &[CollectedDecision]) -> anyhow::Result<Vec<AnnotationInjection>> {
    let mut injections = Vec::with_capacity(decisions.len());
    for collected in decisions {
        let injection: AnnotationInjection = serde_json::from_value(collected.value.clone())
            .map_err(|err| {
                anyhow::anyhow!(
                    "package {:?} collected a decision under {DECISION_KEY:?} that is not an annotation decision: {err}. A decision names \"boundary_path\", \"component_name\", \"annotation_key\" and \"annotation_value\". Every attached package is read for {DECISION_KEY:?}, so a rule of that name collecting anything else must be renamed.",
                    collected.package
                )
            })?;
        if injection.annotation_key.is_empty() {
            anyhow::bail!(
                "the {DECISION_KEY:?} decision for boundary {:?} component {:?} names an empty \"annotation_key\"",
                injection.boundary_path,
                injection.component_name
            );
        }
        if injection.component_name.is_empty() {
            anyhow::bail!(
                "the {DECISION_KEY:?} decision for annotation {:?} names an empty \"component_name\"",
                injection.annotation_key
            );
        }
        injections.push(injection);
    }
    Ok(injections)
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

    fn decision(boundary: &str, component: &str, key: &str, value: &str) -> serde_json::Value {
        serde_json::json!({
            "boundary_path": boundary,
            "component_name": component,
            "annotation_key": key,
            "annotation_value": value,
        })
    }

    #[test]
    fn a_decision_becomes_one_annotation() {
        let out = injections(&decisions(serde_json::json!([decision(
            "main",
            "app",
            "backup.example.com/schedule",
            "nightly"
        )])))
        .expect("the decision is read");
        assert_eq!(out.len(), 1);
        assert_eq!(out[0].boundary_path, "main");
        assert_eq!(out[0].component_name, "app");
        assert_eq!(out[0].annotation_key, "backup.example.com/schedule");
        assert_eq!(out[0].annotation_value, "nightly");
    }

    #[test]
    fn a_nested_boundary_coordinate_is_carried_through_unchanged() {
        let out = injections(&decisions(serde_json::json!([decision(
            "main/edge",
            "app",
            "k",
            "v"
        )])))
        .expect("the decision is read");
        assert_eq!(out[0].boundary_path, "main/edge");
    }

    #[test]
    fn an_empty_boundary_path_is_accepted_because_a_target_may_carry_one() {
        // A component directly under a boundary named by the empty string is a
        // coordinate the build can supply, so it is the build's target set that
        // decides whether it exists rather than this reader.
        let out = injections(&decisions(serde_json::json!([decision(
            "", "app", "k", "v"
        )])))
        .expect("the decision is read");
        assert_eq!(out[0].boundary_path, "");
    }

    #[test]
    fn decisions_keep_the_order_they_arrived_in() {
        let out = injections(&decisions(serde_json::json!([
            decision("main", "b", "k", "1"),
            decision("main", "a", "k", "2"),
        ])))
        .expect("the decisions are read");
        let names: Vec<&str> = out.iter().map(|i| i.component_name.as_str()).collect();
        assert_eq!(
            names,
            vec!["b", "a"],
            "the policy's order is preserved rather than re-sorted"
        );
    }

    #[test]
    fn a_decision_of_the_wrong_shape_is_refused() {
        let err = injections(&decisions(serde_json::json!(["just a string"])))
            .expect_err("a decision of the wrong shape is refused");
        assert!(err.to_string().contains("annotations"), "{err}");
    }

    #[test]
    fn a_decision_of_the_wrong_shape_names_the_package_that_collected_it() {
        // A rule sharing the decision key's name in an unrelated helper package
        // fails the whole attachment. Without the package name the message reads
        // as a malformed annotation decision, which sends the reader to inspect
        // the decisions they meant to write rather than the rule that collided.
        let err = injections(&collected_by(
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
    fn a_decision_missing_a_coordinate_is_refused_rather_than_defaulted() {
        let err = injections(&decisions(serde_json::json!([{
            "component_name": "app",
            "annotation_key": "k",
            "annotation_value": "v",
        }])))
        .expect_err("a decision with no boundary is refused");
        assert!(err.to_string().contains("boundary_path"), "{err}");
    }

    #[test]
    fn a_non_string_annotation_value_is_refused_rather_than_stringified() {
        let err = injections(&decisions(serde_json::json!([{
            "boundary_path": "main",
            "component_name": "app",
            "annotation_key": "k",
            "annotation_value": 3,
        }])))
        .expect_err("a value that is not a string is refused");
        assert!(err.to_string().contains("annotations"), "{err}");
    }

    #[test]
    fn an_empty_annotation_key_is_refused() {
        let err = injections(&decisions(serde_json::json!([decision(
            "main", "app", "", "v"
        )])))
        .expect_err("an empty key is refused");
        assert!(err.to_string().contains("annotation_key"), "{err}");
    }

    #[test]
    fn an_empty_component_name_is_refused() {
        let err = injections(&decisions(serde_json::json!([decision(
            "main", "", "k", "v"
        )])))
        .expect_err("an empty component name is refused");
        assert!(err.to_string().contains("component_name"), "{err}");
    }

    #[test]
    fn reading_decisions_is_repeatable() {
        let input = decisions(serde_json::json!([
            decision("main", "app", "k", "v"),
            decision("main", "db", "k", "w"),
        ]));
        let first = injections(&input).expect("read");
        let second = injections(&input).expect("read");
        let shape = |list: &[AnnotationInjection]| {
            list.iter()
                .map(|i| {
                    format!(
                        "{}|{}|{}|{}",
                        i.boundary_path, i.component_name, i.annotation_key, i.annotation_value
                    )
                })
                .collect::<Vec<_>>()
        };
        assert_eq!(shape(&first), shape(&second));
    }
}
