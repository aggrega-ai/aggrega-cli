// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

//! Reading a policy's annotation decisions, and the order they are reported in.
//!
//! Attached to inject annotations, a policy decides which annotations the build's
//! components should carry. It reports them the same way it reports a verdict — as
//! the value one decision key collects — except that what this key collects is a
//! decision rather than a message:
//!
//! ```cel
//! {
//!   "annotations": input.targets
//!     .filter(t, "backup.example.com/schedule" in input.binding_keys)
//!     .map(t, {
//!       "boundary_path": t.boundary_path,
//!       "component_name": t.component_name,
//!       "annotation_key": "backup.example.com/schedule",
//!       "annotation_value": "nightly",
//!     }),
//! }
//! ```
//!
//! A decision names the component it applies to — by the boundary it sits under
//! and its own name, the coordinate each target already carries — plus the
//! annotation key and its value. The names a decision uses are the names it read
//! its targets under, so the whole convention is learnable from the input.
//!
//! The value is what an author could write on that annotation: a string, or a
//! map of subkeys, which feeds a binding that routes several subkeys at once —
//! `"annotation_value": {"interval": "30s", "port": "metrics", "path": "/metrics"}`.
//! It is carried to the build as the expression produced it, with a map's keys in
//! sorted order, and the build is what judges its shape: it reads the value
//! exactly as it reads the same value written in the Release, and refuses one of
//! any other shape, naming the injection.
//!
//! One decision needs no list around it, and a file that names no `annotations`
//! key decides no annotations — so one policy set can serve this role and a
//! verdict role from the same files, each role reading only the key it is about.
//!
//! # What a policy reads
//!
//! `input.targets` is every component the policy may annotate, each carrying its
//! `.boundary_path`, its `.component_name` and the `.annotations` it already has —
//! each as the author wrote it, a string, a map of subkeys, or the null of an
//! opt-out — so a policy can decide what to add without displacing a value someone
//! authored.
//! `input.binding_keys` is the annotation keys the Manifest has registered a
//! meaning for: an annotation under one of those keys goes on to expand into an
//! attached feature, so a policy reads the list to inject keys that will take
//! effect. The attachment's own files are at `input.sources` and its settings at
//! `input.settings`.
//!
//! # Ordering: a total order over the decisions, computed here
//!
//! The decisions are reported in ascending order of what they name — the boundary
//! first, then the component, then the annotation key, then its value. That is a
//! total order over what a decision can express, so identical input yields an
//! identical sequence of injections.
//!
//! It is computed here rather than taken from the policy because there is no order
//! to take: a policy that walks a map to reach its targets walks it in an order the
//! evaluator does not fix. An injected annotation can expand into an attached
//! feature and so reaches the rendered output, which is why an arbitrary order
//! would mean the same input rendering differently from one build to the next.
//!
//! Two decisions naming one component and one annotation key with different values
//! are both reported, in ascending order of value, compared as the JSON each is
//! carried as. The build applies them in the order they are reported, so the last
//! of them is the value the annotation ends up with — and because the order is the
//! values' own, which value that is does not depend on how the policy was written
//! out.

use aggrega_abi::AnnotationInjection;
use governancecore::annotations::{check, DECISION_KEY};

use crate::engine::{CollectedDecision, DecisionShape};

/// The field names an annotation decision may name.
const BOUNDARY_PATH: &str = "boundary_path";
const COMPONENT_NAME: &str = "component_name";
const ANNOTATION_KEY: &str = "annotation_key";
const ANNOTATION_VALUE: &str = "annotation_value";

/// What an annotation decision names, and the sentence every refusal under this
/// key ends with.
pub const SHAPE: DecisionShape = DecisionShape {
    key: DECISION_KEY,
    fields: &[
        BOUNDARY_PATH,
        COMPONENT_NAME,
        ANNOTATION_KEY,
        ANNOTATION_VALUE,
    ],
    summary: "An annotation decision names \"boundary_path\", \"component_name\", \
              \"annotation_key\" and \"annotation_value\".",
};

