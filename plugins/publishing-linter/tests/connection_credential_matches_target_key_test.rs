// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//! Unit tests for the connection-credential-matches-target-key rule.
//!
//! Every reported case is built from the agreeing pair by changing one end, so
//! a finding is attributable to that end, and every silence that matters is
//! asserted beside the shape that would make it a finding.
//!
//! Coverage:
//!  - The three reported cases: a credential on the connection with the
//!    target's key slot unfilled; the target's key slot filled with nothing on
//!    the connection; both filled with different locators.
//!  - The controls: one parameter feeding both ends; a target declaring no
//!    caller key; a caller key beside an unrelated pull-token slot, which is
//!    never read whatever it holds.
//!  - A target labelled managed: none of the three cases is reported, each
//!    shown reporting first against the same documents with a self-hosted
//!    target; every other hosting posture, and no hosting label, is judged.
//!  - Each end written by the Release rather than the Aggregate, and a
//!    connection only the Release wires.
//!  - A key reaching the target from a subsystem or a boundary: silent.
//!  - A connection in the short form, an inactive target, a target whose
//!    Component the host could not resolve, no Release, another entity kind.
//!  - The suppression flag, and the finding's severity.
//!  - A planted credential marker is absent from every finding of every case.
//!  - A scrape connection, judged by what its target states about how its
//!    metrics authenticate: not at all for `none`, never against the caller
//!    key for `separate-token`, and as any other connection for `target-key`,
//!    for no annotation and for any other value. A connection on another
//!    socket to the same targets is judged whatever they state, and a managed
//!    target is still not judged.

mod common;

use aggrega_pdk_rust::{Diagnostic, LinterInput, Policies};
use publishing_linter::rules::connection_credential_matches_target_key as rule;
use serde_json::{json, Value};

/// Written as a literal locator wherever a case needs one end to differ. The
/// text an author writes as a locator can be the secret itself, so it must
/// appear in no finding.
const MARKER: &str = "PLANTED-CREDENTIAL-MARKER-5e2d";

/// The locator both ends write when they agree: one parameter, read at each.
const SHARED: &str = "{{ .parameters.shared_key }}";

/// A key-checking server: it declares the caller key slot, and also a
/// registry pull token of the same secret family that no caller presents.
fn key_checking_server() -> Value {
    json!({
        "apiVersion": "aggrega.ai/v1alpha1",
        "kind": "Component",
        "metadata": {"name": "example/ai-model/server", "version": "1.0.0"},
        "spec": {
            "featuresSchema": {
                "api_key": {"selector": "feature.aggrega.ai/kind=secret", "sensitive": true},
                "model_pull_token": {"selector": "feature.aggrega.ai/kind=secret", "sensitive": true},
            },
        },
    })
}

/// The key-checking server carrying a hosting label, written with the label
/// key the standard catalog uses rather than the rule's own constant, so a
/// rule reading another key fails here.
fn hosted_server(hosting: &str) -> Value {
    let mut server = key_checking_server();
    server["metadata"]["labels"] = json!({"component.aggrega.ai/hosting": hosting});
    server
}

/// A target declaring no caller key — only the pull token.
fn keyless_server() -> Value {
    json!({
        "apiVersion": "aggrega.ai/v1alpha1",
        "kind": "Component",
        "metadata": {"name": "example/ai-model/open-server", "version": "1.0.0"},
        "spec": {
            "featuresSchema": {
                "model_pull_token": {"selector": "feature.aggrega.ai/kind=secret", "sensitive": true},
            },
        },
    })
}

/// The key-checking server stating how its metrics authenticate, written with
/// the annotation key the standard catalog uses rather than the rule's own
/// constant, so a rule reading another key fails here.
fn metrics_server(value: Value) -> Value {
    let mut server = key_checking_server();
    server["metadata"]["annotations"] =
        json!({"observability.aggrega.ai/metrics-credential": value});
    server
}

/// The socket the standard catalog gives a metrics scraper, written as the
/// catalog writes it rather than through the rule's own constant.
const SCRAPE: &str = "scrape_target";

