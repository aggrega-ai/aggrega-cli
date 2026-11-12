// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

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
//! The plugin serves all four governance roles, and the role an operator attaches
//! it at fixes both which payload arrives and what the plugin may report. Attached
//! to render a verdict it reports decisions that stop or annotate a build;
//! attached to shape a build it reports the change to make.
//!
//! The two verdict roles have one export each: `verdict_ir` reads the build graph
//! and `verdict_render` reads the rendered output files. The export the host
//! dispatches is what says which payload arrives, so each one reads exactly the
//! shape its own role sends.
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
//! Attached to shape labels, the `mutate` export reports which labels the build's
//! nodes should carry; attached to inject annotations, the `transform_annotations`
//! export reports which annotations the build's components should carry. Each
//! has its own decision key and its own input shape, both documented on the module
//! that reads them — [`labels`] and [`annotations`].
//!
//! In every case the attachment's own files are visible as `input.sources` and its
//! settings as `input.settings`, so an operator-supplied threshold is read as
//! `input.settings.<key>`. Both are always present as a value, empty when the
//! attachment declares none, so an expression addressing them does not have to
//! guard for their absence.
//!
//! # Authoring convention, and how to change it
//!
//! A policy is attached in either of two forms, and the file's name says which.
//! A policy set is as many files as it needs, and the two forms mix freely
//! within one.
//!
//! ## An expression, in a file named `.cel`
//!
//! An expression language has no rule names to collect decisions under, so the
//! decisions are the value each expression produces: a map from decision key to
//! what that key collects. At a verdict role a key collects the messages reached
//! under it; at a role that shapes the build it collects the decisions to apply.
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
//! A key a file omits reached no decision there, so a file need not name every
//! key — and each role reads only the key it is about, so one policy set can serve
//! a verdict role and a role that shapes the build from the same files. A key
//! carries a list of what it collects, a single one of them, or the null value for
//! none.
//!
//! ## A rules document, in a file named `.yaml` or `.yml`
//!
//! A rules document is a list of rules, each naming the decision key it reaches,
//! the expression that decides whether it is reached, and what is reported.
//!
//! ```yaml
//! - key: deny
//!   expression: size(input.graph.spec.nodes) > 20
//!   message: this build declares a lot of components
//! ```
//!
//! What a rule reports follows the key it reaches, exactly as it does in an
//! expression: text is the message a verdict carries, and a mapping is the fields
//! of the one decision a role that shapes the build reads.
//!
//! ```yaml
//! - key: labels
//!   expression: input.compilation_context.environment == "production"
//!   message:
//!     node: app
//!     key: compliance.example.com/tier
//!     value: gold
//! ```
//!
//! Which form fits depends on what is reported. A rules document suits a rule
//! whose report is fixed — written once, beside the expression that reaches it,
//! and read as a list of guards. An expression suits a rule whose report names
//! what it found: one expression can filter the components of a build and produce
//! a message, or a decision, per component — which a rules document cannot,
//! because each of its rules carries one report written in advance.
//!
//! A rules document is read as the expression it stands for before anything is
//! evaluated, so both forms are evaluated by the same evaluator and a rules
//! document decides exactly what the expression it stands for decides. The
//! translation and every way a document can be malformed are documented at
//! [`rules`].
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
//! Sorting within a key applies to both authored forms, because both are evaluated
//! by the same evaluator. So the messages of a rules document are reported sorted
//! rather than in the order its rules were written.
//!
//! At a role that shapes the build the same reasoning applies to the decisions
//! themselves, and the order is stronger: the decisions are reported in ascending
//! order of everything they name, which is a total order over what a decision can
//! express. That is what keeps a build's rendered output identical from one build
//! to the next, and each of the two roles documents its own order — see [`labels`]
//! and [`annotations`].
//!
//! # Failure is never a silent allow
//!
//! When a policy cannot be read, cannot be evaluated, or does not produce decisions
//! in the documented shape, the plugin fails instead of returning a verdict or a
//! change, and
//! that failure is reported as a build fault naming the file and, where it belongs
//! to one part of the file, the position within it. It is deliberately distinct
//! from a decision that stops the build: "this policy could not be evaluated" must
//! never read as "this policy allows the build".

