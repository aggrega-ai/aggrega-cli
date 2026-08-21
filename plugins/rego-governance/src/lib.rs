// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//! Governance plugin that evaluates operator-authored Rego.
//!
//! The plugin evaluates policies written in Rego against a build, entirely
//! in-guest: the policy engine is compiled into the plugin, so nothing outside it
//! runs a policy engine or carries any policy-language logic. The attachment's
//! own files and settings are read and handed here; this plugin loads them,
//! evaluates the policy, and reports the decisions it reached.
//!
//! # What it decides over
//!
//! One `validate` export serves both verdict roles, because the role an operator
//! attaches the plugin at fixes which payload arrives — the build graph at one
//! role, the rendered output files at the other. The plugin reads the payload it
//! was given and never declares which one it expects.
//!
//! Attached over the build graph, a policy reads the graph as `input.graph` and
//! the pre-enumerated routing paths as `input.precalculated_paths`.
//!
//! Attached over the rendered output, a policy reads each output file as
//! `input.artifacts[_]`, with its output path at `.path`, its raw rendered text
//! at `.content`, the format it was rendered in at `.format`, and its structured
//! documents at `.documents`. A file that could not be read structurally carries
//! `.parse_error` and no documents, so a policy can reject on the failure itself
//! rather than having an unreadable file pass unexamined.
//!
//! In both cases the attachment's own files are visible as `input.sources` and
//! its settings as `input.settings`, and the settings are also the policy's
//! `data` document, so a policy reads an operator-supplied threshold as
//! `data.<key>`.
//!
//! # Authoring convention, and how to change it
//!
//! A policy collects its messages under decision keys. By default those keys are
//! the conventional Rego collection names — `deny` for a
//! message that stops the build, `warn` for one that surfaces alongside a build
//! that still completes, and `audit` for one that is recorded without changing
//! the outcome — so a policy written to that convention runs with no
//! configuration at all.
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
//! decisions are reported in. A malformed translation is refused before the
//! policy runs, against the schema this plugin declares for its settings.
//!
//! # Failure is never a silent allow
//!
//! When a policy cannot be evaluated — most often because it calls a builtin this
//! engine does not implement — the plugin fails instead of returning a verdict,
//! and that failure is reported as a build fault naming the plugin. It is
//! deliberately distinct from a decision that stops the build: "this policy could
//! not be evaluated" must never read as "this policy allows the build".

mod artifacts;
mod engine;
mod translation;

use aggrega_abi::RenderPolicyInput;
use aggrega_pdk_rust::{
    describe, PluginDescriptor, PluginDescriptorMeta, PluginExport, PolicyInput, PolicyOutput,
    API_VERSION,
};
use extism_pdk::{plugin_fn, FnResult};

use translation::Translation;