/// The caller, whose model socket and scrape socket both declare the
/// credential slot. The rule reads no consumer's Component, so one caller
/// serves the connections on either socket.
fn caller() -> Value {
    json!({
        "apiVersion": "aggrega.ai/v1alpha1",
        "kind": "Component",
        "metadata": {"name": "example/ai-agent/caller", "version": "1.0.0"},
        "spec": {
            "usesSchema": {
                "model": {"featuresSchema": {"credential": {"sensitive": true}}},
                "scrape_target": {"featuresSchema": {"credential": {"sensitive": true}}},
            },
        },
    })
}

/// A credential attachment written in the Aggregate, its locator in `values`.
fn developer_secret(locator: &str) -> Value {
    json!({"type": "core/referenced-secret", "values": {"key": "api-key", "origin": locator}})
}

/// A credential attachment written in the Release, its locator in `infra`.
fn operator_secret(locator: &str) -> Value {
    json!({"type": "core/referenced-secret", "infra": {"origin": locator}})
}

/// The target's attachment block: its caller key filled with `key` when given,
/// and its pull token always filled with a locator unlike any other here.
fn target_features(key: Option<&str>) -> Value {
    let mut features = json!({"model_pull_token": developer_secret("registry-pull-token-elsewhere")});
    if let Some(locator) = key {
        features["api_key"] = developer_secret(locator);
    }
    features
}

/// The connection entry: the agent's `primary` connection to `model`, its
/// credential slot filled with `credential` when given.
fn connection(credential: Option<&str>) -> Value {
    let mut conn = json!({"target": "model"});
    if let Some(locator) = credential {
        conn["features"] = json!({"credential": developer_secret(locator)});
    }
    conn
}

/// An Aggregate holding the server `model` and the caller `agent`.
fn aggregate(target_key: Option<&str>, credential: Option<&str>) -> Value {
    aggregate_on("model", target_key, credential)
}

/// The same Aggregate with the caller's `primary` connection on `socket`.
fn aggregate_on(socket: &str, target_key: Option<&str>, credential: Option<&str>) -> Value {
    let mut uses = serde_json::Map::new();
    uses.insert(
        socket.to_string(),
        json!({"primary": connection(credential)}),
    );
    json!({
        "apiVersion": "aggrega.ai/v1alpha1",
        "kind": "Aggregate",
        "metadata": {"name": "two-ended", "version": "1.0.0"},
        "spec": {
            "components": {
                "model": {"type": "example/ai-model/server:1.0.0", "features": target_features(target_key)},
                "agent": {
                    "type": "example/ai-agent/caller:1.0.0",
                    "uses": Value::Object(uses),
                },
            },
        },
    })
}

/// A Release adding nothing to either end.
fn bare_release() -> Value {
    json!({
        "apiVersion": "aggrega.ai/v1alpha1",
        "kind": "Release",
        "metadata": {"name": "two-ended-release", "version": "1.0.0"},
        "spec": {"boundaries": {"app": {"components": {"model": {}, "agent": {}}}}},
    })
}

/// A Release whose `app` boundary carries the given component entries.
fn release_with(components: Value) -> Value {
    json!({
        "apiVersion": "aggrega.ai/v1alpha1",
        "kind": "Release",
        "metadata": {"name": "two-ended-release", "version": "1.0.0"},
        "spec": {"boundaries": {"app": {"components": components}}},
    })
}

fn input(aggregate: Value, release: Value) -> LinterInput {
    input_against(key_checking_server(), aggregate, release)
}

/// The input with `target` as the Component the `model` instance resolves to.
fn input_against(target: Value, aggregate: Value, release: Value) -> LinterInput {
    common::fixture_release(aggregate, release, &[("model", target), ("agent", caller())], None)
}

