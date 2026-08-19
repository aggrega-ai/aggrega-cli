// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//! Unit tests for the secret-slot-declares-sensitive rule.
//!
//! Every positive case is paired with the negative that makes it
//! meaningful. A rule that fired on every slot, or on none, would look
//! exactly like correct behaviour in a positive-only suite — so each
//! selector form that pins is tested beside a form that does not, and
//! each firing case is tested beside the same slot with the marker
//! declared.
//!
//! Coverage:
//!  - Every selector form the grammar admits, split into the forms that
//!    pin the kind to secret and the forms that do not.
//!  - The marker present, absent, false, and non-boolean.
//!  - Slots without a selector, entities without slots, and entity kinds
//!    that declare no slots.
//!  - The finding's severity is `warn`, so the rule never halts.
//!  - Both slot-declaring entity kinds.

mod common;

use publishing_linter::rules::secret_slot_declares_sensitive as rule;
use serde_json::{json, Value};

fn entity(kind: &str, slots: Value) -> Value {
    json!({
        "apiVersion": "aggrega.ai/v1alpha1",
        "kind": kind,
        "metadata": {"name": "core/datastore/example", "version": "1.0.0"},
        "spec": {"featuresSchema": slots},
    })
}

fn classify(kind: &str, slots: Value) -> Vec<String> {
    let input = common::fixture(kind, entity(kind, slots), None, None, None);
    rule::classify(&input)
        .into_iter()
        .map(|f| {
            assert_eq!(f.rule_id, "secret-slot-declares-sensitive");
            assert_eq!(
                f.severity, "warn",
                "the rule is advisory; a non-warn severity would halt a pre-publish run"
            );
            f.message
        })
        .collect()
}

fn slot(selector: &str) -> Value {
    json!({"credentials": {"selector": selector, "inject": ["env"]}})
}

/// The selector forms that admit only a secret-kind Feature. Each must
/// fire.
const PINNING: &[&str] = &[
    "feature.aggrega.ai/kind=secret",
    "feature.aggrega.ai/kind = secret",
    "  feature.aggrega.ai/kind=secret  ",
    "feature.aggrega.ai/kind in (secret)",
    "feature.aggrega.ai/kind in ( secret )",
    // A pinning clause among others still pins: clauses combine with AND.
    "feature.aggrega.ai/kind=secret,feature.aggrega.ai/scope=team",
    "feature.aggrega.ai/scope=team,feature.aggrega.ai/kind=secret",
    "feature.aggrega.ai/kind=secret,!feature.aggrega.ai/legacy",
];

/// The selector forms that do not admit only a secret-kind Feature. None
/// may fire — this half is what proves the rule is reading the grammar
/// rather than matching the substring "secret".
const NOT_PINNING: &[&str] = &[
    "",
    "feature.aggrega.ai/kind!=secret",
    "feature.aggrega.ai/kind notin (secret)",
    "!feature.aggrega.ai/kind",
    "feature.aggrega.ai/kind",
    // A set with a second member admits a non-secret fill.
    "feature.aggrega.ai/kind in (secret,tls)",
    "feature.aggrega.ai/kind in (tls,secret)",
    // A different key pinned to the same value.
    "feature.aggrega.ai/flavour=secret",
    // The value is a different one that merely starts the same way.
    "feature.aggrega.ai/kind=secretish",
    // A near-miss key that contains the real one as a suffix.
    "x-feature.aggrega.ai/kind=secret",
];

#[test]
fn pinning_selectors_without_the_marker_are_flagged() {
    for selector in PINNING {
        let findings = classify("Component", slot(selector));
        assert_eq!(
            findings.len(),
            1,
            "selector {selector:?} pins the secret kind and declares no marker; expected one finding, got {findings:?}"
        );
        assert!(
            findings[0].contains("credentials"),
            "the finding must name the slot; got {:?}",
            findings[0]
        );
        assert!(
            findings[0].contains(selector.trim()) || findings[0].contains(selector),
            "the finding must quote the selector; got {:?}",
            findings[0]
        );
    }
}

