// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

//! Reading a bundled policy archive an operator attached.
//!
//! A policy set can be attached two ways. Each file attached on its own is
//! policy text, read as written. Alternatively the whole set travels as one
//! archive — the shape a Rego toolchain builds — carrying its policy files and
//! its data files together, and this module opens it.
//!
//! # Why the archive arrives encoded
//!
//! An attached file's content travels to this plugin as text. A compressed
//! archive is not text: it is arbitrary bytes, and carrying arbitrary bytes
//! through a text channel loses them. So an archive is attached
//! base64-encoded — the standard way to carry bytes where text is expected:
//!
//! ```sh
//! tar czf bundle.tar.gz policy.rego data.json
//! base64 -w0 bundle.tar.gz > bundle.tar.gz.base64
//! ```
//!
//! A file whose name ends in `.base64` is read as an encoded archive; every
//! other attached file is read as policy text. Naming it is what makes the
//! choice the operator's own statement rather than something guessed from the
//! bytes: a damaged archive is then reported as a damaged archive, naming the
//! file and the step that failed, instead of being mistaken for a policy file
//! that does not parse.
//!
//! # What an archive may hold
//!
//! Every `.rego` member is a policy module, added under its path inside the
//! archive. Every `data.json` or `data.yaml` member is a data document, added
//! under the directory it sits in — a `limits/data.json` holding `{"max": 3}`
//! is read by a policy as `data.limits.max`. Members of any other kind are
//! carried by the archive for its own toolchain and are not policy input, so
//! they are passed over.
//!
//! # An archive that cannot be opened is never an empty policy set
//!
//! Every failure — an encoding that does not decode, a compressed stream that
//! does not decompress, a truncated archive, a data file that does not read, or
//! an archive holding no policy at all — is reported as a failure naming the
//! attached file. None of them yields an empty policy set, because an empty
//! policy set reaches no decision, and "the archive was unreadable" must never
//! be indistinguishable from "the policy allowed the build".

use std::io::Read;

use base64::Engine as _;

/// The file-name suffix that marks an attached file as an encoded archive
/// rather than policy text.
pub const ENCODED_SUFFIX: &str = ".base64";

/// The base name of a data document inside an archive, per extension.
const DATA_STEMS: [&str; 2] = ["data.json", "data.yaml"];

/// The extension marking an archive member as a policy module.
const POLICY_EXTENSION: &str = ".rego";

/// One policy module read out of an archive: the path it held inside the
/// archive, and its policy text.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Module {
    /// The member's path inside the archive, used as the module's path so a
    /// diagnostic naming it points at something the operator can find.
    pub path: String,
    /// The policy text, carried through exactly as the archive held it.
    pub content: String,
}

/// What one archive held: its policy modules and its data documents, each
/// ordered by the member's path inside the archive so an identical archive
/// yields an identical sequence.
#[derive(Debug, Clone, Default, PartialEq, Eq)]
pub struct Bundle {
    /// The policy modules, in member-path order.
    pub modules: Vec<Module>,
    /// The data documents, in member-path order, each already nested under the
    /// directory its member sat in.
    pub data: Vec<serde_json::Value>,
}

/// Reports whether an attached file's name marks it as an encoded archive.
pub fn is_encoded_archive(path: &str) -> bool {
    path.ends_with(ENCODED_SUFFIX)
}

/// Opens one attached archive: decodes it, decompresses it, reads its members,
/// and returns the policy modules and data documents it held.
///
/// `path` is the attached file's name and appears in every failure message, so
/// an operator reading one knows which attachment to look at. Each step reports
/// its own failure, so the message says whether the encoding, the compression,
/// the archive itself, or one member's content was the problem.
pub fn read(path: &str, encoded: &str) -> anyhow::Result<Bundle> {
    let compressed = decode(path, encoded)?;
    let archive = decompress(path, &compressed)?;
    let bundle = members(path, &archive)?;
    if bundle.modules.is_empty() {
        anyhow::bail!(
            "attached archive {path:?} holds no {POLICY_EXTENSION} policy file, so it declares no policy to evaluate"
        );
    }
    Ok(bundle)
}