/// The three reported cases, each as the target's key and the connection's
/// credential that draw it, with a phrase only its finding carries.
const CASES: [(Option<&str>, Option<&str>, &str); 3] = [
    (None, Some(MARKER), "leaves its api_key slot"),
    (Some(MARKER), None, "carries nothing in its credential slot"),
    (Some(SHARED), Some(MARKER), "write their secret's locator differently"),
];

/// Runs the rule and asserts, for every case, that no finding carries the
/// planted marker and that every finding is the rule's own, at warn severity.
fn run(input: &LinterInput) -> Vec<Diagnostic> {
    let findings = rule::classify(input);
    for f in &findings {
        assert!(
            !f.message.contains(MARKER),
            "a finding quotes a locator: {}",
            f.message
        );
        assert_eq!(f.rule_id, rule::RULE_ID);
        assert_eq!(f.severity, "warn", "the rule never halts");
    }
    findings
}

fn assert_one(findings: &[Diagnostic], phrase: &str) {
    assert_eq!(findings.len(), 1, "want one finding; got {findings:#?}");
    let message = &findings[0].message;
    for want in [phrase, "\"primary\"", "\"model\"", "\"agent\"", "api_key", "credential"] {
        assert!(message.contains(want), "the finding names {want:?}; got {message}");
    }
}

#[test]
fn one_parameter_feeding_both_ends_is_silent() {
    let findings = run(&input(aggregate(Some(SHARED), Some(SHARED)), bare_release()));
    assert!(findings.is_empty(), "an agreeing pair drew {findings:#?}");
}

#[test]
fn a_credential_to_a_target_whose_key_slot_is_unfilled_is_reported() {
    let findings = run(&input(aggregate(None, Some(MARKER)), bare_release()));
    assert_one(&findings, "leaves its api_key slot");
}

#[test]
fn a_filled_key_slot_with_no_credential_on_the_connection_is_reported() {
    let findings = run(&input(aggregate(Some(MARKER), None), bare_release()));
    assert_one(&findings, "carries nothing in its credential slot");
}

#[test]
fn two_ends_writing_different_locators_are_reported() {
    let findings = run(&input(aggregate(Some(SHARED), Some(MARKER)), bare_release()));
    assert_one(&findings, "write their secret's locator differently");
}

#[test]
fn two_literals_with_the_same_text_are_silent() {
    let findings = run(&input(aggregate(Some(MARKER), Some(MARKER)), bare_release()));
    assert!(findings.is_empty(), "two equal literals drew {findings:#?}");
}

#[test]
fn a_target_declaring_no_caller_key_draws_nothing() {
    for credential in [Some(MARKER), None] {
        let input = common::fixture_release(
            aggregate(None, credential),
            bare_release(),
            &[("model", keyless_server()), ("agent", caller())],
            None,
        );
        let findings = run(&input);
        assert!(
            findings.is_empty(),
            "a target with no caller key drew {findings:#?} (credential {credential:?})"
        );
    }
}

#[test]
fn a_managed_target_is_not_judged_in_any_case() {
    for (target_key, credential, phrase) in CASES {
        // The same documents against a self-hosted target draw the case, so the
        // silence below is the managed label's and nothing else's.
        let self_hosted = input_against(
            hosted_server("self-hosted"),
            aggregate(target_key, credential),
            bare_release(),
        );
        assert_one(&run(&self_hosted), phrase);

        let managed = input_against(hosted_server("managed"), aggregate(target_key, credential), bare_release());
        let findings = run(&managed);
        assert!(
            findings.is_empty(),
            "a managed target was judged ({phrase:?}): {findings:#?}"
        );
    }

    // An end the Release writes is not judged against a managed target either.
    let release = release_with(json!({
        "model": {},
        "agent": {"uses": {"model": {"primary": {"features": {"credential": operator_secret(MARKER)}}}}},
    }));
    let self_hosted = input_against(hosted_server("self-hosted"), aggregate(Some(SHARED), None), release.clone());
    assert_one(&run(&self_hosted), "write their secret's locator differently");
    let managed = input_against(hosted_server("managed"), aggregate(Some(SHARED), None), release);
    let findings = run(&managed);
    assert!(findings.is_empty(), "a managed target was judged on the Release's credential: {findings:#?}");
}

