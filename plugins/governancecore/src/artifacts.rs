// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

//! Projection of rendered artifacts into the document shape a policy reads.
//!
//! Nothing here names a policy engine: the projection is what every engine's
//! rendered-output facet reads, so it has one home rather than one per engine.
//!
//! A policy reasoning over rendered output wants the structured document, not the
//! raw text: a rule reads a container image or a replica count out of a parsed
//! object rather than by matching on the rendered characters. This module builds
//! that view.
//!
//! Where the parsing happens. The host parses an artifact whose format the guest
//! cannot read natively and hands the result over as the artifact's tree, so the
//! guest reads that tree rather than carrying a second parser for the same
//! format. An artifact arriving without a tree is one the host left for the guest
//! to read, and YAML covers those: YAML is a superset of JSON, so one reader
//! handles both, and a multi-document file yields its documents in order.
//!
//! A parse failure never allows. When an artifact does not parse, it stays
//! visible with its raw content and an accompanying parse message, and it
//! contributes no documents. Dropping it would let an unparseable artifact read
//! as compliant; surfacing it lets a policy reject on the failure itself.

use aggrega_abi::RenderedArtifact;
use serde_json::json;

/// The projected view of one rendered artifact: its path, its format, its raw
/// content, the structured documents it holds, and a parse message when it holds
/// none because it could not be read.
fn project(artifact: &RenderedArtifact) -> serde_json::Value {
    let mut entry = json!({
        "path": artifact.path,
        "format": artifact.format,
        "content": artifact.content,
    });
    match documents(artifact) {
        Ok(documents) => {
            entry["documents"] = serde_json::Value::Array(documents);
        }
        Err(err) => {
            entry["documents"] = serde_json::Value::Array(Vec::new());
            entry["parse_error"] = serde_json::Value::String(err.to_string());
        }
    }
    entry
}

/// The structured documents of one artifact.
///
/// A tree the host supplied is the artifact's parsed form already, so it is the
/// single document and nothing re-reads the content. Otherwise the content is
/// read as a YAML or JSON document stream.
fn documents(artifact: &RenderedArtifact) -> anyhow::Result<Vec<serde_json::Value>> {
    if let Some(tree) = artifact.tree.as_deref() {
        let parsed: serde_json::Value = serde_json::from_str(tree.get())?;
        return Ok(vec![parsed]);
    }
    parse_documents(&artifact.content)
}

/// Reads content as a stream of YAML or JSON documents, in document order.
///
/// An empty document — a bare separator, or trailing whitespace after the last
/// document — carries nothing for a policy to read and is skipped, so a
/// separator-terminated file yields the documents it actually declares.
fn parse_documents(content: &str) -> anyhow::Result<Vec<serde_json::Value>> {
    let mut documents = Vec::new();
    for document in serde_yaml::Deserializer::from_str(content) {
        let value: serde_json::Value = serde_path_to_value(document)?;
        if value.is_null() {
            continue;
        }
        documents.push(value);
    }
    Ok(documents)
}

/// Deserializes one YAML document into the JSON value model.
fn serde_path_to_value(
    document: serde_yaml::Deserializer<'_>,
) -> anyhow::Result<serde_json::Value> {
    Ok(serde::Deserialize::deserialize(document)?)
}

/// Builds the input document for the rendered-artifact facet: every artifact
/// with its projected documents, plus the attachment's own sources and settings,
/// so the whole payload is visible to a policy.
///
/// The field names are the names a policy addresses, and they are the same names
/// whichever engine evaluates it — which is why this assembly lives here rather
/// than in an engine adapter. A policy moved between engines reads
/// `artifacts`, `sources` and `settings` in both.
///
/// Artifacts keep the order the host supplied, which is sorted by path, and the
/// documents within each artifact keep document order — so identical input yields
/// an identical input document.
pub fn render_input(payload: &aggrega_abi::RenderPolicyInput) -> serde_json::Value {
    let artifacts: Vec<serde_json::Value> = payload.artifacts.iter().map(project).collect();
    let sources: Vec<serde_json::Value> = payload
        .sources
        .iter()
        .map(|source| json!({ "path": source.path, "content": source.content }))
        .collect();
    json!({
        "artifacts": artifacts,
        "sources": sources,
        "settings": crate::payload::settings_value(payload.settings.as_deref()),
    })
}