/// Decodes the attached text back to the archive's bytes. Whitespace the
/// encoding tool wrapped lines with is not part of the encoding, so it is
/// removed before decoding rather than being reported as a damaged file.
fn decode(path: &str, encoded: &str) -> anyhow::Result<Vec<u8>> {
    let compact: String = encoded.chars().filter(|c| !c.is_whitespace()).collect();
    base64::engine::general_purpose::STANDARD
        .decode(compact.as_bytes())
        .map_err(|err| {
            anyhow::anyhow!(
                "attached archive {path:?} is not base64-encoded: {err}. An archive is attached encoded because an attached file's content travels as text; encode it with `base64 -w0`."
            )
        })
}

/// Decompresses the archive's bytes.
fn decompress(path: &str, compressed: &[u8]) -> anyhow::Result<Vec<u8>> {
    let mut out = Vec::new();
    flate2::read::GzDecoder::new(compressed)
        .read_to_end(&mut out)
        .map_err(|err| {
            anyhow::anyhow!(
                "attached archive {path:?} did not decompress: {err}. It is expected to be a gzip-compressed tar archive."
            )
        })?;
    Ok(out)
}

/// Reads the archive's members, collecting the policy modules and the data
/// documents and passing over everything else.
///
/// Members are collected in member-path order rather than in the order the
/// archive stored them, so two archives holding the same files yield the same
/// sequence whatever order the tool that built them wrote.
fn members(path: &str, archive: &[u8]) -> anyhow::Result<Bundle> {
    let mut modules = Vec::new();
    let mut data: Vec<(String, serde_json::Value)> = Vec::new();

    let mut reader = tar::Archive::new(archive);
    let entries = reader.entries().map_err(|err| {
        anyhow::anyhow!("attached archive {path:?} could not be read as a tar archive: {err}")
    })?;
    for entry in entries {
        let mut entry = entry.map_err(|err| {
            anyhow::anyhow!("attached archive {path:?} is truncated or damaged: {err}")
        })?;
        if !entry.header().entry_type().is_file() {
            continue;
        }
        let member = member_path(&entry.path().map_err(|err| {
            anyhow::anyhow!("attached archive {path:?} carries an unreadable member name: {err}")
        })?);
        let mut content = String::new();
        entry.read_to_string(&mut content).map_err(|err| {
            anyhow::anyhow!(
                "attached archive {path:?} member {member:?} did not read as text: {err}"
            )
        })?;

        if member.ends_with(POLICY_EXTENSION) {
            modules.push(Module {
                path: member,
                content,
            });
            continue;
        }
        if let Some(stem) = data_stem(&member) {
            let document = read_data(path, &member, stem, &content)?;
            data.push((member, document));
        }
    }

    modules.sort_by(|a, b| a.path.cmp(&b.path));
    data.sort_by(|a, b| a.0.cmp(&b.0));
    Ok(Bundle {
        modules,
        data: data.into_iter().map(|(_, document)| document).collect(),
    })
}

/// Normalises a member's path to the form used as a module path and as a data
/// prefix: the archive-relative path with a leading `./` removed, which is how
/// an archive built from inside a directory names its members.
fn member_path(path: &std::path::Path) -> String {
    let text = path.to_string_lossy().replace('\\', "/");
    text.strip_prefix("./").unwrap_or(&text).to_string()
}

/// Reports which data-document form a member is, or none when it is not one.
fn data_stem(member: &str) -> Option<&'static str> {
    let base = member.rsplit('/').next().unwrap_or(member);
    DATA_STEMS.iter().copied().find(|stem| *stem == base)
}

/// Reads one data document and nests it under the directory its member sat in,
/// so a data file's position inside the archive is where a policy reads it.
///
/// A YAML document is read through the same reader as a JSON one, because YAML
/// is a superset of JSON. A document that is not an object cannot be merged
/// into the data document a policy reads, so it is reported rather than being
/// dropped or wrapped in a shape the operator did not write.
fn read_data(
    path: &str,
    member: &str,
    stem: &str,
    content: &str,
) -> anyhow::Result<serde_json::Value> {
    let value: serde_json::Value = serde_yaml::from_str(content).map_err(|err| {
        anyhow::anyhow!(
            "attached archive {path:?} member {member:?} did not read as a data document: {err}"
        )
    })?;
    if !value.is_object() {
        anyhow::bail!(
            "attached archive {path:?} member {member:?} holds a {} rather than a mapping; a data document is read as a mapping of names to values",
            kind_of(&value)
        );
    }
    let mut nested = value;
    for segment in prefix_segments(member, stem).into_iter().rev() {
        let mut wrapper = serde_json::Map::new();
        wrapper.insert(segment, nested);
        nested = serde_json::Value::Object(wrapper);
    }
    Ok(nested)
}