/// Reads a policy's annotation decisions and returns the annotations the build
/// adds, in the total order this module documents.
///
/// A decision is read straight into the shape the build applies, so the field
/// names a policy writes are the field names the build reads and there is no
/// second spelling of them to keep in step.
pub fn injections(decisions: &[CollectedDecision]) -> anyhow::Result<Vec<AnnotationInjection>> {
    let mut read: Vec<AnnotationInjection> = decisions
        .iter()
        .map(decision)
        .collect::<anyhow::Result<Vec<AnnotationInjection>>>()?;
    read.sort_by(|left, right| order(left).cmp(&order(right)));
    Ok(read)
}

/// Reads one decision into the injection it asks for.
///
/// Every field is required rather than defaulted, because an annotation with an
/// empty key or an empty coordinate is not something a policy meant to ask for.
/// The boundary a component sits under can legitimately be named by the empty
/// string, so it is required to be written rather than required to be non-empty.
///
/// The value is carried as the JSON of what the expression produced, whatever its
/// shape: the build reads it exactly as it reads the same value written in the
/// Release, and is what refuses a shape no annotation takes, so judging it here
/// would be a second statement of that rule.
fn decision(collected: &CollectedDecision) -> anyhow::Result<AnnotationInjection> {
    collected.admit_fields(&SHAPE)?;
    let injection = AnnotationInjection {
        boundary_path: collected.text(&SHAPE, BOUNDARY_PATH)?,
        component_name: collected.text(&SHAPE, COMPONENT_NAME)?,
        annotation_key: collected.text(&SHAPE, ANNOTATION_KEY)?,
        annotation_value: serde_json::value::to_raw_value(
            &collected.json(&SHAPE, ANNOTATION_VALUE)?,
        )?,
    };
    check(&injection)?;
    Ok(injection)
}

