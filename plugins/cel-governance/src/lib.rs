// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//! Governance plugin that evaluates operator-authored expressions.
//!
//! The plugin evaluates policies written as Common Expression Language
//! expressions against a build, entirely in-guest: the evaluator is compiled into
//! the plugin, so nothing outside it runs a policy engine or carries any
//! policy-language logic. The attachment's own files and settings are read and
//! handed here; this plugin loads them, evaluates the expressions, and reports the
//! decisions they reached.
//!
//! # What it decides over
//!
//! One `validate` export serves both verdict roles, because the role fixes which
//! payload arrives — the build graph at one role, the rendered output files at the
//! other. The plugin reads the payload it was given and never declares which one
//! it expects.
//!
//! A policy addresses the whole payload through one name, `input`, so the names it
//! reads are the names the wire gives them.
//!
//! Attached over the build graph, a policy reads the graph as `input.graph` and
//! the pre-enumerated routing paths as `input.precalculated_paths`.
//!
//! Attached over the rendered output, a policy reads each output file as an entry
//! in `input.artifacts`, with its output path at `.path`, its raw rendered text at
//! `.content`, the format it was rendered in at `.format`, and its structured
//! documents at `.documents`. A file that could not be read structurally carries
//! `.parse_error` and no documents, so a policy can reject on the failure itself
//! rather than having an unreadable file pass unexamined.
//!
//! In both cases the attachment's own files are visible as `input.sources` and its
//! settings as `input.settings`, so an operator-supplied threshold is read as
//! `input.settings.<key>`. The settings are always present as a value, empty when
//! the attachment declares none, so an expression addressing them does not have to
//! guard for their absence.
//!
//! # Authoring convention, and how to change it
//!
//! An expression language has no rule names to collect decisions under, so the
//! decisions are the value each expression produces: a map from decision key to
//! the messages reached under it.
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
//! One attached file is one expression, and a policy set is as many files as it
//! needs. A key a file omits reached no decision there, so a file need not name
//! every key. A key carries a list of messages, a single message, or the null
//! value for none.
//!
//! By default the decision keys are the conventional ones — `deny` for a message
//! that stops the build, `warn` for one that surfaces alongside a build that still
//! completes, and `audit` for one that is recorded without changing the outcome —
//! so a policy written to that convention runs with no configuration at all.
//!
//! Those key names belong to the policy, not to this plugin. An operator whose
//! policies name their decisions differently declares the mapping in the
//! attachment's settings:
//!
//! ```yaml
//! with:
//!   settings:
//!     translation:
//!       - key: blocked
//!         severity: halt
//!       - key: advisory
//!         severity: warn
//! ```
//!
//! A declared translation replaces the default rather than extending it, so the
//! keys it names are exactly the keys read. The order it declares is the order
//! decisions are reported in. A malformed translation is refused before the policy
//! runs, against the schema this plugin declares for its settings — the same
//! schema the sibling governance plugin declares, so a translation means the same
//! thing whichever policy language expresses the decisions.
//!
//! # Ordering
//!
//! The messages under one decision key are reported in sorted order, and that is a
//! guarantee rather than a presentation choice: a comprehension over a map walks it
//! in an order the evaluator does not fix, so identical input could otherwise
//! produce the same messages in a different sequence each time. Across keys the
//! order is the translation's declared order, and across files it is the order the
//! host supplied them in.
//!
//! # Failure is never a silent allow
//!
//! When an expression cannot be read, cannot be evaluated, or does not produce
//! decisions in the documented shape, the plugin fails instead of returning a
//! verdict, and that failure is reported as a build fault naming the file and,
//! where the language can locate it, the position. It is deliberately distinct
//! from a decision that stops the build: "this policy could not be evaluated" must
//! never read as "this policy allows the build".

mod engine;

use aggrega_abi::RenderPolicyInput;
use aggrega_pdk_rust::{
    describe, PluginDescriptor, PluginDescriptorMeta, PluginExport, PolicyInput, PolicyOutput,
    API_VERSION,
};
use extism_pdk::{plugin_fn, FnResult};
use governancecore::artifacts::render_input;
use governancecore::payload::{read as read_payload, settings_value};
use governancecore::sources::project as project_sources;
use governancecore::translation::{settings_schema, Translation};

/// The field a policy reads the attachment's settings through, and the field the
/// payload carries them in.
const SETTINGS_FIELD: &str = "settings";