#[test]
fn every_other_hosting_posture_is_judged() {
    let targets = [
        ("no hosting label", key_checking_server()),
        ("self-hosted", hosted_server("self-hosted")),
        ("external", hosted_server("external")),
        ("mixed", hosted_server("mixed")),
    ];
    for (posture, target) in targets {
        for (target_key, credential, phrase) in CASES {
            let findings = run(&input_against(target.clone(), aggregate(target_key, credential), bare_release()));
            assert_eq!(findings.len(), 1, "a {posture} target must be judged ({phrase:?}); got {findings:#?}");
            assert!(findings[0].message.contains(phrase), "{posture}: {}", findings[0].message);
        }
        let agreeing = run(&input_against(target, aggregate(Some(SHARED), Some(SHARED)), bare_release()));
        assert!(agreeing.is_empty(), "an agreeing pair to a {posture} target drew {agreeing:#?}");
    }
}

#[test]
fn the_pull_token_beside_the_caller_key_is_never_read() {
    // The pull token is filled with a locator unlike the connection's, and the
    // caller key agrees with it: nothing is reported.
    let findings = run(&input(aggregate(Some(SHARED), Some(SHARED)), bare_release()));
    assert!(findings.is_empty(), "the pull token was read as a key: {findings:#?}");
    // With the caller key unfilled, the filled pull token does not stand in for
    // it: the target still checks no key.
    let findings = run(&input(aggregate(None, Some(SHARED)), bare_release()));
    assert_one(&findings, "leaves its api_key slot");
}

#[test]
fn a_credential_the_release_writes_on_the_connection_is_compared() {
    let release = release_with(json!({
        "model": {},
        "agent": {"uses": {"model": {"primary": {"features": {"credential": operator_secret(MARKER)}}}}},
    }));
    let findings = run(&input(aggregate(Some(SHARED), None), release));
    assert_one(&findings, "write their secret's locator differently");

    let release = release_with(json!({
        "model": {},
        "agent": {"uses": {"model": {"primary": {"features": {"credential": operator_secret(SHARED)}}}}},
    }));
    let findings = run(&input(aggregate(Some(SHARED), None), release));
    assert!(findings.is_empty(), "the Release's agreeing credential drew {findings:#?}");
}

#[test]
fn a_key_the_release_writes_on_the_target_is_compared() {
    let release = release_with(json!({
        "model": {"features": {"api_key": operator_secret(MARKER)}},
        "agent": {},
    }));
    let findings = run(&input(aggregate(None, Some(SHARED)), release));
    assert_one(&findings, "write their secret's locator differently");
}

#[test]
fn a_connection_only_the_release_wires_is_read() {
    let mut agg = aggregate(Some(SHARED), None);
    agg["spec"]["components"]["agent"]
        .as_object_mut()
        .expect("agent")
        .remove("uses");
    let release = release_with(json!({
        "model": {},
        "agent": {"uses": {"model": {"primary": {"target": "model", "features": {"credential": operator_secret(MARKER)}}}}},
    }));
    let findings = run(&input(agg.clone(), release));
    assert_one(&findings, "write their secret's locator differently");

    let release = release_with(json!({
        "model": {},
        "agent": {"uses": {"model": {"primary": {"target": "model"}}}},
    }));
    let findings = run(&input(agg, release));
    assert_one(&findings, "carries nothing in its credential slot");
}

