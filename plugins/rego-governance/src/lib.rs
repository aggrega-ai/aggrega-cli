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
//! The plugin serves all four governance roles, and the role an operator
//! attaches it at fixes both which payload arrives and what the plugin may
//! report. Attached to render a verdict it reports decisions that stop or
//! annotate a build; attached to shape a build it reports the change to make.
//!
//! One `validate` export serves both verdict roles, because the role fixes which
//! payload arrives — the build graph at one role, the rendered output files at
//! the other. The plugin reads the payload it was given and never declares which
//! one it expects.
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
//! Attached to shape labels, the `mutate` export reports which labels the
//! build's nodes should carry; attached to inject annotations, the
//! `transform_annotations` export reports which annotations the deployment's
//! components should carry. Each has its own decision key and its own input
//! shape, both documented on the module that reads them.
//!
//! In every case the attachment's own files are visible as `input.sources` and
//! its settings as `input.settings`, and the settings are also the policy's
//! `data` document, so a policy reads an operator-supplied threshold as
//! `data.<key>`.
//!
//! # Attaching a whole policy set as one archive
//!
//! A policy set can be attached file by file, or as a single archive carrying its
//! policy files and its data documents together. Because an attached file's
//! content travels as text, an archive is attached base64-encoded and named so
//! that its name ends in `.base64`; the plugin decodes it, decompresses it and
//! reads its members. An archive that cannot be opened is reported as such,
//! naming the file and the step that failed — never as a policy set that reached
//! no decision.
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
//! A translation maps a decision key onto a severity, and only a verdict carries
//! a severity — so a translation applies where the plugin reports verdicts and
//! has nothing to configure where it reports a change. The two change-reporting
//! roles read one fixed decision key each.
//!
//! # Failure is never a silent allow
//!
//! When a policy cannot be evaluated — most often because it calls a builtin this
//! engine does not implement — the plugin fails instead of returning a verdict,
//! and that failure is reported as a build fault naming the plugin. It is
//! deliberately distinct from a decision that stops the build: "this policy could
//! not be evaluated" must never read as "this policy allows the build".

mod annotations;
mod artifacts;
mod bundle;
mod engine;
mod labels;
mod payload;
mod translation;

use aggrega_abi::{
    AnnotationTransformInput, AnnotationTransformOutput, MutatorInput, MutatorOutput,
    RenderPolicyInput,
};
use aggrega_pdk_rust::{
    describe, PluginDescriptor, PluginDescriptorMeta, PluginExport, PolicyInput, PolicyOutput,
    API_VERSION,
};
use extism_pdk::{plugin_fn, FnResult};

use engine::Source;
use translation::Translation;

/// The static `describe()` self-report the host reads once when it loads the
/// plugin.
///
/// It declares the three exports the four governance roles dispatch to, and the
/// schema that types the attachment's settings. It declares no capabilities: a
/// governance plugin is not selected by capability, and what it may emit is
/// fixed by the role it is attached at.
///
/// It declares exactly the exports this plugin implements. The host refuses to
/// call an export a plugin did not declare, so an attachment expecting an export
/// that is absent is refused by name rather than silently doing nothing — which
/// is why an export is declared here in the same change that implements it.
fn descriptor() -> PluginDescriptor {
    PluginDescriptor {
        abi_version: API_VERSION.to_string(),
        plugin_type: "governance".to_string(),
        description:
            "Evaluate operator-authored Rego against a build with an embedded Rego engine."
                .to_string(),
        exports: vec![
            PluginExport {
                name: "validate".to_string(),
                ..Default::default()
            },
            PluginExport {
                name: "mutate".to_string(),
                ..Default::default()
            },
            PluginExport {
                name: "transform_annotations".to_string(),
                ..Default::default()
            },
        ],
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
        let rendered: RenderPolicyInput = payload::read(&payload, &["artifacts"])?;
        let settings = rendered.settings.as_deref();
        let translation = Translation::from_settings(settings)?;
        let input = artifacts::rego_input(&rendered);
        engine::evaluate(&sources(&rendered.sources), settings, &input, &translation)?
    } else {
        let graph: PolicyInput = payload::read(&payload, &["precalculated_paths"])?;
        let settings = graph.settings.as_deref();
        let translation = Translation::from_settings(settings)?;
        // The whole payload is the policy's input document, so a policy reads
        // the graph and the routing paths under the names the wire gives them.
        engine::evaluate(&sources(&graph.sources), settings, &payload, &translation)?
    };
    Ok(PolicyOutput {
        results,
        ..Default::default()
    })
}

