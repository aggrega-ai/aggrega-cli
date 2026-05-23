// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//! Symmetry test pinning byte-identity between the Rust-side regex
//! constants in `src/rules/name_shape.rs` and the Go-side
//! source-of-truth in `aggrega-cli/pkg/catalog/name_shape.go`.
//!
//! The test reads the Go file (umbrella-relative; available in
//! umbrella checkouts), greps the eight `const X = ...` lines that
//! carry the per-kind regex strings, and compares the extracted
//! patterns to the Rust-side constants. Drift between the two
//! surfaces surfaces here at unit-test time.
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

use publishing_linter::rules::name_shape::{
    AGGREGATE_NAME_SHAPE, COMPONENT_NAME_SHAPE, FEATURE_NAME_SHAPE, FEATURE_RECIPE_NAME_SHAPE,
    MAPPING_LIBRARY_NAME_SHAPE, PROVIDER_NAME_SHAPE, RECIPE_NAME_SHAPE, SCHEMA_LIBRARY_NAME_SHAPE,
};

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

    let cases: &[(&str, &str)] = &[
        ("ComponentNameShape", COMPONENT_NAME_SHAPE),
        ("FeatureNameShape", FEATURE_NAME_SHAPE),
        ("RecipeNameShape", RECIPE_NAME_SHAPE),
        ("FeatureRecipeNameShape", FEATURE_RECIPE_NAME_SHAPE),
        ("ProviderNameShape", PROVIDER_NAME_SHAPE),
        ("AggregateNameShape", AGGREGATE_NAME_SHAPE),
        ("SchemaLibraryNameShape", SCHEMA_LIBRARY_NAME_SHAPE),
        ("MappingLibraryNameShape", MAPPING_LIBRARY_NAME_SHAPE),
    ];

    let mut mismatches: Vec<String> = Vec::new();
    for (go_name, rust_const) in cases {
        let go_value = match extract_go_const(&source, go_name) {
            Some(v) => v,
            None => {
                mismatches.push(format!("const {go_name} not found in Go source"));
                continue;
            }
        };
        if go_value != *rust_const {
            mismatches.push(format!(
                "{go_name}: Go {go_value:?} != Rust {rust_const:?}"
            ));
        }
    }

    assert!(
        mismatches.is_empty(),
        "name-shape regex symmetry violated:\n  {}",
        mismatches.join("\n  ")
    );
}