#[test]
fn a_key_reaching_the_target_from_a_subsystem_or_a_boundary_is_not_read() {
    // A subsystem-wide attachment named for the key slot reaches the target.
    let agg = json!({
        "apiVersion": "aggrega.ai/v1alpha1",
        "kind": "Aggregate",
        "metadata": {"name": "two-ended", "version": "1.0.0"},
        "spec": {"subsystems": {
            "serving": {
                "features": {"api_key": developer_secret(MARKER)},
                "components": {"model": {"type": "example/ai-model/server:1.0.0"}},
            },
            "apps": {"components": {"agent": {
                "type": "example/ai-agent/caller:1.0.0",
                "uses": {"model": {"primary": connection(Some(SHARED))}},
            }}},
        }},
    });
    let findings = run(&input(agg, bare_release()));
    assert!(findings.is_empty(), "a subsystem-wide key was judged: {findings:#?}");

    // The same pair with the subsystem-wide key moved onto the target itself is
    // read, so the silence above is the broader scope's.
    let findings = run(&input(aggregate(Some(MARKER), Some(SHARED)), bare_release()));
    assert_one(&findings, "write their secret's locator differently");

    // A boundary-wide attachment named for the key slot reaches the target.
    let release = json!({
        "apiVersion": "aggrega.ai/v1alpha1",
        "kind": "Release",
        "metadata": {"name": "two-ended-release", "version": "1.0.0"},
        "spec": {"boundaries": {"serving": {
            "features": {"api_key": operator_secret(MARKER)},
            "components": {"model": {}},
        }, "apps": {"components": {"agent": {}}}}},
    });
    let findings = run(&input(aggregate(None, Some(SHARED)), release));
    assert!(findings.is_empty(), "a boundary-wide key was judged: {findings:#?}");
}

#[test]
fn a_short_form_connection_presents_no_key() {
    let mut agg = aggregate(Some(SHARED), None);
    agg["spec"]["components"]["agent"]["uses"] = json!({"model": ["model"]});
    let findings = run(&input(agg, bare_release()));
    assert_eq!(findings.len(), 1, "want one finding; got {findings:#?}");
    let message = &findings[0].message;
    assert!(message.contains("listed on socket \"model\""), "the finding names the short form: {message}");
    assert!(message.contains("carries nothing in its credential slot"), "{message}");
}

#[test]
fn an_inactive_target_draws_nothing() {
    let mut agg = aggregate(Some(SHARED), Some(MARKER));
    agg["spec"]["components"]["model"]["active"] = json!(false);
    let findings = run(&input(agg, bare_release()));
    assert!(findings.is_empty(), "an inactive target drew {findings:#?}");

    // A template-valued active is not evaluated and reads as present.
    let mut agg = aggregate(Some(SHARED), Some(MARKER));
    agg["spec"]["components"]["model"]["active"] = json!("{{ .parameters.serve }}");
    let findings = run(&input(agg, bare_release()));
    assert_one(&findings, "write their secret's locator differently");
}

#[test]
fn a_target_the_host_did_not_resolve_draws_nothing() {
    let input = common::fixture_release(
        aggregate(Some(SHARED), Some(MARKER)),
        bare_release(),
        &[("agent", caller())],
        None,
    );
    let findings = run(&input);
    assert!(findings.is_empty(), "an unresolved target drew {findings:#?}");
}

#[test]
fn no_release_and_another_entity_kind_draw_nothing() {
    let mut no_release = input(aggregate(Some(SHARED), Some(MARKER)), bare_release());
    no_release.release = None;
    assert!(run(&no_release).is_empty(), "an input with no Release was judged");

    let mut component = input(aggregate(Some(SHARED), Some(MARKER)), bare_release());
    component.entity_kind = "Component".to_string();
    assert!(run(&component).is_empty(), "a Component input was judged");
}

#[test]
fn the_suppression_silences_every_case() {
    for (target_key, credential) in [(None, Some(MARKER)), (Some(MARKER), None), (Some(SHARED), Some(MARKER))] {
        let reported = run(&input(aggregate(target_key, credential), bare_release()));
        assert_eq!(reported.len(), 1, "the case must report before it is suppressed");
        let suppressed = common::fixture_release(
            aggregate(target_key, credential),
            bare_release(),
            &[("model", key_checking_server()), ("agent", caller())],
            Some(Policies {
                allow_connection_credential_mismatches: true,
                ..Default::default()
            }),
        );
        assert!(run(&suppressed).is_empty(), "the suppression left a finding");
    }
}