/// Reports which labels the build's nodes should carry, as the label changes the
/// build applies. A failure to evaluate propagates, and is surfaced as a build
/// fault rather than as a change.
#[plugin_fn]
pub fn mutate(input: String) -> FnResult<String> {
    let output = label_changes(&input)?;
    Ok(serde_json::to_string(&output)?)
}

/// Evaluates the attached policies against the node payload and reads their label
/// decisions.
///
/// The whole payload is the policy's input document, so a policy reads the nodes
/// and the deployment context under the names the wire gives them.
fn label_changes(raw: &str) -> anyhow::Result<MutatorOutput> {
    let payload: serde_json::Value = serde_json::from_str(raw)?;
    let input: MutatorInput = payload::read(&payload, &[])?;
    let decisions = engine::decisions(
        &sources(&input.sources),
        input.settings.as_deref(),
        &payload,
        labels::DECISION_KEY,
    )?;
    Ok(MutatorOutput {
        patches: labels::changes(&decisions)?,
        ..Default::default()
    })
}

/// Reports which annotations the deployment's components should carry. A failure
/// to evaluate propagates, and is surfaced as a build fault rather than as a
/// change.
#[plugin_fn]
pub fn transform_annotations(input: String) -> FnResult<String> {
    let output = annotation_injections(&input)?;
    Ok(serde_json::to_string(&output)?)
}

/// Evaluates the attached policies against the injection-target payload and reads
/// their annotation decisions.
///
/// The whole payload is the policy's input document, so a policy reads the
/// targets and the registered annotation keys under the names the wire gives
/// them.
fn annotation_injections(raw: &str) -> anyhow::Result<AnnotationTransformOutput> {
    let payload: serde_json::Value = serde_json::from_str(raw)?;
    let input: AnnotationTransformInput = payload::read(&payload, &["targets", "binding_keys"])?;
    let decisions = engine::decisions(
        &sources(&input.sources),
        input.settings.as_deref(),
        &payload,
        annotations::DECISION_KEY,
    )?;
    Ok(AnnotationTransformOutput {
        injections: annotations::injections(&decisions)?,
        ..Default::default()
    })
}