/// The static `describe()` self-report the host reads once when it loads the
/// plugin.
///
/// It declares the `validate` export and the schema that types the attachment's
/// settings. It declares no capabilities: a governance plugin is not selected by
/// capability, and what it may emit is fixed by the role it is attached at.
///
/// It declares exactly the exports this plugin implements. The host refuses to
/// call an export a plugin did not declare, so an attachment expecting an export
/// that is absent is refused by name rather than silently doing nothing.
fn descriptor() -> PluginDescriptor {
    PluginDescriptor {
        abi_version: API_VERSION.to_string(),
        plugin_type: "governance".to_string(),
        description: "Evaluate operator-authored Rego against a build with an embedded Rego engine."
            .to_string(),
        exports: vec![PluginExport {
            name: "validate".to_string(),
            ..Default::default()
        }],
        settings_schema: Some(
            serde_json::value::RawValue::from_string(translation::settings_schema())
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

/// Evaluates the attached policies against the payload and reports the decisions
/// they reached. A failure to evaluate propagates, and is surfaced as a build
/// fault rather than as a decision.
#[plugin_fn]
pub fn validate(input: String) -> FnResult<String> {
    let output = verdict(&input)?;
    Ok(serde_json::to_string(&output)?)
}

/// Reads the payload, evaluates the policies against it, and collects the
/// decisions.
///
/// The payload identifies itself by shape: one facet carries the rendered output
/// files, the other carries the build graph, and each field is required in the
/// facet that carries it. Reading the shape is what lets one export serve both
/// roles without the plugin declaring which role it is attached at.
fn verdict(raw: &str) -> anyhow::Result<PolicyOutput> {
    let payload: serde_json::Value = serde_json::from_str(raw)?;
    let results = if payload.get("artifacts").is_some() {
        let rendered: RenderPolicyInput = serde_json::from_str(raw)?;
        let settings = rendered.settings.as_deref();
        let translation = Translation::from_settings(settings)?;
        let input = artifacts::rego_input(&rendered);
        engine::evaluate(&rendered.sources, settings, &input, &translation)?
    } else {
        let graph: PolicyInput = serde_json::from_str(raw)?;
        let settings = graph.settings.as_deref();
        let translation = Translation::from_settings(settings)?;
        // The whole payload is the policy's input document, so a policy reads
        // the graph and the routing paths under the names the wire gives them.
        engine::evaluate(&graph.sources, settings, &payload, &translation)?
    };
    Ok(PolicyOutput {
        results,
        ..Default::default()
    })
}

#[cfg(test)]
mod tests {
    use super::*;
    use aggrega_pdk_rust::{SEVERITY_HALT, SEVERITY_INFO};

    const GRAPH_POLICY: &str = r#"
package proof

deny contains msg if {
    count(input.graph.spec.nodes) > 1
    msg := "more than one node"
}
"#;

    const ARTIFACT_POLICY: &str = r#"
package proof

deny contains msg if {
    some artifact in input.artifacts
    artifact.documents[_].kind == "Service"
    msg := "a service was rendered"
}
"#;

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
            "sources": [{ "path": "policy.rego", "content": policy }],
        });
        if let Some(settings) = settings {
            payload["settings"] = settings;
        }
        payload.to_string()
    }

    #[test]
    fn the_descriptor_declares_only_the_implemented_export() {
        let descriptor = descriptor();
        let declared: Vec<&str> = descriptor
            .exports
            .iter()
            .map(|e| e.name.as_str())
            .collect();
        assert_eq!(declared, vec!["validate"]);
    }

    #[test]
    fn the_descriptor_declares_no_capabilities() {
        assert!(descriptor().capabilities.is_empty());
    }

    #[test]
    fn the_descriptor_declares_a_settings_schema() {
        let descriptor = descriptor();
        let schema = descriptor
            .settings_schema
            .as_deref()
            .expect("a settings schema is declared");
        let parsed: serde_json::Value =
            serde_json::from_str(schema.get()).expect("the schema is valid JSON");
        assert!(parsed["properties"]["translation"].is_object(), "{parsed}");
    }

    #[test]
    fn the_graph_facet_reads_the_graph() {
        let output = verdict(&graph_payload(&["a", "b"], GRAPH_POLICY, None))
            .expect("evaluation succeeds");
        assert_eq!(output.results.len(), 1, "{:?}", output.results);
        assert_eq!(output.results[0].severity, SEVERITY_HALT);
        assert_eq!(output.results[0].message, "more than one node");
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
            "sources": [{ "path": "policy.rego", "content": ARTIFACT_POLICY }],
        });
        let output = verdict(&payload.to_string()).expect("evaluation succeeds");
        assert_eq!(output.results.len(), 1, "{:?}", output.results);
        assert_eq!(output.results[0].message, "a service was rendered");
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
            "sources": [{ "path": "policy.rego", "content": ARTIFACT_POLICY }],
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
    fn an_operator_translation_applies_at_both_facets() {
        let policy = "package proof\n\nadvisory contains msg if { msg := \"noted\" }\n";
        let settings = serde_json::json!({
            "translation": [{ "key": "advisory", "severity": "info" }]
        });

        let graph = verdict(&graph_payload(&["a"], policy, Some(settings.clone())))
            .expect("the graph facet evaluates");
        assert_eq!(graph.results.len(), 1, "{:?}", graph.results);
        assert_eq!(graph.results[0].severity, SEVERITY_INFO);

        let rendered_payload = serde_json::json!({
            "artifacts": [],
            "sources": [{ "path": "policy.rego", "content": policy }],
            "settings": settings,
        });
        let rendered =
            verdict(&rendered_payload.to_string()).expect("the rendered facet evaluates");
        assert_eq!(rendered.results.len(), 1, "{:?}", rendered.results);
        assert_eq!(rendered.results[0].severity, SEVERITY_INFO);
    }

    #[test]
    fn a_malformed_translation_is_refused_rather_than_evaluated() {
        let settings = serde_json::json!({
            "translation": [{ "key": "blocked", "severity": "reject" }]
        });
        let err = verdict(&graph_payload(&["a"], GRAPH_POLICY, Some(settings)))
            .expect_err("a severity outside the closed set is refused");
        assert!(err.to_string().contains("reject"), "{err}");
    }

    #[test]
    fn the_verdict_is_repeatable() {
        let payload = graph_payload(&["a", "b"], GRAPH_POLICY, None);
        let run = || {
            serde_json::to_string(&verdict(&payload).expect("evaluation succeeds"))
                .expect("the output serializes")
        };
        assert_eq!(run(), run());
    }
}
