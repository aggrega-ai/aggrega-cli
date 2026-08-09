// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//! Symmetry test pinning byte-identity between the Rust-side regex
//! constants in `src/rules/name_shape.rs` and the Go-side
//! source-of-truth in `aggrega-cli/pkg/catalog/name_shape.go`.
//!
//! The test reads the Go file (umbrella-relative; available in
//! umbrella checkouts), extracts every `const X = ...` line carrying a
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
//! Skip-on-missing-file behaviour: standalone PDK / plugin checkouts
//! where the Go file is unreachable skip the test gracefully (cargo's
//! stable test framework has no skip-with-message primitive; the
//! test passes with a printed note).
//!
//! Hard-fail-when-supposed-to-pin: callers running in an umbrella
//! checkout (or any context that REQUIRES the symmetry to hold) set
//! the `UMBRELLA_CHECKOUT=1` environment variable before invoking
//! the test. Under that env var, a missing Go file panics instead of
//! silently skipping. CI workflows that pin the symmetry should set
//! the env var; standalone developer testing leaves it unset for the
//! graceful-skip path.

use std::path::PathBuf;

use publishing_linter::rules::name_shape::NAME_SHAPES;

fn go_source_path() -> Option<PathBuf> {
    let manifest_dir = PathBuf::from(env!("CARGO_MANIFEST_DIR"));
    let candidate = manifest_dir
        .join("..")
        .join("..")
        .join("pkg")
        .join("catalog")
        .join("name_shape.go");
    if candidate.exists() {
        Some(candidate)
    } else {
        None
    }
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
    let path = match go_source_path() {
        Some(p) => p,
        None => {
            if std::env::var("UMBRELLA_CHECKOUT").is_ok() {
                panic!(
                    "name_shape_symmetry_test: UMBRELLA_CHECKOUT is set but the Go file is unreachable. \
                     The umbrella layout requires aggrega-cli/pkg/catalog/name_shape.go relative to \
                     this plugin crate. If you are running CI, ensure the umbrella checkout is intact."
                );
            }
            eprintln!(
                "name_shape_symmetry_test: Go file not reachable (standalone plugin checkout); \
                 skipping. The umbrella CI run pins symmetry (set UMBRELLA_CHECKOUT=1 to hard-fail \
                 on missing Go file)."
            );
            return;
        }
    };

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
