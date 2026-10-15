// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//! Symmetry test pinning byte-identity between the Rust-side regex
//! constants in `src/rules/name_shape.rs` and the Go-side
//! source-of-truth in `aggrega-cli/pkg/catalog/name_shape.go`.
//!
//! The test reads the Go file (two directories above this crate, in the
//! same repository), extracts every `const X = ...` line carrying a
//! per-kind regex string, and compares the extracted patterns to the
//! Rust-side dispatch table. Drift between the two surfaces surfaces
//! here at unit-test time.
//!
//! The comparison runs in BOTH directions and neither side's kind set
//! is transcribed here. The Rust side is walked from its own dispatch
//! table, and each kind's Go constant name is derived — the constant
//! for a kind is exactly `<Kind>NameShape` — so a kind added to one
//! side alone fails. A one-directional check would not: a kind added
//! to the Go declarations with no Rust arm leaves the rule resolving
//! "no shape" for it, which reports nothing and reads as a pass.
//!
//! The Go file ships in every checkout of this crate, because the crate
//! lives inside the repository that owns it. A missing file is therefore
//! a failure, never a skip: a test that passed with nothing to compare
//! would report symmetry it never checked.

use std::path::PathBuf;

use publishing_linter::rules::name_shape::NAME_SHAPES;

/// `go_source_path` resolves the Go source of truth, which sits at
/// `pkg/catalog/name_shape.go` in the repository holding this crate (two
/// directories above it), and panics naming that path when it is absent.
fn go_source_path() -> PathBuf {
    let manifest_dir = PathBuf::from(env!("CARGO_MANIFEST_DIR"));
    let candidate = manifest_dir
        .join("..")
        .join("..")
        .join("pkg")
        .join("catalog")
        .join("name_shape.go");
    if !candidate.exists() {
        panic!(
            "name_shape_symmetry_test: the Go source of truth is missing at {}; it is \
             pkg/catalog/name_shape.go in the repository holding this crate, two directories \
             above it",
            candidate.display()
        );
    }
    candidate
}

fn extract_go_const(source: &str, name: &str) -> Option<String> {
    // Pattern: `const <name> = ` <backtick-quoted-regex-string> backtick
    // Example: `const ComponentNameShape = `^[a-z]...$``
    let needle = format!("const {name} = `");
    let start = source.find(&needle)?;
    let after = &source[start + needle.len()..];
    let end = after.find('`')?;
    Some(after[..end].to_string())
}

/// `go_const_name` derives the Go source-of-truth constant name for a
/// kind. The naming rule is uniform across every kind, so the mapping
/// is computed rather than listed — a list here would be a third copy
/// of the kind set and would need remembering exactly when a kind is
/// added.
fn go_const_name(kind: &str) -> String {
    format!("{kind}NameShape")
}

/// `declared_go_kinds` recovers the kind set from the Go file itself by
/// walking its `const <Kind>NameShape = ` declarations. This is what
/// lets the reverse direction be checked at all: without it the test
/// could only confirm that the kinds Rust knows about agree, which is
/// exactly the check that passes while a Go-declared kind reaches no
/// dispatch arm.
fn declared_go_kinds(source: &str) -> Vec<String> {
    const PREFIX: &str = "const ";
    const SUFFIX: &str = "NameShape = `";
    let mut kinds = Vec::new();
    for line in source.lines() {
        let line = line.trim_start();
        let Some(rest) = line.strip_prefix(PREFIX) else {
            continue;
        };
        let Some(idx) = rest.find(SUFFIX) else {
            continue;
        };
        let kind = &rest[..idx];
        if !kind.is_empty() {
            kinds.push(kind.to_string());
        }
    }
    kinds
}

#[test]
fn name_shape_constants_match_go_source_of_truth() {
    let path = go_source_path();

    let source = std::fs::read_to_string(&path)
        .unwrap_or_else(|e| panic!("read Go file at {}: {e}", path.display()));

    let go_kinds = declared_go_kinds(&source);
    assert!(
        !go_kinds.is_empty(),
        "no `const <Kind>NameShape` declarations were extracted from {}; the extraction has \
         broken and every assertion below would be vacuous",
        path.display()
    );

    let mut mismatches: Vec<String> = Vec::new();

    // Forward: every kind the rule dispatches on has a Go declaration
    // carrying the identical pattern.
    for (kind, rust_const) in NAME_SHAPES {
        let go_name = go_const_name(kind);
        let go_value = match extract_go_const(&source, &go_name) {
            Some(v) => v,
            None => {
                mismatches.push(format!(
                    "{kind}: the rule carries a pattern but the Go source declares no const {go_name}"
                ));
                continue;
            }
        };
        if go_value != *rust_const {
            mismatches.push(format!("{go_name}: Go {go_value:?} != Rust {rust_const:?}"));
        }
    }

    // Reverse: every kind the Go source declares a shape for is
    // actually dispatched on. A kind failing here is one whose name is
    // documented as having a shape and checked by nothing — the rule
    // returns no finding for it, which reads as a clean result.
    for kind in &go_kinds {
        if !NAME_SHAPES.iter().any(|(k, _)| k == kind) {
            mismatches.push(format!(
                "{kind}: the Go source declares a name shape but the rule has no entry, so a \
                 name of that kind is checked by nothing and every one of them reports clean"
            ));
        }
    }

    assert!(
        mismatches.is_empty(),
        "name-shape regex symmetry violated:\n  {}",
        mismatches.join("\n  ")
    );
}