/// The static `describe()` self-report the host reads once when it loads the
/// plugin.
///
/// It declares the export the verdict roles dispatch to, and the schema that types
/// the attachment's settings. It declares no capabilities: a governance plugin is
/// not selected by capability, and what it may emit is fixed by the role it is
/// attached at.
///
/// It declares exactly the exports this plugin implements. The host refuses to
/// call an export a plugin did not declare, so an attachment expecting an export
/// that is absent is refused by name rather than silently doing nothing — which is
/// why an export is declared here in the same change that implements it.
fn descriptor() -> PluginDescriptor {
    PluginDescriptor {
        abi_version: API_VERSION.to_string(),
        plugin_type: "governance".to_string(),
        description:
            "Evaluate operator-authored Common Expression Language against a build with an \
             embedded expression evaluator."
                .to_string(),
        exports: vec![PluginExport {
            name: "validate".to_string(),
            ..Default::default()
        }],
        settings_schema: Some(
            serde_json::value::RawValue::from_string(settings_schema())
                .expect("the declared settings schema is valid JSON"),
        ),
        metadata: Some(Box::new(PluginDescriptorMeta {
            author: "aggrega".to_string(),
            license: "Apache-2.0".to_string(),
            ..Default::default()
        })),
        ..Default::default()
    }
}

/// Reports the plugin's static self-report, read once at load before any other
/// export is called.
#[plugin_fn]
pub fn describe() -> FnResult<String> {
    Ok(describe!(descriptor()))
}

/// Evaluates the attached expressions against the payload and reports the
/// decisions they reached. A failure to evaluate propagates, and is surfaced as a
/// build fault rather than as a decision.
#[plugin_fn]
pub fn validate(input: String) -> FnResult<String> {
    let output = verdict(&input)?;
    Ok(serde_json::to_string(&output)?)
}

/// Reads the payload, evaluates the expressions against it, and collects the
/// decisions.
///
/// The payload identifies itself by shape: one facet carries the rendered output
/// files, the other carries the build graph, and each field is required in the
/// facet that carries it. Reading the shape is what lets one export serve both
/// roles without the plugin declaring which role it is attached at.
fn verdict(raw: &str) -> anyhow::Result<PolicyOutput> {
    let payload: serde_json::Value = serde_json::from_str(raw)?;
    let results = if payload.get("artifacts").is_some() {
        let rendered: RenderPolicyInput = read_payload(&payload, &["artifacts"])?;
        let translation = Translation::from_settings(rendered.settings.as_deref())?;
        // The rendered facet's input document is assembled rather than passed
        // through, because the artifacts are projected into readable documents on
        // the way. It already carries the settings under the same name.
        let input = render_input(&rendered);
        engine::evaluate(&project_sources(&rendered.sources), &input, &translation)?
    } else {
        let graph: PolicyInput = read_payload(&payload, &["precalculated_paths"])?;
        let translation = Translation::from_settings(graph.settings.as_deref())?;
        // The whole payload is the input document, so a policy reads the graph and
        // the routing paths under the names the wire gives them. The settings are
        // written in as a value even when the attachment declared none: an
        // expression that reads an absent field fails, so leaving the field out
        // would turn "this attachment configured nothing" into a policy failure
        // for every policy that reads a threshold.
        let mut input = payload;
        input[SETTINGS_FIELD] = settings_value(graph.settings.as_deref());
        engine::evaluate(&project_sources(&graph.sources), &input, &translation)?
    };
    Ok(PolicyOutput {
        results,
        ..Default::default()
    })
}

#[cfg(test)]
mod tests {
    use super::*;
    use aggrega_pdk_rust::{SEVERITY_HALT, SEVERITY_INFO, SEVERITY_WARN};

    const GRAPH_POLICY: &str =
        r#"{"deny": size(input.graph.spec.nodes) > 1 ? ["more than one component"] : []}"#;