#[test]
fn non_pinning_selectors_are_silent() {
    for selector in NOT_PINNING {
        let findings = classify("Component", slot(selector));
        assert!(
            findings.is_empty(),
            "selector {selector:?} does not pin the secret kind; expected silence, got {findings:?}"
        );
    }
}

#[test]
fn the_declared_marker_silences_every_pinning_form() {
    for selector in PINNING {
        let slots = json!({"credentials": {"selector": selector, "sensitive": true}});
        let findings = classify("Component", slots);
        assert!(
            findings.is_empty(),
            "selector {selector:?} with the marker declared must be silent; got {findings:?}"
        );
    }
}

#[test]
fn only_the_boolean_true_marker_silences() {
    let cases: &[(&str, Value, bool)] = &[
        ("declared", json!(true), true),
        ("false", json!(false), false),
        ("quoted string", json!("true"), false),
        ("number", json!(1), false),
        ("null", Value::Null, false),
    ];
    for (label, marker, silent) in cases {
        let slots = json!({
            "credentials": {"selector": "feature.aggrega.ai/kind=secret", "sensitive": marker}
        });
        let findings = classify("Component", slots);
        assert_eq!(
            findings.is_empty(),
            *silent,
            "marker {label} ({marker:?}): expected silent={silent}, got {findings:?}"
        );
    }
}

#[test]
fn a_slot_without_a_selector_is_silent() {
    let slots = json!({"credentials": {"inject": ["env"]}});
    assert!(classify("Component", slots).is_empty());
}

#[test]
fn a_non_string_selector_is_silent() {
    let slots = json!({"credentials": {"selector": ["feature.aggrega.ai/kind=secret"]}});
    assert!(classify("Component", slots).is_empty());
}

#[test]
fn both_slot_declaring_kinds_are_read_and_others_are_not() {
    for kind in ["Component", "Feature"] {
        assert_eq!(
            classify(kind, slot("feature.aggrega.ai/kind=secret")).len(),
            1,
            "{kind} declares attachment slots and must be read"
        );
    }
    for kind in ["Recipe", "FeatureRecipe", "Aggregate", "Plugin", "SchemaLibrary"] {
        assert!(
            classify(kind, slot("feature.aggrega.ai/kind=secret")).is_empty(),
            "{kind} declares no attachment slots and must be silent"
        );
    }
}

#[test]
fn an_entity_without_slots_is_silent() {
    let input = common::fixture(
        "Component",
        json!({"kind": "Component", "spec": {"schema": {"values": {}}}}),
        None,
        None,
        None,
    );
    assert!(rule::classify(&input).is_empty());
    let no_spec = common::fixture("Component", json!({"kind": "Component"}), None, None, None);
    assert!(rule::classify(&no_spec).is_empty());
    let slots_not_a_map = common::fixture(
        "Component",
        json!({"kind": "Component", "spec": {"featuresSchema": []}}),
        None,
        None,
        None,
    );
    assert!(rule::classify(&slots_not_a_map).is_empty());
}

#[test]
fn every_pinning_slot_is_reported_and_declared_ones_are_skipped() {
    let slots = json!({
        "a_flagged": {"selector": "feature.aggrega.ai/kind=secret"},
        "b_declared": {"selector": "feature.aggrega.ai/kind=secret", "sensitive": true},
        "c_flagged": {"selector": "feature.aggrega.ai/kind in (secret)"},
        "d_unrelated": {"selector": "feature.aggrega.ai/kind=tls"},
    });
    let findings = classify("Component", slots);
    assert_eq!(findings.len(), 2, "got {findings:?}");
    // Slot iteration is key-ordered, so the finding order is stable across
    // runs of the same input.
    assert!(findings[0].contains("a_flagged"), "got {findings:?}");
    assert!(findings[1].contains("c_flagged"), "got {findings:?}");
}