/// What one injection is ordered by: everything a decision can express, in the
/// order a reader would compare them. A value is compared by its JSON, whose map
/// keys are already sorted, so two values compare the same way every build.
fn order(injection: &AnnotationInjection) -> (&str, &str, &str, &str) {
    (
        injection.boundary_path.as_str(),
        injection.component_name.as_str(),
        injection.annotation_key.as_str(),
        injection.annotation_value.get(),
    )
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::engine::decisions;
    use governancecore::sources::Source;

    /// Evaluates one expression against a payload carrying the named components
    /// and returns the injections it decided.
    fn injected(policy: &str, components: &[&str]) -> anyhow::Result<Vec<AnnotationInjection>> {
        let collected = decisions(
            &[Source {
                path: "policy.cel",
                content: policy,
            }],
            &targets(components),
            &SHAPE,
        )?;
        injections(&collected)
    }

    /// The annotation-transform payload for the named components, built from the
    /// payload's own type so a field added to the wire shape does not leave this
    /// fixture describing a payload the guest would reject.
    fn targets(components: &[&str]) -> serde_json::Value {
        let mut input = aggrega_abi::AnnotationTransformInput {
            binding_keys: vec!["gate.example.com/tier".to_string()],
            ..Default::default()
        };
        for name in components {
            input.targets.push(aggrega_abi::AnnotationTarget {
                boundary_path: "main".to_string(),
                component_name: (*name).to_string(),
                ..Default::default()
            });
        }
        let mut payload = serde_json::to_value(&input).expect("the payload serializes");
        payload["sources"] = serde_json::json!([]);
        payload["settings"] = serde_json::json!({});
        payload
    }

    /// Each injection as one line, its value written as the text it carries when
    /// it is a string and as its JSON otherwise.
    fn coordinates(injected: &[AnnotationInjection]) -> Vec<String> {
        injected
            .iter()
            .map(|i| {
                let value = serde_json::from_str::<String>(i.annotation_value.get())
                    .unwrap_or_else(|_| i.annotation_value.get().to_string());
                format!(
                    "{}|{}|{}|{}",
                    i.boundary_path, i.component_name, i.annotation_key, value
                )
            })
            .collect()
    }

    #[test]
    fn a_decision_becomes_one_annotation() {
        let out = injected(
            r#"{"annotations": [{"boundary_path": "main", "component_name": "app",
                 "annotation_key": "gate.example.com/tier", "annotation_value": "gold"}]}"#,
            &["app"],
        )
        .expect("the decision is read");
        assert_eq!(
            coordinates(&out),
            vec!["main|app|gate.example.com/tier|gold"]
        );
    }

    #[test]
    fn one_decision_needs_no_list_around_it() {
        let out = injected(
            r#"{"annotations": {"boundary_path": "main", "component_name": "app",
                 "annotation_key": "k", "annotation_value": "v"}}"#,
            &["app"],
        )
        .expect("a single decision is read");
        assert_eq!(out.len(), 1, "{out:?}");
    }

    #[test]
    fn a_file_that_names_no_annotation_decisions_decides_nothing() {
        assert!(
            injected(r#"{"deny": ["a verdict, not an annotation"]}"#, &["app"])
                .expect("a file about something else decides no annotations")
                .is_empty()
        );
        assert!(injected(r#"{"annotations": null}"#, &["app"])
            .expect("a key named as null decides nothing")
            .is_empty());
    }

    #[test]
    fn a_decision_computed_over_the_targets_reads_the_registered_keys() {
        let out = injected(
            r#"{"annotations": input.targets
                  .filter(t, "gate.example.com/tier" in input.binding_keys)
                  .map(t, {
                    "boundary_path": t.boundary_path,
                    "component_name": t.component_name,
                    "annotation_key": "gate.example.com/tier",
                    "annotation_value": "gold",
                  })}"#,
            &["web", "api"],
        )
        .expect("the decisions are read");
        assert_eq!(
            coordinates(&out),
            vec![
                "main|api|gate.example.com/tier|gold",
                "main|web|gate.example.com/tier|gold",
            ]
        );
    }

    #[test]
    fn a_key_the_deployment_has_not_registered_reaches_no_decision() {
        // The negative half of the same read: a policy filtering on the registered
        // keys decides nothing when the key it wants is not among them.
        let out = injected(
            r#"{"annotations": input.targets
                  .filter(t, "never.example.com/registered" in input.binding_keys)
                  .map(t, {
                    "boundary_path": t.boundary_path,
                    "component_name": t.component_name,
                    "annotation_key": "never.example.com/registered",
                    "annotation_value": "x",
                  })}"#,
            &["web"],
        )
        .expect("the decisions are read");
        assert!(out.is_empty(), "{out:?}");
    }

    #[test]
    fn the_reported_order_is_ascending_over_what_a_decision_names() {
        let out = injected(
            r#"{"annotations": [
                 {"boundary_path": "main", "component_name": "web", "annotation_key": "b", "annotation_value": "1"},
                 {"boundary_path": "main", "component_name": "api", "annotation_key": "z", "annotation_value": "1"},
                 {"boundary_path": "edge", "component_name": "web", "annotation_key": "a", "annotation_value": "1"},
                 {"boundary_path": "main", "component_name": "web", "annotation_key": "a", "annotation_value": "zulu"},
                 {"boundary_path": "main", "component_name": "web", "annotation_key": "a", "annotation_value": "alpha"}
               ]}"#,
            &["web", "api"],
        )
        .expect("the decisions are read");
        assert_eq!(
            coordinates(&out),
            vec![
                "edge|web|a|1",
                "main|api|z|1",
                "main|web|a|alpha",
                "main|web|a|zulu",
                "main|web|b|1",
            ]
        );
    }

    #[test]
    fn a_decision_that_is_not_an_object_is_refused_by_name() {
        let err = injected(r#"{"annotations": ["just a string"]}"#, &["app"])
            .expect_err("a decision of the wrong shape is refused");
        let message = err.to_string();
        assert!(message.contains("policy.cel"), "{message}");
        assert!(message.contains("type string"), "{message}");
        assert!(message.contains("\"annotation_key\""), "{message}");
    }

    #[test]
    fn a_decision_missing_a_coordinate_is_refused_rather_than_defaulted() {
        let err = injected(
            r#"{"annotations": [{"component_name": "app", "annotation_key": "k", "annotation_value": "v"}]}"#,
            &["app"],
        )
        .expect_err("a decision with no boundary is refused");
        assert!(err.to_string().contains("\"boundary_path\""), "{err}");
    }

    #[test]
    fn a_misspelled_field_is_refused_rather_than_ignored() {
        let err = injected(
            r#"{"annotations": [{"boundary_path": "main", "component_name": "app",
                 "annotation_key": "k", "annotation_value": "v", "annotation_values": "v"}]}"#,
            &["app"],
        )
        .expect_err("an undefined field is refused");
        assert!(err.to_string().contains("\"annotation_values\""), "{err}");
    }

    #[test]
    fn a_map_value_is_carried_as_its_json_with_its_keys_sorted() {
        let out = injected(
            r#"{"annotations": [{"boundary_path": "main", "component_name": "app",
                 "annotation_key": "feature.aggrega.ai/monitoring",
                 "annotation_value": {"port": "metrics", "interval": "30s", "path": "/metrics",
                                      "tls": {"z": true, "a": 1}}}]}"#,
            &["app"],
        )
        .expect("a map value is read");
        assert_eq!(
            out[0].annotation_value.get(),
            r#"{"interval":"30s","path":"/metrics","port":"metrics","tls":{"a":1,"z":true}}"#
        );
    }

    #[test]
    fn a_value_of_another_shape_is_passed_to_the_build_rather_than_stringified() {
        // The build is what judges a value's shape, and it refuses a number naming
        // the injection; here it must arrive as the number the expression produced,
        // never as text the expression did not produce.
        let out = injected(
            r#"{"annotations": [{"boundary_path": "main", "component_name": "app",
                 "annotation_key": "k", "annotation_value": 3}]}"#,
            &["app"],
        )
        .expect("the decision is read");
        assert_eq!(out[0].annotation_value.get(), "3");
    }

    #[test]
    fn a_value_with_no_json_form_is_refused_rather_than_coerced() {
        let err = injected(
            r#"{"annotations": [{"boundary_path": "main", "component_name": "app",
                 "annotation_key": "k", "annotation_value": {"at": b"raw"}}]}"#,
            &["app"],
        )
        .expect_err("bytes have no JSON form");
        assert!(err.to_string().contains("\"annotation_value\""), "{err}");
    }

    #[test]
    fn a_decision_with_no_value_is_refused() {
        let err = injected(
            r#"{"annotations": [{"boundary_path": "main", "component_name": "app",
                 "annotation_key": "k", "annotation_value": null}]}"#,
            &["app"],
        )
        .expect_err("a value named as null is refused");
        assert!(err.to_string().contains("\"annotation_value\""), "{err}");
    }

    #[test]
    fn an_empty_annotation_key_is_refused() {
        let err = injected(
            r#"{"annotations": [{"boundary_path": "main", "component_name": "app",
                 "annotation_key": "", "annotation_value": "v"}]}"#,
            &["app"],
        )
        .expect_err("an empty key is refused");
        assert!(err.to_string().contains("annotation_key"), "{err}");
    }

    #[test]
    fn a_component_directly_under_the_root_boundary_is_accepted() {
        let out = injected(
            r#"{"annotations": [{"boundary_path": "", "component_name": "app",
                 "annotation_key": "k", "annotation_value": "v"}]}"#,
            &["app"],
        )
        .expect("an empty boundary path is a coordinate the build can supply");
        assert_eq!(out[0].boundary_path, "");
    }

    #[test]
    fn reading_the_same_decisions_is_repeatable() {
        let policy = r#"{"annotations": input.targets.map(t, {
             "boundary_path": t.boundary_path,
             "component_name": t.component_name,
             "annotation_key": "gate.example.com/tier",
             "annotation_value": "gold",
           })}"#;
        let components = ["delta", "alpha", "charlie", "bravo"];
        let first = coordinates(&injected(policy, &components).expect("read"));
        let second = coordinates(&injected(policy, &components).expect("read"));
        assert_eq!(first, second);
        assert_eq!(first.len(), 4, "{first:?}");
    }
}
