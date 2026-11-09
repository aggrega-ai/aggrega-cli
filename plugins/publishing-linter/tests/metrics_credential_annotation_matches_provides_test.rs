// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//! Unit tests for the metrics-credential-annotation-matches-provides rule.
//!
//! Every row is one Component differing from an agreeing one in one respect,
//! so a finding is attributable to that respect, and each reported pair sits
//! beside the agreeing pair that differs from it only in the provides key.
//!
//! Coverage:
//!  - The two disagreements: annotated `none` while declaring the key, and
//!    annotated `target-key` or `separate-token` while not declaring it.
//!  - Each agreeing pair, an unannotated Component, and a value outside the
//!    three, declaring the key and not.
//!  - The provides pillar absent, written with `properties`, and written as a
//!    shape the reader cannot enumerate; a Component extending another.
//!  - Another entity kind, the finding's wording, rule id and severity.
//!  - The annotation reader shared with the connection rule reads only the
//!    three values.

mod common;

use aggrega_pdk_rust::Diagnostic;
use publishing_linter::metrics_credential::{self, MetricsCredential};
use publishing_linter::rules::metrics_credential_annotation_matches_provides as rule;
use serde_json::{json, Value};

/// The annotation key, written as the standard catalog writes it rather than
/// through the rule's own constant, so a rule reading another key fails here.
const ANNOTATION: &str = "observability.aggrega.ai/metrics-credential";

/// The provides key, written as the catalog writes it, for the same reason.
const KEY: &str = "metrics_credential_required";

const NAME: &str = "example/ai-model/server";

/// A provides pillar declaring the metrics address and, when `with_key`, the
/// key saying whether an instance's metrics need a credential.
fn provides(with_key: bool) -> Value {
    let mut pillar = json!({"metrics_endpoint": {"type": "string"}});
    if with_key {
        pillar[KEY] = json!({"type": "boolean"});
    }
    pillar
}

/// A Component annotated with `annotation` when given, whose schema declares
/// the given provides pillar.
fn component(annotation: Option<Value>, pillar: Value) -> Value {
    let mut doc = json!({
        "apiVersion": "aggrega.ai/v1beta1",
        "kind": "Component",
        "metadata": {"name": NAME, "version": "1.0.0", "labels": {"component.aggrega.ai/type": "server"}},
        "spec": {"schema": {"values": {"port": {"type": "integer"}}, "provides": pillar}},
    });
    if let Some(value) = annotation {
        doc["metadata"]["annotations"] = json!({ ANNOTATION: value });
    }
    doc
}

/// Runs the rule over `doc` as an entity of `kind`, asserting that every
/// finding is the rule's own and advisory.
fn run_as(kind: &str, doc: Value) -> Vec<Diagnostic> {
    let findings = rule::classify(&common::fixture(kind, doc, None, None, None));
    for f in &findings {
        assert_eq!(f.rule_id, "metrics-credential-annotation-matches-provides");
        assert_eq!(f.rule_id, rule::RULE_ID);
        assert_eq!(f.severity, "warn", "the rule is advisory and never halts");
    }
    findings
}

fn run(doc: Value) -> Vec<Diagnostic> {
    run_as("Component", doc)
}

/// What a row expects: silence, or one finding carrying the phrase.
const EXTRA: Option<&str> = Some("but it declares the provides key");
const MISSING: Option<&str> = Some("but it does not declare the provides key");

#[test]
fn the_annotation_and_the_provides_key_are_held_to_agree() {
    // (row, annotation value, declares the key, expected finding)
    let rows: &[(&str, Option<Value>, bool, Option<&str>)] = &[
        ("none, the key declared", Some(json!("none")), true, EXTRA),
        ("none, no key", Some(json!("none")), false, None),
        (
            "target-key, no key",
            Some(json!("target-key")),
            false,
            MISSING,
        ),
        (
            "target-key, the key declared",
            Some(json!("target-key")),
            true,
            None,
        ),
        (
            "separate-token, no key",
            Some(json!("separate-token")),
            false,
            MISSING,
        ),
        (
            "separate-token, the key declared",
            Some(json!("separate-token")),
            true,
            None,
        ),
        ("unannotated, the key declared", None, true, None),
        ("unannotated, no key", None, false, None),
        (
            "another value, the key declared",
            Some(json!("open")),
            true,
            None,
        ),
        ("another value, no key", Some(json!("open")), false, None),
        (
            "a value in another case, no key",
            Some(json!("Target-Key")),
            false,
            None,
        ),
        ("a non-string value, no key", Some(json!(true)), false, None),
    ];
    for (row, annotation, with_key, want) in rows {
        let findings = run(component(annotation.clone(), provides(*with_key)));
        match want {
            None => assert!(
                findings.is_empty(),
                "{row}: want silence; got {findings:#?}"
            ),
            Some(phrase) => {
                assert_eq!(
                    findings.len(),
                    1,
                    "{row}: want one finding; got {findings:#?}"
                );
                let message = &findings[0].message;
                assert!(message.contains(phrase), "{row}: {message}");
            }
        }
    }
}