    const ARTIFACT_POLICY: &str = r#"{
      "deny": input.artifacts
                .filter(a, a.documents.exists(d, d.kind == "Service"))
                .map(a, "a service was rendered to " + a.path)
    }"#;

    /// Builds a graph payload carrying the named nodes.
    ///
    /// The graph is built from the generated type's own defaults rather than
    /// hand-written JSON, so a field added to the wire shape does not silently
    /// leave this fixture describing a graph the guest would reject.
    fn graph_with_nodes(names: &[&str]) -> serde_json::Value {
        let mut graph = aggrega_abi::IRGraph::default();
        for name in names {
            graph
                .spec
                .nodes
                .insert((*name).to_string(), aggrega_abi::IRNode::default());
        }
        serde_json::to_value(&graph).expect("a default graph serializes")
    }

    /// Builds a graph-facet payload: a graph with the named nodes, one policy
    /// source, and the given settings.
    fn graph_payload(nodes: &[&str], policy: &str, settings: Option<serde_json::Value>) -> String {
        let mut payload = serde_json::json!({
            "graph": graph_with_nodes(nodes),
            "precalculated_paths": [],
            "sources": [{ "path": "policy.cel", "content": policy }],
        });
        if let Some(settings) = settings {
            payload["settings"] = settings;
        }
        payload.to_string()
    }

    #[test]
    fn the_descriptor_declares_every_implemented_export() {
        let descriptor = descriptor();
        let declared: Vec<&str> = descriptor.exports.iter().map(|e| e.name.as_str()).collect();
        assert_eq!(declared, vec!["validate"]);
    }

    #[test]
    fn the_descriptor_reports_the_governance_plugin_type() {
        assert_eq!(descriptor().plugin_type, "governance");
    }

    #[test]
    fn the_descriptor_declares_no_capabilities() {
        assert!(descriptor().capabilities.is_empty());
    }

    #[test]
    fn the_descriptor_declares_the_same_settings_schema_as_the_sibling_plugin() {
        // The translation contract is shared, so the schema that types it must be
        // the shared one rather than a local restatement: an attachment accepted
        // by one governance plugin and refused by the other, with the same
        // settings, is a difference no reader could attribute.
        let descriptor = descriptor();
        let declared = descriptor
            .settings_schema
            .as_deref()
            .expect("a settings schema is declared");
        assert_eq!(declared.get(), settings_schema());
        let parsed: serde_json::Value =
            serde_json::from_str(declared.get()).expect("the schema is valid JSON");
        assert!(parsed["properties"]["translation"].is_object(), "{parsed}");
    }

    #[test]
    fn the_graph_facet_reads_the_graph() {
        let output =
            verdict(&graph_payload(&["a", "b"], GRAPH_POLICY, None)).expect("evaluation succeeds");
        assert_eq!(output.results.len(), 1, "{:?}", output.results);
        assert_eq!(output.results[0].severity, SEVERITY_HALT);
        assert_eq!(output.results[0].message, "more than one component");
        assert!(output.error.is_empty());
    }

    #[test]
    fn the_graph_facet_allows_when_no_decision_is_reached() {
        let output =
            verdict(&graph_payload(&["a"], GRAPH_POLICY, None)).expect("evaluation succeeds");
        assert!(output.results.is_empty(), "{:?}", output.results);
    }

    #[test]
    fn the_rendered_facet_reads_the_artifacts() {
        let payload = serde_json::json!({
            "artifacts": [{ "path": "svc.yaml", "content": "kind: Service\n" }],
            "sources": [{ "path": "policy.cel", "content": ARTIFACT_POLICY }],
        });
        let output = verdict(&payload.to_string()).expect("evaluation succeeds");
        assert_eq!(output.results.len(), 1, "{:?}", output.results);
        assert_eq!(
            output.results[0].message,
            "a service was rendered to svc.yaml"
        );
    }

    #[test]
    fn the_rendered_facet_reads_a_host_supplied_tree() {
        let payload = serde_json::json!({
            "artifacts": [{
                "path": "main.tf",
                "content": "resource \"x\" \"y\" {\n a = 1\n}\n",
                "tree": { "kind": "Service" },
                "format": "hcl",
            }],
            "sources": [{ "path": "policy.cel", "content": ARTIFACT_POLICY }],
        });
        let output = verdict(&payload.to_string()).expect("evaluation succeeds");
        assert_eq!(
            output.results.len(),
            1,
            "the host-supplied tree is what the policy reads: {:?}",
            output.results
        );
    }

    #[test]
    fn the_rendered_facet_can_reject_on_a_file_it_could_not_read() {
        let payload = serde_json::json!({
            "artifacts": [{ "path": "broken.yaml", "content": "{ replicas: [1, 2\n" }],
            "sources": [{
                "path": "policy.cel",
                "content": r#"{"deny": input.artifacts.filter(a, has(a.parse_error)).map(a, "unreadable output at " + a.path)}"#,
            }],
        });
        let output = verdict(&payload.to_string()).expect("evaluation succeeds");
        assert_eq!(output.results.len(), 1, "{:?}", output.results);
        assert_eq!(
            output.results[0].message,
            "unreadable output at broken.yaml"
        );
    }

    #[test]
    fn an_operator_translation_applies_at_both_facets() {
        let policy = r#"{"advisory": ["noted"]}"#;
        let settings = serde_json::json!({
            "translation": [{ "key": "advisory", "severity": "info" }]
        });

        let graph = verdict(&graph_payload(&["a"], policy, Some(settings.clone())))
            .expect("the graph facet evaluates");
        assert_eq!(graph.results.len(), 1, "{:?}", graph.results);
        assert_eq!(graph.results[0].severity, SEVERITY_INFO);

        let rendered_payload = serde_json::json!({
            "artifacts": [],
            "sources": [{ "path": "policy.cel", "content": policy }],
            "settings": settings,
        });
        let rendered =
            verdict(&rendered_payload.to_string()).expect("the rendered facet evaluates");
        assert_eq!(rendered.results.len(), 1, "{:?}", rendered.results);
        assert_eq!(rendered.results[0].severity, SEVERITY_INFO);
    }

    #[test]
    fn a_malformed_translation_is_refused_before_the_policy_runs() {
        let settings = serde_json::json!({
            "translation": [{ "key": "blocked", "severity": "reject" }]
        });
        let err = verdict(&graph_payload(&["a"], GRAPH_POLICY, Some(settings)))
            .expect_err("a severity outside the closed set is refused");
        assert!(err.to_string().contains("blocked"), "{err}");
    }

    #[test]
    fn a_threshold_reads_as_settings_even_when_the_attachment_declares_none() {
        // Settings are optional, and an expression that reads an absent field
        // fails — so the field is always present as a value. A policy guarding on
        // the field's own contents therefore evaluates rather than failing.
        let policy =
            r#"{"warn": has(input.settings.max_nodes) ? ["configured"] : ["not configured"]}"#;
        let output = verdict(&graph_payload(&["a"], policy, None)).expect("evaluation succeeds");
        assert_eq!(output.results.len(), 1, "{:?}", output.results);
        assert_eq!(output.results[0].severity, SEVERITY_WARN);
        assert_eq!(output.results[0].message, "not configured");
    }

    #[test]
    fn an_evaluation_failure_is_a_build_fault_rather_than_a_verdict() {
        let err = verdict(&graph_payload(
            &["a"],
            r#"{"deny": [input.graph.nope]}"#,
            None,
        ))
        .expect_err("an evaluation failure is not a verdict");
        assert!(err.to_string().contains("could not be evaluated"), "{err}");
    }

    #[test]
    fn the_graph_facet_tolerates_absent_routing_paths() {
        // Some senders write the absent value where the payload declares a list.
        // Refusing it would fail an ordinary build with nothing to enumerate.
        let payload = serde_json::json!({
            "graph": graph_with_nodes(&["a"]),
            "precalculated_paths": serde_json::Value::Null,
            "sources": [{ "path": "policy.cel", "content": GRAPH_POLICY }],
        });
        let output = verdict(&payload.to_string()).expect("evaluation succeeds");
        assert!(output.results.is_empty(), "{:?}", output.results);
    }

    #[test]
    fn identical_input_yields_an_identical_report() {
        // The whole output, serialized, is what the host reads — so the property
        // asserted is byte equality of that, not just the message set.
        let policy = r#"{"deny": input.graph.spec.nodes.map(n, "component " + n)}"#;
        let payload = graph_payload(&["delta", "alpha", "charlie", "bravo"], policy, None);
        let first = serde_json::to_string(&verdict(&payload).expect("evaluation succeeds"))
            .expect("the output serializes");
        let second = serde_json::to_string(&verdict(&payload).expect("evaluation succeeds"))
            .expect("the output serializes");
        assert_eq!(first, second);
        assert!(
            first.contains("component alpha")
                && first.find("component alpha") < first.find("component bravo"),
            "the report is in sorted order: {first}"
        );
    }
}
