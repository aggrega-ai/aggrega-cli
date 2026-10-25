// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//! What a decided annotation must name before the build can apply it.
//!
//! A plugin attached to inject annotations reports which annotations the build's
//! components should carry. A decided annotation names the component it applies
//! to — by the boundary it sits under and its own name, the coordinate each target
//! already carries — plus the annotation key and its value. The rules below are the
//! same whichever language expressed the decision, so they live here rather than in
//! each plugin that reaches them.
//!
//! # A decision may only add an annotation to a component that exists
//!
//! A decided annotation carries a coordinate, a key and a value — a string, or an
//! object of subkeys — and there is no other field it could carry, so a decision
//! has no way to express any other change. A decision naming a component the
//! payload does not carry, or carrying a value of any other shape, is refused by
//! the build, loudly, naming the coordinate and the annotation. The value's shape
//! is deliberately not checked here: the build reads it exactly as it reads the
//! same value written in the Release, which is the one statement of what an
//! annotation value may be. An operator attaching a plugin to inject annotations
//! has declared that injection and nothing further.

use aggrega_abi::AnnotationInjection;

/// The decision key a policy collects its annotation decisions under.
///
/// One name across every governance plugin, so a policy set moved between
/// languages collects its annotation decisions under the same key in both.
pub const DECISION_KEY: &str = "annotations";

/// Requires a decided annotation to name the two things the build cannot supply
/// for it.
///
/// An empty annotation key or an empty component name is refused rather than
/// carried through: neither is something a decision meant to ask for, and both
/// would reach the build as an injection it could not attribute. The boundary
/// path is deliberately not checked, because a component sitting directly under a
/// boundary named by the empty string is a coordinate the build itself can
/// supply — so it is the build's own target set that decides whether a coordinate
/// exists, rather than this check.
pub fn check(injection: &AnnotationInjection) -> anyhow::Result<()> {
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
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;

    fn injection(boundary: &str, component: &str, key: &str, value: &str) -> AnnotationInjection {
        AnnotationInjection {
            boundary_path: boundary.to_string(),
            component_name: component.to_string(),
            annotation_key: key.to_string(),
            annotation_value: serde_json::value::to_raw_value(value).expect("a string encodes"),
        }
    }

    #[test]
    fn a_complete_decision_is_accepted() {
        check(&injection(
            "main",
            "app",
            "backup.example.com/schedule",
            "nightly",
        ))
        .expect("a complete decision is accepted");
    }

    #[test]
    fn an_empty_annotation_key_is_refused() {
        let err = check(&injection("main", "app", "", "v")).expect_err("an empty key is refused");
        assert!(err.to_string().contains("annotation_key"), "{err}");
    }

    #[test]
    fn an_empty_component_name_is_refused() {
        let err = check(&injection("main", "", "k", "v"))
            .expect_err("an empty component name is refused");
        assert!(err.to_string().contains("component_name"), "{err}");
    }

    #[test]
    fn an_empty_boundary_path_is_accepted_because_a_target_may_carry_one() {
        // A component directly under a boundary named by the empty string is a
        // coordinate the build can supply, so it is the build's target set that
        // decides whether it exists rather than this check.
        check(&injection("", "app", "k", "v")).expect("an empty boundary path is accepted");
    }

    #[test]
    fn an_empty_annotation_value_is_accepted_because_it_is_a_value_an_author_can_mean() {
        check(&injection("main", "app", "k", "")).expect("an empty value is accepted");
    }
}