/// The directory segments a data member sits under, which become the names a
/// policy reads the document beneath.
fn prefix_segments(member: &str, stem: &str) -> Vec<String> {
    member
        .strip_suffix(stem)
        .unwrap_or("")
        .split('/')
        .filter(|segment| !segment.is_empty())
        .map(str::to_string)
        .collect()
}

/// Names a value's kind for a message about a document of the wrong shape.
fn kind_of(value: &serde_json::Value) -> &'static str {
    match value {
        serde_json::Value::Null => "empty document",
        serde_json::Value::Bool(_) => "true/false value",
        serde_json::Value::Number(_) => "number",
        serde_json::Value::String(_) => "single string",
        serde_json::Value::Array(_) => "list",
        serde_json::Value::Object(_) => "mapping",
    }
}

/// Builds a base64-encoded gzipped tar carrying the named members, in the
/// order given.
///
/// It lives here, beside the reader it feeds, so every test that needs an
/// archive builds one the same way — a second builder would be a second opinion
/// on what an archive looks like.
#[cfg(test)]
pub(crate) fn encoded_archive(members: &[(&str, &str)]) -> String {
    use std::io::Write;
    let mut tarball = Vec::new();
    {
        let mut builder = tar::Builder::new(&mut tarball);
        for (name, body) in members {
            let mut header = tar::Header::new_gnu();
            header.set_size(body.len() as u64);
            header.set_mode(0o644);
            header.set_cksum();
            builder
                .append_data(&mut header, name, body.as_bytes())
                .expect("a test archive member is appendable");
        }
        builder.finish().expect("a test archive finishes");
    }
    let mut gz = flate2::write::GzEncoder::new(Vec::new(), flate2::Compression::default());
    gz.write_all(&tarball).expect("a test archive compresses");
    let compressed = gz.finish().expect("a test archive compresses");
    base64::engine::general_purpose::STANDARD.encode(compressed)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn a_named_encoded_file_is_recognised_and_others_are_not() {
        assert!(is_encoded_archive("bundle.tar.gz.base64"));
        assert!(is_encoded_archive("anything.base64"));
        assert!(!is_encoded_archive("policy.rego"));
        assert!(!is_encoded_archive("bundle.tar.gz"));
    }

    #[test]
    fn an_archive_yields_its_policy_and_its_data() {
        let encoded = encoded_archive(&[
            ("data.json", r#"{"max":3}"#),
            ("policy.rego", "package proof\n"),
        ]);
        let bundle = read("bundle.tar.gz.base64", &encoded).expect("the archive opens");
        assert_eq!(
            bundle.modules,
            vec![Module {
                path: "policy.rego".to_string(),
                content: "package proof\n".to_string(),
            }]
        );
        assert_eq!(bundle.data, vec![serde_json::json!({"max": 3})]);
    }

    #[test]
    fn members_are_ordered_by_path_not_by_stored_order() {
        // Stored z-then-a; read a-then-z, so an archive built in any order
        // evaluates the same way.
        let encoded = encoded_archive(&[("z.rego", "package z\n"), ("a.rego", "package a\n")]);
        let bundle = read("bundle.tar.gz.base64", &encoded).expect("the archive opens");
        let paths: Vec<&str> = bundle.modules.iter().map(|m| m.path.as_str()).collect();
        assert_eq!(paths, vec!["a.rego", "z.rego"]);
    }

    #[test]
    fn a_data_document_is_nested_under_the_directory_it_sits_in() {
        let encoded = encoded_archive(&[
            ("policy.rego", "package proof\n"),
            ("limits/data.json", r#"{"max":3}"#),
        ]);
        let bundle = read("bundle.tar.gz.base64", &encoded).expect("the archive opens");
        assert_eq!(bundle.data, vec![serde_json::json!({"limits": {"max": 3}})]);
    }

    #[test]
    fn a_yaml_data_document_reads_through_the_same_reader() {
        let encoded = encoded_archive(&[
            ("policy.rego", "package proof\n"),
            ("data.yaml", "max: 3\n"),
        ]);
        let bundle = read("bundle.tar.gz.base64", &encoded).expect("the archive opens");
        assert_eq!(bundle.data, vec![serde_json::json!({"max": 3})]);
    }

    #[test]
    fn a_leading_dot_slash_is_normalised_away() {
        let encoded = encoded_archive(&[("./policy.rego", "package proof\n")]);
        let bundle = read("bundle.tar.gz.base64", &encoded).expect("the archive opens");
        assert_eq!(bundle.modules[0].path, "policy.rego");
    }

    #[test]
    fn a_member_of_another_kind_is_passed_over() {
        let encoded = encoded_archive(&[
            ("policy.rego", "package proof\n"),
            (".manifest", r#"{"roots":[""]}"#),
            ("README.md", "notes\n"),
        ]);
        let bundle = read("bundle.tar.gz.base64", &encoded).expect("the archive opens");
        assert_eq!(bundle.modules.len(), 1);
        assert!(bundle.data.is_empty());
    }

    #[test]
    fn text_that_is_not_base64_is_reported_as_such() {
        let err = read("bundle.tar.gz.base64", "this is not base64 ###")
            .expect_err("an unencoded file is refused");
        let message = err.to_string();
        assert!(message.contains("bundle.tar.gz.base64"), "{message}");
        assert!(message.contains("base64"), "{message}");
    }

    #[test]
    fn base64_that_is_not_compressed_is_reported_as_such() {
        let encoded = base64::engine::general_purpose::STANDARD.encode(b"not a gzip stream at all");
        let err =
            read("bundle.tar.gz.base64", &encoded).expect_err("an uncompressed payload is refused");
        let message = err.to_string();
        assert!(message.contains("did not decompress"), "{message}");
    }

    #[test]
    fn a_truncated_archive_is_reported_rather_than_read_as_empty() {
        // Cut the compressed stream in half: it decodes, and decompression then
        // fails on the incomplete stream. The failure must name the file, not
        // yield an empty policy set.
        let full = encoded_archive(&[("policy.rego", "package proof\n")]);
        let raw = base64::engine::general_purpose::STANDARD
            .decode(full.as_bytes())
            .expect("the test archive decodes");
        let truncated = base64::engine::general_purpose::STANDARD.encode(&raw[..raw.len() / 2]);
        let err =
            read("bundle.tar.gz.base64", &truncated).expect_err("a truncated archive is refused");
        let message = err.to_string();
        assert!(message.contains("bundle.tar.gz.base64"), "{message}");
    }

    #[test]
    fn an_archive_with_no_policy_is_refused_rather_than_evaluating_nothing() {
        let encoded = encoded_archive(&[("data.json", r#"{"max":3}"#)]);
        let err = read("bundle.tar.gz.base64", &encoded)
            .expect_err("an archive with no policy is refused");
        let message = err.to_string();
        assert!(message.contains("no .rego policy file"), "{message}");
    }

    #[test]
    fn a_data_document_that_is_not_a_mapping_is_refused() {
        let encoded =
            encoded_archive(&[("policy.rego", "package proof\n"), ("data.json", "[1,2,3]")]);
        let err = read("bundle.tar.gz.base64", &encoded)
            .expect_err("a non-mapping data document is refused");
        let message = err.to_string();
        assert!(message.contains("list"), "{message}");
    }

    #[test]
    fn line_wrapped_encoding_is_accepted() {
        let compact = encoded_archive(&[("policy.rego", "package proof\n")]);
        let mut wrapped = String::new();
        for (index, chunk) in compact.as_bytes().chunks(76).enumerate() {
            if index > 0 {
                wrapped.push('\n');
            }
            wrapped.push_str(std::str::from_utf8(chunk).expect("base64 is ascii"));
        }
        let bundle = read("bundle.tar.gz.base64", &wrapped).expect("wrapped encoding opens");
        assert_eq!(bundle.modules.len(), 1);
    }

    #[test]
    fn reading_an_archive_is_repeatable() {
        let encoded = encoded_archive(&[
            ("b/data.json", r#"{"x":1}"#),
            ("a.rego", "package a\n"),
            ("c.rego", "package c\n"),
        ]);
        let first = read("bundle.tar.gz.base64", &encoded).expect("the archive opens");
        let second = read("bundle.tar.gz.base64", &encoded).expect("the archive opens");
        assert_eq!(first, second);
    }
}