#[cfg(test)]
mod tests {
    use super::*;
    use serde_json::value::RawValue;

    fn artifact(path: &str, content: &str, tree: Option<&str>) -> RenderedArtifact {
        RenderedArtifact {
            path: path.to_string(),
            content: content.to_string(),
            tree: tree.map(|t| RawValue::from_string(t.to_string()).expect("test JSON is valid")),
            format: String::new(),
        }
    }

    #[test]
    fn a_host_supplied_tree_is_read_rather_than_the_content() {
        // The content is deliberately unreadable as YAML or JSON, so a document
        // can only have come from the tree.
        let art = artifact(
            "main.tf",
            "resource \"aws_s3_bucket\" \"b\" {\n  acl = \"private\"\n}\n",
            Some(r#"{"resource":{"aws_s3_bucket":{"b":{"acl":"private"}}}}"#),
        );
        let projected = project(&art);
        assert_eq!(
            projected["documents"][0]["resource"]["aws_s3_bucket"]["b"]["acl"],
            serde_json::json!("private")
        );
        assert!(projected.get("parse_error").is_none(), "{projected}");
    }

    #[test]
    fn an_artifact_without_a_tree_is_read_as_a_document_stream() {
        let art = artifact("k8s.yaml", "kind: Service\n---\nkind: Deployment\n", None);
        let projected = project(&art);
        assert_eq!(
            projected["documents"][0]["kind"],
            serde_json::json!("Service")
        );
        assert_eq!(
            projected["documents"][1]["kind"],
            serde_json::json!("Deployment")
        );
    }

    #[test]
    fn json_content_reads_through_the_same_reader() {
        let art = artifact("values.json", r#"{"replicas":3}"#, None);
        let projected = project(&art);
        assert_eq!(projected["documents"][0]["replicas"], serde_json::json!(3));
    }

    #[test]
    fn an_unreadable_artifact_stays_visible_and_contributes_no_documents() {
        // An unterminated flow mapping is not a readable document in either
        // format the guest reads.
        let content = "{ replicas: [1, 2\n";
        let art = artifact("values.yaml", content, None);
        let projected = project(&art);
        assert_eq!(projected["documents"], serde_json::json!([]));
        assert!(
            projected["parse_error"].is_string(),
            "the read failure is surfaced: {projected}"
        );
        assert_eq!(
            projected["content"],
            serde_json::json!(content),
            "the raw content stays readable"
        );
    }

    #[test]
    fn content_the_host_left_unparsed_and_that_is_not_a_document_reads_as_a_scalar() {
        // A format the guest does not read natively arrives with a tree, so this
        // path is only reached for content that is nominally YAML or JSON. Such
        // content can still read as a bare scalar rather than a mapping, and the
        // scalar is reported as the document it is rather than being dropped: a
        // policy indexing into it finds nothing, which is a visible absence, and
        // it can also read the scalar directly.
        let art = artifact("main.tf", "resource \"x\" \"y\" { a = 1 }", None);
        let projected = project(&art);
        assert!(
            projected["documents"][0].is_string(),
            "the scalar document is preserved: {projected}"
        );
        assert!(projected.get("parse_error").is_none(), "{projected}");
    }

    #[test]
    fn an_empty_document_is_skipped() {
        let art = artifact("k8s.yaml", "kind: Service\n---\n", None);
        let projected = project(&art);
        assert_eq!(projected["documents"].as_array().map(Vec::len), Some(1));
    }
}