mod annotations;
mod engine;
mod labels;
mod rules;

use aggrega_abi::{
    AnnotationTransformInput, AnnotationTransformOutput, MutatorInput, MutatorOutput,
    RenderPolicyInput,
};
use aggrega_pdk_rust::{
    describe, PluginDescriptor, PluginDescriptorMeta, PluginExport, PolicyInput, PolicyOutput,
    API_VERSION,
};
use extism_pdk::{plugin_fn, FnResult};
use governancecore::artifacts::render_input;
use governancecore::payload::settings_value;
use governancecore::sources::{project as project_sources, Source};
use governancecore::translation::{settings_schema, Translation};

/// The field a policy reads the attachment's settings through, and the field the
/// payload carries them in.
const SETTINGS_FIELD: &str = "settings";

/// The field a policy reads the attachment's own files through, and the field the
/// payload carries them in.
const SOURCES_FIELD: &str = "sources";

/// The static `describe()` self-report the host reads once when it loads the
/// plugin.
///
/// It declares one export per governance role, and the schema that types the
/// attachment's settings. It declares no capabilities: a governance plugin is not
/// selected by capability, and what it may emit is fixed by the role it is
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
        exports: vec![
            PluginExport {
                name: "verdict_ir".to_string(),
                ..Default::default()
            },
            PluginExport {
                name: "verdict_render".to_string(),
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

/// Evaluates the attached expressions against the build graph and reports the
/// decisions they reached. A failure to evaluate propagates, and is surfaced as a
/// build fault rather than as a decision.
///
/// Every governance role's payload and result travel as canonical CBOR, so each
/// export takes and returns bytes rather than text.
#[plugin_fn]
pub fn verdict_ir(input: Vec<u8>) -> FnResult<Vec<u8>> {
    let output = graph_verdict(&input)?;
    Ok(output.encode_cbor()?)
}

/// Evaluates the attached expressions against the rendered output files and
/// reports the decisions they reached. A failure to evaluate propagates, and is
/// surfaced as a build fault rather than as a decision.
#[plugin_fn]
pub fn verdict_render(input: Vec<u8>) -> FnResult<Vec<u8>> {
    let output = rendered_verdict(&input)?;
    Ok(output.encode_cbor()?)
}

/// Reads the build-graph payload and evaluates the expressions against it.
///
/// The whole payload is the input document, so a policy reads the graph and the
/// routing paths under the names the wire gives them; the evaluator reads a JSON
/// document, so the decoded payload is written back out as one. The settings are
/// written in as a value even when the attachment declared none: an expression
/// that reads an absent field fails, so leaving the field out would turn "this
/// attachment configured nothing" into a policy failure for every policy that
/// reads a threshold.
fn graph_verdict(raw: &[u8]) -> anyhow::Result<PolicyOutput> {
    let graph = PolicyInput::decode_cbor(raw)
        .map_err(|err| anyhow::anyhow!("reading the ir-verdict payload: {err}"))?;
    let translation = Translation::from_settings(graph.settings.as_deref())?;
    let sources = project_sources(&graph.sources);
    let mut input = serde_json::to_value(&graph)?;
    input[SETTINGS_FIELD] = settings_value(graph.settings.as_deref());
    verdict(&sources, &input, &translation)
}

/// Reads the rendered-output payload and evaluates the expressions against it.
///
/// This input document is assembled rather than passed through, because the
/// artifacts are projected into readable documents on the way. It already carries
/// the settings under the same name.
fn rendered_verdict(raw: &[u8]) -> anyhow::Result<PolicyOutput> {
    let rendered = RenderPolicyInput::decode_cbor(raw)
        .map_err(|err| anyhow::anyhow!("reading the render-verdict payload: {err}"))?;
    let translation = Translation::from_settings(rendered.settings.as_deref())?;
    let sources = project_sources(&rendered.sources);
    let input = render_input(&rendered);
    verdict(&sources, &input, &translation)
}

/// Evaluates the expressions against one input document and collects the
/// decisions. This is the body both verdict exports share: the payload each one
/// reads differs, and everything after the read is the same work.
fn verdict(
    sources: &[Source<'_>],
    input: &serde_json::Value,
    translation: &Translation,
) -> anyhow::Result<PolicyOutput> {
    Ok(PolicyOutput {
        results: engine::evaluate(sources, input, translation)?,
        ..Default::default()
    })
}

/// Reports which labels the build's nodes should carry, as the label changes the
/// build applies. A failure to evaluate propagates, and is surfaced as a build
/// fault rather than as a change.
#[plugin_fn]
pub fn mutate(input: Vec<u8>) -> FnResult<Vec<u8>> {
    let output = label_changes(&input)?;
    Ok(output.encode_cbor()?)
}

/// Evaluates the attached expressions against the node payload and reads their
/// label decisions.
fn label_changes(raw: &[u8]) -> anyhow::Result<MutatorOutput> {
    let input = MutatorInput::decode_cbor(raw)
        .map_err(|err| anyhow::anyhow!("reading the label-transform payload: {err}"))?;
    let document = transform_input(&input, &input.sources, input.settings.as_deref())?;
    let decisions = engine::decisions(&project_sources(&input.sources), &document, &labels::SHAPE)?;
    Ok(MutatorOutput {
        patches: labels::changes(&decisions)?,
        ..Default::default()
    })
}

/// Reports which annotations the build's components should carry. A failure
/// to evaluate propagates, and is surfaced as a build fault rather than as a
/// change.
#[plugin_fn]
pub fn transform_annotations(input: Vec<u8>) -> FnResult<Vec<u8>> {
    let output = annotation_injections(&input)?;
    Ok(output.encode_cbor()?)
}

/// Evaluates the attached expressions against the injection-target payload and
/// reads their annotation decisions.
fn annotation_injections(raw: &[u8]) -> anyhow::Result<AnnotationTransformOutput> {
    let input = AnnotationTransformInput::decode_cbor(raw)
        .map_err(|err| anyhow::anyhow!("reading the annotation-transform payload: {err}"))?;
    let document = transform_input(&input, &input.sources, input.settings.as_deref())?;
    let decisions = engine::decisions(
        &project_sources(&input.sources),
        &document,
        &annotations::SHAPE,
    )?;
    Ok(AnnotationTransformOutput {
        injections: annotations::injections(&decisions)?,
        ..Default::default()
    })
}

/// Builds the input document a transform role's expressions read.
///
/// The payload itself is the input document, so a policy reads the role's own
/// fields under the names the wire gives them. Two of those names are written back
/// in as values: the payload leaves an empty file list and absent settings out
/// altogether, and an expression that reads an absent field fails — so leaving
/// them out would turn "this attachment declared no settings" into a policy failure
/// for every policy that reads a threshold. Assembling the document from the
/// payload's own declared shape is what makes that guarantee reach every field
/// rather than the two named here.
fn transform_input<P, S>(
    payload: &P,
    sources: &[S],
    settings: Option<&serde_json::value::RawValue>,
) -> anyhow::Result<serde_json::Value>
where
    P: serde::Serialize,
    S: serde::Serialize,
{
    let mut document = serde_json::to_value(payload)?;
    document[SOURCES_FIELD] = serde_json::to_value(sources)?;
    document[SETTINGS_FIELD] = settings_value(settings);
    Ok(document)
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

    /// Builds a graph carrying the named nodes.
    ///
    /// The graph is built from the generated type's own defaults rather than
    /// hand-written, so a field added to the wire shape does not silently leave
    /// this fixture describing a graph the guest would reject.
    fn graph_with_nodes(names: &[&str]) -> aggrega_abi::IRGraph {
        let mut graph = aggrega_abi::IRGraph::default();
        for name in names {
            graph
                .spec
                .nodes
                .insert((*name).to_string(), aggrega_abi::IRNode::default());
        }
        graph
    }

    /// Settings as the payload carries them: the attachment's JSON, verbatim.
    fn raw_settings(settings: serde_json::Value) -> Box<serde_json::value::RawValue> {
        serde_json::value::RawValue::from_string(settings.to_string())
            .expect("test settings are valid JSON")
    }

    /// Builds a graph-facet payload: a graph with the named nodes, one policy file
    /// under the given name, and the given settings — encoded by the generated
    /// codec the host's own encoder agrees with byte for byte, so the export reads
    /// exactly what a dispatch hands it.
    fn graph_payload_at(
        path: &str,
        nodes: &[&str],
        policy: &str,
        settings: Option<serde_json::Value>,
    ) -> Vec<u8> {
        PolicyInput {
            graph: graph_with_nodes(nodes),
            precalculated_paths: Vec::new(),
            sources: vec![aggrega_abi::PolicySource {
                path: path.to_string(),
                content: policy.to_string(),
            }],
            settings: settings.map(raw_settings),
        }
        .encode_cbor()
        .expect("the payload encodes")
    }

    /// Builds a graph-facet payload: a graph with the named nodes, one policy
    /// source, and the given settings.
    fn graph_payload(nodes: &[&str], policy: &str, settings: Option<serde_json::Value>) -> Vec<u8> {
        graph_payload_at("policy.cel", nodes, policy, settings)
    }

    /// Builds a rendered-output payload: the given artifacts, one policy source,
    /// and the given settings.
    fn rendered_payload(
        artifacts: Vec<aggrega_abi::RenderedArtifact>,
        policy: &str,
        settings: Option<serde_json::Value>,
    ) -> Vec<u8> {
        RenderPolicyInput {
            artifacts,
            sources: vec![aggrega_abi::PolicySource {
                path: "policy.cel".to_string(),
                content: policy.to_string(),
            }],
            settings: settings.map(raw_settings),
        }
        .encode_cbor()
        .expect("the payload encodes")
    }

    /// One rendered file, optionally carrying the tree the host parsed it into.
    fn artifact(path: &str, content: &str, tree: Option<&str>, format: &str) -> aggrega_abi::RenderedArtifact {
        aggrega_abi::RenderedArtifact {
            path: path.to_string(),
            content: content.to_string(),
            tree: tree.map(|t| {
                serde_json::value::RawValue::from_string(t.to_string()).expect("test tree is valid JSON")
            }),
            format: format.to_string(),
        }
    }

    #[test]
    fn the_descriptor_declares_every_implemented_export() {
        let descriptor = descriptor();
        let declared: Vec<&str> = descriptor.exports.iter().map(|e| e.name.as_str()).collect();
        assert_eq!(
            declared,
            vec![
                "verdict_ir",
                "verdict_render",
                "mutate",
                "transform_annotations"
            ]
        );
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
        let output = graph_verdict(&graph_payload(&["a", "b"], GRAPH_POLICY, None))
            .expect("evaluation succeeds");
        assert_eq!(output.results.len(), 1, "{:?}", output.results);
        assert_eq!(output.results[0].severity, SEVERITY_HALT);
        assert_eq!(output.results[0].message, "more than one component");
        assert!(output.error.is_empty());
    }

    #[test]
    fn the_graph_facet_allows_when_no_decision_is_reached() {
        let output =
            graph_verdict(&graph_payload(&["a"], GRAPH_POLICY, None)).expect("evaluation succeeds");
        assert!(output.results.is_empty(), "{:?}", output.results);
    }

    #[test]
    fn the_rendered_facet_reads_the_artifacts() {
        let payload = rendered_payload(
            vec![artifact("svc.yaml", "kind: Service\n", None, "")],
            ARTIFACT_POLICY,
            None,
        );
        let output = rendered_verdict(&payload).expect("evaluation succeeds");
        assert_eq!(output.results.len(), 1, "{:?}", output.results);
        assert_eq!(
            output.results[0].message,
            "a service was rendered to svc.yaml"
        );
    }

    #[test]
    fn the_rendered_facet_reads_a_host_supplied_tree() {
        let payload = rendered_payload(
            vec![artifact(
                "main.tf",
                "resource \"x\" \"y\" {\n a = 1\n}\n",
                Some(r#"{ "kind": "Service" }"#),
                "hcl",
            )],
            ARTIFACT_POLICY,
            None,
        );
        let output = rendered_verdict(&payload).expect("evaluation succeeds");
        assert_eq!(
            output.results.len(),
            1,
            "the host-supplied tree is what the policy reads: {:?}",
            output.results
        );
    }

    #[test]
    fn the_rendered_facet_can_reject_on_a_file_it_could_not_read() {
        let payload = rendered_payload(
            vec![artifact("broken.yaml", "{ replicas: [1, 2\n", None, "")],
            r#"{"deny": input.artifacts.filter(a, has(a.parse_error)).map(a, "unreadable output at " + a.path)}"#,
            None,
        );
        let output = rendered_verdict(&payload).expect("evaluation succeeds");
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

        let graph = graph_verdict(&graph_payload(&["a"], policy, Some(settings.clone())))
            .expect("the graph facet evaluates");
        assert_eq!(graph.results.len(), 1, "{:?}", graph.results);
        assert_eq!(graph.results[0].severity, SEVERITY_INFO);

        let rendered = rendered_verdict(&rendered_payload(Vec::new(), policy, Some(settings)))
            .expect("the rendered facet evaluates");
        assert_eq!(rendered.results.len(), 1, "{:?}", rendered.results);
        assert_eq!(rendered.results[0].severity, SEVERITY_INFO);
    }

    #[test]
    fn a_malformed_translation_is_refused_before_the_policy_runs() {
        let settings = serde_json::json!({
            "translation": [{ "key": "blocked", "severity": "reject" }]
        });
        let err = graph_verdict(&graph_payload(&["a"], GRAPH_POLICY, Some(settings)))
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
        let output =
            graph_verdict(&graph_payload(&["a"], policy, None)).expect("evaluation succeeds");
        assert_eq!(output.results.len(), 1, "{:?}", output.results);
        assert_eq!(output.results[0].severity, SEVERITY_WARN);
        assert_eq!(output.results[0].message, "not configured");
    }

    #[test]
    fn an_evaluation_failure_is_a_build_fault_rather_than_a_verdict() {
        let err = graph_verdict(&graph_payload(
            &["a"],
            r#"{"deny": [input.graph.nope]}"#,
            None,
        ))
        .expect_err("an evaluation failure is not a verdict");
        assert!(err.to_string().contains("could not be evaluated"), "{err}");
    }

    #[test]
    fn the_graph_facet_reads_a_build_with_no_routing_paths() {
        // A build with nothing to enumerate carries an empty list of routing
        // paths, which is an ordinary build rather than a malformed payload.
        let output = graph_verdict(&graph_payload(&["a"], GRAPH_POLICY, None))
            .expect("evaluation succeeds");
        assert!(output.results.is_empty(), "{:?}", output.results);
    }

    #[test]
    fn a_graph_payload_that_cannot_be_read_fails_loudly() {
        let err = graph_verdict(&[0xff, 0xff, 0xff]).expect_err("unreadable bytes are refused");
        assert!(err.to_string().contains("ir-verdict payload"), "{err}");
    }

    #[test]
    fn a_rules_document_reaches_the_verdict_through_the_export_path() {
        let document = "\
- key: deny
  expression: size(input.graph.spec.nodes) > 1
  message: this build declares more than one component
";
        let output = graph_verdict(&payload_named("policy.yaml", &["a", "b"], document))
            .expect("a rules document evaluates");
        assert_eq!(output.results.len(), 1, "{:?}", output.results);
        assert_eq!(output.results[0].severity, SEVERITY_HALT);
        assert_eq!(
            output.results[0].message,
            "this build declares more than one component"
        );
        assert!(output.error.is_empty());
    }

    #[test]
    fn the_reported_output_is_byte_identical_between_the_two_authored_forms() {
        // What the host reads is the serialized output, so the equivalence of the
        // two authored forms is asserted on exactly that.
        let document = "\
- key: deny
  expression: size(input.graph.spec.nodes) > 1
  message: this build declares more than one component
- key: warn
  expression: size(input.graph.spec.nodes) > 2
  message: this build declares more than two components
";
        let expression = r#"{
          "deny": size(input.graph.spec.nodes) > 1
                    ? ["this build declares more than one component"] : [],
          "warn": size(input.graph.spec.nodes) > 2
                    ? ["this build declares more than two components"] : [],
        }"#;
        let nodes = ["web", "api", "worker"];
        let from_document = serde_json::to_string(
            &graph_verdict(&payload_named("policy.yaml", &nodes, document))
                .expect("the rules document evaluates"),
        )
        .expect("the output serializes");
        let from_expression = serde_json::to_string(
            &graph_verdict(&payload_named("policy.cel", &nodes, expression))
                .expect("the expression evaluates"),
        )
        .expect("the output serializes");
        assert_eq!(from_document, from_expression);
        assert!(
            from_document.contains("more than one component"),
            "the comparison is only worth something over decisions that were \
             reached: {from_document}"
        );
    }

    /// Builds a graph-facet payload carrying one policy file under the given name,
    /// so a test can choose which authored form the name selects.
    fn payload_named(path: &str, nodes: &[&str], policy: &str) -> Vec<u8> {
        graph_payload_at(path, nodes, policy, None)
    }

    /// The label-transform payload, as canonical bytes on the wire: a build of the
    /// named nodes, each carrying one label, with one policy file.
    ///
    /// It is built from the payload's own type and encoded the way the host encodes
    /// it, so the test drives the export through the same decoding a real dispatch
    /// does rather than around it.
    fn label_payload(nodes: &[&str], path: &str, policy: &str) -> Vec<u8> {
        let mut input = MutatorInput {
            compilation_context: aggrega_abi::CompilationContext {
                release_name: "rel".to_string(),
                environment: "testing".to_string(),
            },
            ..Default::default()
        };
        for name in nodes {
            let mut node = aggrega_abi::MutatorNode::default();
            node.labels.insert(
                "component.aggrega.ai/type".to_string(),
                "server".to_string(),
            );
            input.graph.nodes.insert((*name).to_string(), node);
        }
        input.sources.push(aggrega_abi::MutatorSource {
            path: path.to_string(),
            content: policy.to_string(),
        });
        input.encode_cbor().expect("the payload encodes")
    }

    /// The annotation-transform payload, as canonical bytes on the wire: the named
    /// components under one boundary, with one registered annotation key and one
    /// policy file.
    fn annotation_payload(components: &[&str], path: &str, policy: &str) -> Vec<u8> {
        let mut input = AnnotationTransformInput {
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
        input.sources.push(aggrega_abi::AnnotationSource {
            path: path.to_string(),
            content: policy.to_string(),
        });
        input.encode_cbor().expect("the payload encodes")
    }

    fn patch_documents(output: &MutatorOutput) -> Vec<serde_json::Value> {
        output
            .patches
            .iter()
            .map(|patch| serde_json::from_str(patch.get()).expect("a change is valid JSON"))
            .collect()
    }

    #[test]
    fn the_label_role_reports_the_changes_the_build_applies() {
        let output = label_changes(&label_payload(
            &["app"],
            "policy.cel",
            r#"{"labels": [{"node": "app", "key": "compliance.example.com/tier", "value": "gold"}]}"#,
        ))
        .expect("the decisions are read");
        assert_eq!(
            patch_documents(&output),
            vec![serde_json::json!([{
                "op": "add",
                "path": "/nodes/app/labels/compliance.example.com~1tier",
                "value": "gold",
            }])]
        );
        assert!(output.error.is_empty());
    }

    #[test]
    fn the_label_role_reads_the_deployment_environment() {
        let output = label_changes(&label_payload(
            &["app"],
            "policy.cel",
            r#"{"labels": input.graph.nodes.map(n, {
                 "node": n,
                 "key": "tier",
                 "value": input.compilation_context.environment,
               })}"#,
        ))
        .expect("the decisions are read");
        assert_eq!(
            patch_documents(&output)[0][0]["value"],
            serde_json::json!("testing")
        );
    }

    #[test]
    fn the_annotation_role_reports_the_injections_the_build_adds() {
        let output = annotation_injections(&annotation_payload(
            &["app"],
            "policy.cel",
            r#"{"annotations": input.targets
                 .filter(t, "gate.example.com/tier" in input.binding_keys)
                 .map(t, {
                   "boundary_path": t.boundary_path,
                   "component_name": t.component_name,
                   "annotation_key": "gate.example.com/tier",
                   "annotation_value": "gold",
                 })}"#,
        ))
        .expect("the decisions are read");
        assert_eq!(output.injections.len(), 1, "{:?}", output.injections);
        assert_eq!(output.injections[0].component_name, "app");
        assert_eq!(output.injections[0].annotation_key, "gate.example.com/tier");
        assert!(output.error.is_empty());
    }

    #[test]
    fn a_rules_document_reaches_both_transform_roles() {
        // The two authored forms share one intake, and what a rule reports follows
        // the key it reaches — so a rules document decides labels and annotations
        // as well as verdicts.
        let labels = label_changes(&label_payload(
            &["app"],
            "policy.yaml",
            "- key: labels\n  expression: size(input.graph.nodes) > 0\n  message:\n    \
             node: app\n    key: tier\n    value: gold\n",
        ))
        .expect("a rules document decides labels");
        assert_eq!(
            patch_documents(&labels)[0][0]["path"],
            serde_json::json!("/nodes/app/labels/tier")
        );

        let annotations = annotation_injections(&annotation_payload(
            &["app"],
            "policy.yaml",
            "- key: annotations\n  expression: size(input.targets) > 0\n  message:\n    \
             boundary_path: main\n    component_name: app\n    \
             annotation_key: gate.example.com/tier\n    annotation_value: gold\n",
        ))
        .expect("a rules document decides annotations");
        assert_eq!(
            annotations.injections.len(),
            1,
            "{:?}",
            annotations.injections
        );
        assert_eq!(annotations.injections[0].annotation_value.get(), "\"gold\"");
    }

    #[test]
    fn the_annotation_role_reports_a_map_value_and_reads_a_structured_target() {
        // A policy decides a map of subkeys, and reads a target's existing
        // structured annotation to leave a component that already carries one alone.
        let mut input = AnnotationTransformInput {
            binding_keys: vec!["feature.aggrega.ai/monitoring".to_string()],
            ..Default::default()
        };
        for name in ["app", "db"] {
            input.targets.push(aggrega_abi::AnnotationTarget {
                boundary_path: "main".to_string(),
                component_name: name.to_string(),
                ..Default::default()
            });
        }
        input.targets[1].annotations.insert(
            "feature.aggrega.ai/monitoring".to_string(),
            serde_json::value::to_raw_value(&serde_json::json!({"interval": "5s"}))
                .expect("a raw value"),
        );
        input.sources.push(aggrega_abi::AnnotationSource {
            path: "policy.cel".to_string(),
            content: r#"{"annotations": input.targets
                 .filter(t, !("feature.aggrega.ai/monitoring" in t.annotations))
                 .map(t, {
                   "boundary_path": t.boundary_path,
                   "component_name": t.component_name,
                   "annotation_key": "feature.aggrega.ai/monitoring",
                   "annotation_value": {"port": "metrics", "interval": "30s", "path": "/metrics"},
                 })}"#
                .to_string(),
        });
        let output = annotation_injections(&input.encode_cbor().expect("the payload encodes"))
            .expect("the decisions are read");
        assert_eq!(output.injections.len(), 1, "{:?}", output.injections);
        assert_eq!(output.injections[0].component_name, "app");
        assert_eq!(
            output.injections[0].annotation_value.get(),
            r#"{"interval":"30s","path":"/metrics","port":"metrics"}"#
        );
    }

    #[test]
    fn the_reported_changes_are_byte_identical_between_the_two_authored_forms() {
        // What the host reads is the encoded output, so the equivalence of the two
        // forms is asserted on exactly that.
        let document = "- key: labels\n  expression: size(input.graph.nodes) > 1\n  message:\n    \
                        node: app\n    key: tier\n    value: gold\n";
        let expression = r#"{"labels": size(input.graph.nodes) > 1
                              ? [{"node": "app", "key": "tier", "value": "gold"}] : []}"#;
        let nodes = ["app", "db"];
        let from_document = label_changes(&label_payload(&nodes, "policy.yaml", document))
            .expect("the rules document decides")
            .encode_cbor()
            .expect("the output encodes");
        let from_expression = label_changes(&label_payload(&nodes, "policy.cel", expression))
            .expect("the expression decides")
            .encode_cbor()
            .expect("the output encodes");
        assert_eq!(from_document, from_expression);
        assert!(
            !from_document.is_empty()
                && !patch_documents(
                    &label_changes(&label_payload(&nodes, "policy.yaml", document))
                        .expect("the rules document decides")
                )
                .is_empty(),
            "the comparison is only worth something over a decision that was reached"
        );
    }

    #[test]
    fn a_transform_policy_reading_settings_the_attachment_did_not_declare_still_evaluates() {
        // Settings are optional at every role, and an expression that reads an
        // absent field fails — so the field is present as a value even when the
        // payload left it out.
        let output = label_changes(&label_payload(
            &["app"],
            "policy.cel",
            r#"{"labels": has(input.settings.tier)
                 ? [{"node": "app", "key": "tier", "value": input.settings.tier}] : []}"#,
        ))
        .expect("a policy guarding on its own settings evaluates");
        assert!(output.patches.is_empty(), "{:?}", output.patches);
    }

    #[test]
    fn a_transform_payload_that_cannot_be_read_fails_loudly() {
        let err = label_changes(&[0xff, 0xff, 0xff]).expect_err("unreadable bytes are refused");
        assert!(err.to_string().contains("label-transform payload"), "{err}");
    }

    #[test]
    fn a_malformed_transform_decision_is_a_build_fault_rather_than_a_change() {
        let err = label_changes(&label_payload(
            &["app"],
            "policy.cel",
            r#"{"labels": ["a message, not a decision"]}"#,
        ))
        .expect_err("a decision of the wrong shape is not a change");
        let message = err.to_string();
        assert!(message.contains("policy.cel"), "{message}");
        assert!(message.contains("\"node\""), "{message}");
    }

    #[test]
    fn one_policy_file_can_serve_a_verdict_role_and_a_transform_role() {
        // Each role reads only the key it is about, so a policy set need not be
        // split by role.
        const POLICY: &str = r#"{
          "deny": ["a verdict"],
          "labels": [{"node": "app", "key": "tier", "value": "gold"}],
        }"#;
        let verdict =
            graph_verdict(&graph_payload(&["app"], POLICY, None)).expect("the verdict is read");
        assert_eq!(verdict.results.len(), 1, "{:?}", verdict.results);
        assert_eq!(verdict.results[0].message, "a verdict");

        let changes = label_changes(&label_payload(&["app"], "policy.cel", POLICY))
            .expect("the label decisions are read");
        assert_eq!(changes.patches.len(), 1, "{:?}", changes.patches);
    }

    #[test]
    fn identical_input_yields_an_identical_report() {
        // The whole output, serialized, is what the host reads — so the property
        // asserted is byte equality of that, not just the message set.
        let policy = r#"{"deny": input.graph.spec.nodes.map(n, "component " + n)}"#;
        let payload = graph_payload(&["delta", "alpha", "charlie", "bravo"], policy, None);
        let first = serde_json::to_string(&graph_verdict(&payload).expect("evaluation succeeds"))
            .expect("the output serializes");
        let second = serde_json::to_string(&graph_verdict(&payload).expect("evaluation succeeds"))
            .expect("the output serializes");
        assert_eq!(first, second);
        assert!(
            first.contains("component alpha")
                && first.find("component alpha") < first.find("component bravo"),
            "the report is in sorted order: {first}"
        );
    }
}