/// Projects a role's own source entries into the view the evaluator reads.
///
/// Each role carries its own source type with the same two fields, so this is
/// the one place the four payload shapes converge on a single reader — the
/// alternative would be four copies of the loading rules, one per role.
fn sources<'a, S>(entries: &'a [S]) -> Vec<Source<'a>>
where
    Source<'a>: From<&'a S>,
{
    entries.iter().map(Source::from).collect()
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::bundle::encoded_archive;
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
    fn the_descriptor_declares_every_implemented_export() {
        let descriptor = descriptor();
        let declared: Vec<&str> = descriptor.exports.iter().map(|e| e.name.as_str()).collect();
        assert_eq!(
            declared,
            vec!["validate", "mutate", "transform_annotations"]
        );
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
        let output =
            verdict(&graph_payload(&["a", "b"], GRAPH_POLICY, None)).expect("evaluation succeeds");
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

    /// Builds a graph-facet payload carrying the given attached files verbatim,
    /// so a test can attach an archive where another attaches policy text.
    fn graph_payload_with_sources(nodes: &[&str], sources: serde_json::Value) -> String {
        serde_json::json!({
            "graph": graph_with_nodes(nodes),
            "precalculated_paths": [],
            "sources": sources,
        })
        .to_string()
    }

    /// Builds a node payload for the label-shaping role: the named nodes, each
    /// carrying the given component type label, plus one attached policy file.
    ///
    /// Built from the generated type's own defaults rather than hand-written
    /// JSON, so a field added to the wire shape does not leave this fixture
    /// describing a payload the plugin would reject.
    fn label_payload(nodes: &[(&str, &str)], policy: &str) -> String {
        let mut input = MutatorInput {
            compilation_context: aggrega_abi::CompilationContext {
                release_name: "rel".to_string(),
                environment: "testing".to_string(),
            },
            ..Default::default()
        };
        for (id, kind) in nodes {
            let mut node = aggrega_abi::MutatorNode {
                component: "test/svc".to_string(),
                resolved_version: "1.0.0".to_string(),
                ..Default::default()
            };
            node.labels
                .insert("component.aggrega.ai/type".to_string(), (*kind).to_string());
            input.graph.nodes.insert((*id).to_string(), node);
        }
        input.sources = vec![aggrega_abi::MutatorSource {
            path: "policy.rego".to_string(),
            content: policy.to_string(),
        }];
        serde_json::to_string(&input).expect("the payload serializes")
    }

    /// Builds an injection-target payload for the annotation-injecting role.
    fn annotation_payload(targets: &[(&str, &str)], binding_keys: &[&str], policy: &str) -> String {
        let input = AnnotationTransformInput {
            targets: targets
                .iter()
                .map(|(boundary, component)| aggrega_abi::AnnotationTarget {
                    boundary_path: (*boundary).to_string(),
                    component_name: (*component).to_string(),
                    ..Default::default()
                })
                .collect(),
            binding_keys: binding_keys.iter().map(|k| (*k).to_string()).collect(),
            sources: vec![aggrega_abi::AnnotationSource {
                path: "policy.rego".to_string(),
                content: policy.to_string(),
            }],
            settings: None,
        };
        serde_json::to_string(&input).expect("the payload serializes")
    }

    #[test]
    fn the_label_role_reports_the_labels_a_policy_decided() {
        const POLICY: &str = r#"
package tagging

labels contains {"node": id, "key": "compliance.example.com/tier", "value": "gold"} if {
    some id, node in input.graph.nodes
    node.labels["component.aggrega.ai/type"] == "server"
}
"#;
        let output = label_changes(&label_payload(&[("app", "server")], POLICY))
            .expect("the policy evaluates");
        assert_eq!(output.patches.len(), 1, "{:?}", output.patches);
        let change: serde_json::Value = serde_json::from_str(output.patches[0].get())
            .expect("the emitted change is valid JSON");
        assert_eq!(
            change,
            serde_json::json!([{
                "op": "add",
                "path": "/nodes/app/labels/compliance.example.com~1tier",
                "value": "gold",
            }])
        );
        assert!(output.error.is_empty());
    }

    #[test]
    fn the_label_role_reports_nothing_when_a_policy_decides_nothing() {
        const POLICY: &str = r#"
package tagging

labels contains {"node": id, "key": "k", "value": "v"} if {
    some id, node in input.graph.nodes
    node.labels["component.aggrega.ai/type"] == "resource"
}
"#;
        let output = label_changes(&label_payload(&[("app", "server")], POLICY))
            .expect("the policy evaluates");
        assert!(output.patches.is_empty(), "{:?}", output.patches);
    }

    #[test]
    fn the_label_role_reads_the_deployment_environment() {
        const POLICY: &str = r#"
package tagging

labels contains {"node": id, "key": "env", "value": input.compilation_context.environment} if {
    some id, _ in input.graph.nodes
}
"#;
        let output = label_changes(&label_payload(&[("app", "server")], POLICY))
            .expect("the policy evaluates");
        let change: serde_json::Value = serde_json::from_str(output.patches[0].get())
            .expect("the emitted change is valid JSON");
        assert_eq!(change[0]["value"], serde_json::json!("testing"));
    }

    #[test]
    fn the_label_role_fails_rather_than_reporting_no_change() {
        // A policy that cannot be evaluated must not read as a policy that asked
        // for nothing, which would let a build proceed unshaped and unremarked.
        const POLICY: &str = r#"
package tagging

labels contains {"node": id, "key": "k", "value": "v"} if {
    some id, _ in input.graph.nodes
    net.lookup_ip_addr("example.com")
}
"#;
        let outcome = label_changes(&label_payload(&[("app", "server")], POLICY));
        match outcome {
            Err(_) => {}
            Ok(output) => assert!(
                !output.patches.is_empty(),
                "an unimplemented builtin must not produce a silent no-change"
            ),
        }
    }

    #[test]
    fn the_annotation_role_reports_the_annotations_a_policy_decided() {
        const POLICY: &str = r#"
package tagging

annotations contains {
    "boundary_path": target.boundary_path,
    "component_name": target.component_name,
    "annotation_key": "backup.example.com/schedule",
    "annotation_value": "nightly",
} if {
    some target in input.targets
    "backup.example.com/schedule" in input.binding_keys
}
"#;
        let output = annotation_injections(&annotation_payload(
            &[("main", "app")],
            &["backup.example.com/schedule"],
            POLICY,
        ))
        .expect("the policy evaluates");
        assert_eq!(output.injections.len(), 1, "{:?}", output.injections);
        assert_eq!(output.injections[0].boundary_path, "main");
        assert_eq!(output.injections[0].component_name, "app");
        assert_eq!(
            output.injections[0].annotation_key,
            "backup.example.com/schedule"
        );
        assert_eq!(output.injections[0].annotation_value, "nightly");
        assert!(output.error.is_empty());
    }

    #[test]
    fn the_annotation_role_reads_the_registered_keys_and_can_decline() {
        // The same policy, with the key absent from the registered list, reaches
        // no decision — proving the registered list is really read.
        const POLICY: &str = r#"
package tagging

annotations contains {
    "boundary_path": target.boundary_path,
    "component_name": target.component_name,
    "annotation_key": "backup.example.com/schedule",
    "annotation_value": "nightly",
} if {
    some target in input.targets
    "backup.example.com/schedule" in input.binding_keys
}
"#;
        let output = annotation_injections(&annotation_payload(&[("main", "app")], &[], POLICY))
            .expect("the policy evaluates");
        assert!(output.injections.is_empty(), "{:?}", output.injections);
    }

    #[test]
    fn the_annotation_role_reports_nothing_when_no_target_matches() {
        const POLICY: &str = r#"
package tagging

annotations contains {
    "boundary_path": target.boundary_path,
    "component_name": target.component_name,
    "annotation_key": "k",
    "annotation_value": "v",
} if {
    some target in input.targets
    target.component_name == "absent"
}
"#;
        let output = annotation_injections(&annotation_payload(&[("main", "app")], &[], POLICY))
            .expect("the policy evaluates");
        assert!(output.injections.is_empty(), "{:?}", output.injections);
    }

    #[test]
    fn an_archive_evaluates_identically_to_the_same_files_attached_separately() {
        const POLICY: &str = r#"
package proof

deny contains msg if {
    count(input.graph.spec.nodes) > data.limits.max_nodes
    msg := "more nodes than the limit allows"
}
"#;
        const DATA: &str = r#"{"limits":{"max_nodes":1}}"#;

        // Attached separately: the policy file, plus the same data as settings,
        // which is the only way to supply data without an archive.
        let separate = serde_json::json!({
            "graph": graph_with_nodes(&["a", "b"]),
            "precalculated_paths": [],
            "sources": [{ "path": "policy.rego", "content": POLICY }],
            "settings": serde_json::from_str::<serde_json::Value>(DATA).expect("valid"),
        })
        .to_string();
        let from_separate = verdict(&separate).expect("the separate files evaluate");

        // Attached as one archive carrying both.
        let archive = encoded_archive(&[("data.json", DATA), ("policy.rego", POLICY)]);
        let from_archive = verdict(&graph_payload_with_sources(
            &["a", "b"],
            serde_json::json!([{ "path": "bundle.tar.gz.base64", "content": archive }]),
        ))
        .expect("the archive evaluates");

        assert_eq!(
            serde_json::to_string(&from_separate).expect("serializes"),
            serde_json::to_string(&from_archive).expect("serializes"),
            "an archive and the same files attached separately must reach the same decisions"
        );
        assert_eq!(from_archive.results.len(), 1, "{:?}", from_archive.results);
        assert_eq!(from_archive.results[0].severity, SEVERITY_HALT);
    }

    #[test]
    fn an_archive_serves_the_label_role_too() {
        const POLICY: &str = r#"
package tagging

labels contains {"node": id, "key": "tier", "value": data.tier} if {
    some id, _ in input.graph.nodes
}
"#;
        let archive = encoded_archive(&[("data.json", r#"{"tier":"gold"}"#), ("p.rego", POLICY)]);
        let mut input = MutatorInput {
            compilation_context: aggrega_abi::CompilationContext {
                release_name: "rel".to_string(),
                environment: "testing".to_string(),
            },
            ..Default::default()
        };
        input
            .graph
            .nodes
            .insert("app".to_string(), aggrega_abi::MutatorNode::default());
        input.sources = vec![aggrega_abi::MutatorSource {
            path: "bundle.tar.gz.base64".to_string(),
            content: archive,
        }];
        let output = label_changes(&serde_json::to_string(&input).expect("serializes"))
            .expect("the archive evaluates");
        let change: serde_json::Value = serde_json::from_str(output.patches[0].get())
            .expect("the emitted change is valid JSON");
        assert_eq!(change[0]["value"], serde_json::json!("gold"));
    }

    #[test]
    fn a_damaged_archive_fails_rather_than_evaluating_nothing() {
        let payload = graph_payload_with_sources(
            &["a"],
            serde_json::json!([{
                "path": "bundle.tar.gz.base64",
                "content": "not an archive at all ###",
            }]),
        );
        let err = verdict(&payload).expect_err("a damaged archive is refused");
        let message = err.to_string();
        assert!(message.contains("bundle.tar.gz.base64"), "{message}");
    }

    #[test]
    fn a_file_not_named_as_an_archive_is_read_as_policy_text() {
        // The name is the discriminator, so a plain policy file is never
        // mistaken for an archive however its content reads.
        let payload = graph_payload_with_sources(
            &["a", "b"],
            serde_json::json!([{ "path": "policy.rego", "content": GRAPH_POLICY }]),
        );
        let output = verdict(&payload).expect("policy text evaluates");
        assert_eq!(output.results.len(), 1, "{:?}", output.results);
    }

    #[test]
    fn every_role_is_repeatable() {
        const LABEL_POLICY: &str = r#"
package tagging

labels contains {"node": id, "key": "a/b", "value": "1"} if { some id, _ in input.graph.nodes }
"#;
        const ANNOTATION_POLICY: &str = r#"
package tagging

annotations contains {
    "boundary_path": target.boundary_path,
    "component_name": target.component_name,
    "annotation_key": "k",
    "annotation_value": "v",
} if { some target in input.targets }
"#;
        let label = label_payload(&[("app", "server"), ("db", "server")], LABEL_POLICY);
        let run_labels = || {
            serde_json::to_string(&label_changes(&label).expect("evaluates")).expect("serializes")
        };
        assert_eq!(run_labels(), run_labels());

        let annotation = annotation_payload(
            &[("main", "app"), ("main", "db")],
            &["k"],
            ANNOTATION_POLICY,
        );
        let run_annotations = || {
            serde_json::to_string(&annotation_injections(&annotation).expect("evaluates"))
                .expect("serializes")
        };
        assert_eq!(run_annotations(), run_annotations());
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