// ---- Scrape connections ----

/// The phrase each reported case's finding carries, named for the rows below.
const PRESENTS_NO_KEY: Option<&str> = Some("carries nothing in its credential slot");
const LOCATORS_DIFFER: Option<&str> = Some("write their secret's locator differently");
const TARGET_CHECKS_NO_KEY: Option<&str> = Some("leaves its api_key slot");

/// A credential that is not the target's key: a token issued for its metrics
/// alone, read from a parameter of its own.
const METRICS_TOKEN: &str = "{{ .parameters.metrics_token }}";

/// Asserts what one scrape row drew: silence, or one finding carrying the
/// phrase and naming the connection, the scrape socket, the target and the two
/// slots.
fn assert_row(row: &str, findings: &[Diagnostic], want: Option<&str>) {
    match want {
        None => assert!(
            findings.is_empty(),
            "{row}: want silence; got {findings:#?}"
        ),
        Some(phrase) => {
            assert_one(findings, phrase);
            let message = &findings[0].message;
            assert!(
                message.contains("on socket \"scrape_target\""),
                "{row}: the finding names the scrape socket; got {message}"
            );
        }
    }
}

/// The target a row reaches: the key-checking server stating `annotation`, or
/// stating nothing.
fn target_stating(annotation: Option<Value>) -> Value {
    match annotation {
        Some(value) => metrics_server(value),
        None => key_checking_server(),
    }
}

#[test]
fn a_scrape_is_judged_by_what_its_target_states_about_its_metrics() {
    // One row per target statement and case: the annotation value (none for an
    // unannotated target), the target's key, the scrape connection's
    // credential, and what the row draws.
    let rows = [
        // Metrics served with no credential, as vllm and tgi serve theirs: the
        // scrape is not judged in any case.
        (
            "vllm-like target stating none, key filled, no credential",
            Some(json!("none")),
            Some(SHARED),
            None,
            None,
        ),
        (
            "tgi-like target stating none, key filled, a metrics-only token",
            Some(json!("none")),
            Some(SHARED),
            Some(METRICS_TOKEN),
            None,
        ),
        (
            "target stating none, key unfilled, a credential",
            Some(json!("none")),
            None,
            Some(METRICS_TOKEN),
            None,
        ),
        // Metrics taking a token of their own: never compared with the key.
        (
            "target stating separate-token, a metrics-only token",
            Some(json!("separate-token")),
            Some(SHARED),
            Some(METRICS_TOKEN),
            None,
        ),
        (
            "target stating separate-token, key filled, no credential",
            Some(json!("separate-token")),
            Some(SHARED),
            None,
            None,
        ),
        (
            "target stating separate-token, key unfilled, a token",
            Some(json!("separate-token")),
            None,
            Some(METRICS_TOKEN),
            None,
        ),
        // Metrics taking the caller key: judged as any connection is.
        (
            "target stating target-key, no credential",
            Some(json!("target-key")),
            Some(SHARED),
            None,
            PRESENTS_NO_KEY,
        ),
        (
            "target stating target-key, a metrics-only token",
            Some(json!("target-key")),
            Some(SHARED),
            Some(METRICS_TOKEN),
            LOCATORS_DIFFER,
        ),
        (
            "target stating target-key, key unfilled",
            Some(json!("target-key")),
            None,
            Some(SHARED),
            TARGET_CHECKS_NO_KEY,
        ),
        (
            "target stating target-key, one parameter at both ends",
            Some(json!("target-key")),
            Some(SHARED),
            Some(SHARED),
            None,
        ),
        // No statement: judged as today.
        (
            "unannotated target, no credential",
            None,
            Some(SHARED),
            None,
            PRESENTS_NO_KEY,
        ),
        (
            "unannotated target, a metrics-only token",
            None,
            Some(SHARED),
            Some(METRICS_TOKEN),
            LOCATORS_DIFFER,
        ),
        (
            "unannotated target, key unfilled",
            None,
            None,
            Some(SHARED),
            TARGET_CHECKS_NO_KEY,
        ),
        // A value the rule does not know states nothing: judged as today.
        (
            "target stating another value",
            Some(json!("open")),
            Some(SHARED),
            None,
            PRESENTS_NO_KEY,
        ),
        (
            "target stating none in another letter case",
            Some(json!("None")),
            Some(SHARED),
            None,
            PRESENTS_NO_KEY,
        ),
        (
            "target stating a non-string value",
            Some(json!(true)),
            Some(SHARED),
            None,
            PRESENTS_NO_KEY,
        ),
    ];
    for (row, annotation, target_key, credential, want) in rows {
        let findings = run(&input_against(
            target_stating(annotation),
            aggregate_on(SCRAPE, target_key, credential),
            bare_release(),
        ));
        assert_row(row, &findings, want);
    }
}