#[test]
fn the_finding_names_the_component_the_value_and_the_key() {
    let cases = [
        ("none", true, "served with no credential"),
        (
            "target-key",
            false,
            "take the key it requires of every caller",
        ),
        ("separate-token", false, "take a token of their own"),
    ];
    for (value, with_key, saying) in cases {
        let findings = run(component(Some(json!(value)), provides(with_key)));
        assert_eq!(findings.len(), 1, "{value}: got {findings:#?}");
        let message = &findings[0].message;
        for want in [
            format!("{NAME:?}"),
            format!("{ANNOTATION}: {value:?}"),
            format!("{KEY:?}"),
            saying.to_string(),
            "state one fact about the product".to_string(),
            "so they must agree".to_string(),
        ] {
            assert!(
                message.contains(want.as_str()),
                "{value}: the finding names {want:?}; got {message}"
            );
        }
    }
}

#[test]
fn a_component_declaring_no_provides_declares_no_key() {
    // No pillar at all is a real answer: the Component declares no key.
    let mut doc = component(Some(json!("target-key")), provides(false));
    doc["spec"]["schema"]
        .as_object_mut()
        .expect("schema")
        .remove("provides");
    let findings = run(doc);
    assert_eq!(findings.len(), 1, "got {findings:#?}");
    assert!(findings[0].message.contains(MISSING.expect("phrase")));

    // No schema at all is the same answer.
    let mut doc = component(Some(json!("target-key")), provides(false));
    doc["spec"].as_object_mut().expect("spec").remove("schema");
    let findings = run(doc);
    assert_eq!(findings.len(), 1, "got {findings:#?}");

    // And annotated none, no schema at all agrees.
    let mut doc = component(Some(json!("none")), provides(false));
    doc["spec"].as_object_mut().expect("spec").remove("schema");
    assert!(run(doc).is_empty());
}

#[test]
fn a_pillar_written_with_properties_is_read() {
    let pillar = json!({
        "type": "object",
        "properties": {"metrics_endpoint": {"type": "string"}, KEY: {"type": "boolean"}},
    });
    assert!(run(component(Some(json!("target-key")), pillar.clone())).is_empty());
    let findings = run(component(Some(json!("none")), pillar));
    assert_eq!(findings.len(), 1, "got {findings:#?}");
    assert!(findings[0].message.contains(EXTRA.expect("phrase")));
}

#[test]
fn a_pillar_the_reader_cannot_enumerate_draws_nothing() {
    for pillar in [
        json!({"$ref": "#/$defs/Published"}),
        json!({"allOf": [{"properties": {KEY: {"type": "boolean"}}}]}),
        json!({"$jsonschema": "type: object\nproperties:\n  metrics_endpoint: {type: string}\n"}),
    ] {
        for value in ["target-key", "none"] {
            let findings = run(component(Some(json!(value)), pillar.clone()));
            assert!(
                findings.is_empty(),
                "{value} over {pillar}: an unreadable pillar was judged: {findings:#?}"
            );
        }
    }
}

#[test]
fn a_component_extending_another_draws_nothing() {
    let mut doc = component(Some(json!("target-key")), provides(false));
    doc["spec"]["extends"] = json!("example/ai-model/base:1.0.0");
    doc["spec"]["schema"] = json!("inherited");
    assert!(
        run(doc).is_empty(),
        "an inherited schema was read as declaring nothing"
    );

    // The same Component authoring its own schema is read, so the silence above
    // is the extension's.
    let findings = run(component(Some(json!("target-key")), provides(false)));
    assert_eq!(findings.len(), 1, "got {findings:#?}");
}

#[test]
fn another_entity_kind_draws_nothing() {
    for kind in ["Feature", "Recipe", "Aggregate", "Plugin"] {
        let findings = run_as(kind, component(Some(json!("none")), provides(true)));
        assert!(findings.is_empty(), "{kind} was judged: {findings:#?}");
    }
}

#[test]
fn the_shared_reader_takes_only_the_three_values() {
    let cases: &[(Value, Option<MetricsCredential>)] = &[
        (json!("none"), Some(MetricsCredential::NoCredential)),
        (json!("target-key"), Some(MetricsCredential::TargetKey)),
        (
            json!("separate-token"),
            Some(MetricsCredential::SeparateToken),
        ),
        (json!("open"), None),
        (json!("None"), None),
        (json!(" none"), None),
        (json!(""), None),
        (json!(false), None),
        (Value::Null, None),
    ];
    for (value, want) in cases {
        let doc = component(Some(value.clone()), provides(false));
        assert_eq!(metrics_credential::declared(&doc), *want, "value {value}");
    }
    assert_eq!(
        metrics_credential::declared(&component(None, provides(false))),
        None
    );
    for stated in [
        MetricsCredential::NoCredential,
        MetricsCredential::TargetKey,
        MetricsCredential::SeparateToken,
    ] {
        let doc = component(Some(json!(stated.value())), provides(false));
        assert_eq!(
            metrics_credential::declared(&doc),
            Some(stated),
            "{stated:?} round-trips"
        );
    }
    assert_eq!(metrics_credential::ANNOTATION, ANNOTATION);
}