#[test]
fn a_connection_on_another_socket_is_judged_whatever_the_target_states() {
    // A model connection to a target stating each value, in each case: every
    // case is reported, so no statement about metrics reaches a caller of the
    // target's own API.
    for annotation in [
        Some(json!("none")),
        Some(json!("separate-token")),
        Some(json!("target-key")),
        None,
    ] {
        for (target_key, credential, phrase) in CASES {
            let findings = run(&input_against(
                target_stating(annotation.clone()),
                aggregate_on("model", target_key, credential),
                bare_release(),
            ));
            assert_one(&findings, phrase);
            assert!(
                findings[0].message.contains("on socket \"model\""),
                "{annotation:?}: {}",
                findings[0].message
            );
        }
    }
}

#[test]
fn the_socket_decides_which_connection_to_one_target_is_judged() {
    // One target stating that its metrics are open, reached twice by one
    // caller: a model connection reading a different parameter and a scrape
    // connection carrying a metrics-only token. Only the model connection is
    // reported, so the silence on the scrape is its socket's.
    let mut agg = aggregate_on("model", Some(SHARED), Some(MARKER));
    agg["spec"]["components"]["agent"]["uses"][SCRAPE] =
        json!({"metrics": connection(Some(METRICS_TOKEN))});
    let findings = run(&input_against(
        metrics_server(json!("none")),
        agg,
        bare_release(),
    ));
    assert_one(&findings, "write their secret's locator differently");
    assert!(
        findings[0].message.contains("on socket \"model\""),
        "{}",
        findings[0].message
    );
}

#[test]
fn a_scrape_the_release_writes_is_judged_by_the_same_statement() {
    // A scrape connection only the Release wires, carrying a metrics-only token.
    let mut agg = aggregate(Some(SHARED), None);
    agg["spec"]["components"]["agent"]
        .as_object_mut()
        .expect("agent")
        .remove("uses");
    let release = release_with(json!({
        "model": {},
        "agent": {"uses": {SCRAPE: {"primary": {
            "target": "model",
            "features": {"credential": operator_secret(METRICS_TOKEN)},
        }}}},
    }));
    let rows = [
        ("separate-token", Some(json!("separate-token")), None),
        ("none", Some(json!("none")), None),
        ("target-key", Some(json!("target-key")), LOCATORS_DIFFER),
        ("unannotated", None, LOCATORS_DIFFER),
    ];
    for (row, annotation, want) in rows {
        let findings = run(&input_against(
            target_stating(annotation),
            agg.clone(),
            release.clone(),
        ));
        assert_row(
            &format!("a scrape the Release wires, {row}"),
            &findings,
            want,
        );
    }
}

#[test]
fn a_managed_target_stating_target_key_is_still_not_judged() {
    let mut target = metrics_server(json!("target-key"));
    target["metadata"]["labels"] = json!({"component.aggrega.ai/hosting": "managed"});
    let findings = run(&input_against(
        target,
        aggregate_on(SCRAPE, Some(SHARED), None),
        bare_release(),
    ));
    assert!(
        findings.is_empty(),
        "a managed target was judged: {findings:#?}"
    );
}
