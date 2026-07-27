// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//! The plugin half of the sensitivity-declaration parity check, plus the
//! vocabulary pins the plugin's reader depends on.
//!
//! Three subjects, three mechanisms, chosen per subject rather than
//! uniformly:
//!
//!  1. **A predicate over cases** — "which leaves does this schema
//!     declare sensitive". Two implementations read two representations
//!     of the same declaration and cannot call each other, so neither
//!     can be pinned to the other's source text. They are pinned to a
//!     shared expectation instead: `tests/fixtures/sensitivity_parity.json`
//!     carries the cases and the answer, this test runs them through the
//!     plugin's reader, and the compiler side runs the identical file
//!     through its own. A case is therefore checked on both sides or on
//!     neither.
//!
//!  2. **A closed keyword set** — the compact-form vocabulary the
//!     plugin's dispatch classification depends on. A set of literals IS
//!     pinnable to its source of truth's text, so it is: this test reads
//!     the vocabulary declarations and compares them to the plugin's
//!     copies. A keyword added upstream fails here rather than silently
//!     reclassifying an authored node.
//!
//!  3. **A closed operator set** — the label-selector operators. Same
//!     mechanism as (2), same reason.
//!
//! Skip-on-missing-file behaviour matches the other cross-language test
//! in this crate: a standalone plugin checkout where the compiler source
//! is unreachable skips with a printed note, and setting
//! `UMBRELLA_CHECKOUT=1` turns that skip into a hard failure for callers
//! that require the pin to hold.

use std::collections::BTreeSet;
use std::path::PathBuf;

use publishing_linter::rules::secret_slot_declares_sensitive as slot_rule;
use publishing_linter::sensitivity::{
    self, DeclaredSensitiveLeaves, COMPACT_KEYWORDS, SCHEMA_CONTAINER_KEYWORDS, SENSITIVE_PILLARS,
};
use serde_json::Value;

// ---------------------------------------------------------------------
// Shared-fixture parity: the plugin half
// ---------------------------------------------------------------------

fn fixture_path() -> PathBuf {
    PathBuf::from(env!("CARGO_MANIFEST_DIR"))
        .join("tests")
        .join("fixtures")
        .join("sensitivity_parity.json")
}

fn load_fixture() -> Value {
    let path = fixture_path();
    let text = std::fs::read_to_string(&path)
        .unwrap_or_else(|e| panic!("read shared fixture at {}: {e}", path.display()));
    serde_json::from_str(&text)
        .unwrap_or_else(|e| panic!("parse shared fixture at {}: {e}", path.display()))
}

fn string_set(v: Option<&Value>) -> BTreeSet<String> {
    v.and_then(Value::as_array)
        .map(|entries| {
            entries
                .iter()
                .filter_map(Value::as_str)
                .map(str::to_string)
                .collect()
        })
        .unwrap_or_default()
}

/// The paths the plugin is expected to collect for one case: the shared
/// expectation minus the paths a text-authored escape hides from it.
fn expected_for_guest(case: &Value, pillar: &str) -> BTreeSet<String> {
    let expect = string_set(case.get("expect").and_then(|e| e.get(pillar)));
    let blind = string_set(case.get("guestBlindPaths").and_then(|b| b.get(pillar)));
    expect.difference(&blind).cloned().collect()
}

/// The pillar-qualified paths the plugin is expected to report as
/// unreadable for one case.
fn expected_blind(case: &Value) -> BTreeSet<String> {
    let mut out = BTreeSet::new();
    for pillar in SENSITIVE_PILLARS {
        for path in string_set(case.get("guestBlindPaths").and_then(|b| b.get(*pillar))) {
            out.insert(format!("{pillar}.{path}"));
        }
    }
    out
}

#[test]
fn leaf_cases_match_the_shared_expectation() {
    let fixture = load_fixture();
    let cases = fixture
        .get("leafCases")
        .and_then(Value::as_array)
        .expect("leafCases is an array");
    assert!(
        cases.len() >= 20,
        "the shared fixture lost cases ({} left); it is the only source of cases for \
         both sides, so a deletion here silently narrows both",
        cases.len()
    );

    let mut failures: Vec<String> = Vec::new();
    for case in cases {
        let name = case.get("name").and_then(Value::as_str).unwrap_or("<unnamed>");
        let schema = case.get("schema").expect("case carries a schema");
        let got: DeclaredSensitiveLeaves = sensitivity::declared_sensitive_leaves(schema);

        for pillar in SENSITIVE_PILLARS {
            let want = expected_for_guest(case, pillar);
            let have = got.pillar(pillar).cloned().unwrap_or_default();
            if have != want {
                failures.push(format!(
                    "{name} / {pillar}: guest collected {have:?}, shared expectation {want:?}"
                ));
            }
        }

        let want_blind = expected_blind(case);
        if got.unreadable_escapes != want_blind {
            failures.push(format!(
                "{name}: guest reported unreadable {:?}, shared expectation {want_blind:?}",
                got.unreadable_escapes
            ));
        }

        let want_any = SENSITIVE_PILLARS
            .iter()
            .any(|p| !expected_for_guest(case, p).is_empty());
        if got.any() != want_any {
            failures.push(format!("{name}: any() = {}, want {want_any}", got.any()));
        }
    }

    assert!(
        failures.is_empty(),
        "sensitivity leaf-declaration parity violated:\n  {}",
        failures.join("\n  ")
    );
}

#[test]
fn slot_cases_match_the_shared_expectation() {
    let fixture = load_fixture();
    let cases = fixture
        .get("slotCases")
        .and_then(Value::as_array)
        .expect("slotCases is an array");
    assert!(cases.len() >= 5, "the shared fixture lost slot cases");

    let mut failures: Vec<String> = Vec::new();
    for case in cases {
        let name = case.get("name").and_then(Value::as_str).unwrap_or("<unnamed>");
        let slot = case.get("slot").expect("case carries a slot");
        let want = case
            .get("expectSensitive")
            .and_then(Value::as_bool)
            .expect("case carries expectSensitive");
        let got = sensitivity::slot_declares_sensitive(slot);
        if got != want {
            failures.push(format!("{name}: guest read {got}, shared expectation {want}"));
        }
    }
    assert!(
        failures.is_empty(),
        "sensitivity slot-declaration parity violated:\n  {}",
        failures.join("\n  ")
    );
}

// ---------------------------------------------------------------------
// Vocabulary pins against the source of truth
// ---------------------------------------------------------------------

/// Resolves a compiler source file relative to this crate, mirroring how
/// the name-shape symmetry test reaches the same tree.
fn compiler_source(segments: &[&str]) -> Option<PathBuf> {
    let mut candidate = PathBuf::from(env!("CARGO_MANIFEST_DIR")).join("..").join("..");
    for s in segments {
        candidate = candidate.join(s);
    }
    candidate.exists().then_some(candidate)
}

/// Reads a compiler source file, or returns None after the same
/// skip-or-hard-fail decision the name-shape symmetry test makes.
fn read_compiler_source(segments: &[&str], test_name: &str) -> Option<String> {
    match compiler_source(segments) {
        Some(path) => Some(
            std::fs::read_to_string(&path)
                .unwrap_or_else(|e| panic!("read {}: {e}", path.display())),
        ),
        None => {
            if std::env::var("UMBRELLA_CHECKOUT").is_ok() {
                panic!(
                    "{test_name}: UMBRELLA_CHECKOUT is set but {} is unreachable. The umbrella \
                     layout places it relative to this plugin crate; ensure the checkout is intact.",
                    segments.join("/")
                );
            }
            eprintln!(
                "{test_name}: {} not reachable (standalone plugin checkout); skipping. Set \
                 UMBRELLA_CHECKOUT=1 to hard-fail instead.",
                segments.join("/")
            );
            None
        }
    }
}

/// Returns the text between the delimiter opened by `needle` and its
/// match. `needle` ends with the opening delimiter, which fixes the pair:
/// a declaration block opens with `{`, a grouped constant list with `(`.
fn delimited_block(source: &str, needle: &str) -> Option<String> {
    let (open, close) = match needle.chars().last()? {
        '{' => ('{', '}'),
        '(' => ('(', ')'),
        _ => return None,
    };
    let start = source.find(needle)? + needle.len();
    let mut depth = 1usize;
    for (offset, ch) in source[start..].char_indices() {
        if ch == open {
            depth += 1;
        } else if ch == close {
            depth -= 1;
            if depth == 0 {
                return Some(source[start..start + offset].to_string());
            }
        }
    }
    None
}

/// Every double-quoted literal in a block, with line comments stripped
/// first so a comment can never contribute one.
fn quoted_literals(block: &str) -> BTreeSet<String> {
    let mut out = BTreeSet::new();
    for line in block.lines() {
        let code = match line.find("//") {
            Some(i) => &line[..i],
            None => line,
        };
        let bytes: Vec<char> = code.chars().collect();
        let mut i = 0;
        while i < bytes.len() {
            if bytes[i] == '"' {
                let mut j = i + 1;
                let mut literal = String::new();
                while j < bytes.len() && bytes[j] != '"' {
                    literal.push(bytes[j]);
                    j += 1;
                }
                if j < bytes.len() {
                    out.insert(literal);
                }
                i = j + 1;
            } else {
                i += 1;
            }
        }
    }
    out
}

#[test]
fn compact_form_keyword_vocabulary_matches_the_source_of_truth() {
    let Some(vocabulary) = read_compiler_source(
        &["pkg", "compactform", "vocabulary.go"],
        "compact_form_keyword_vocabulary_matches_the_source_of_truth",
    ) else {
        return;
    };

    // The plugin's copy is the union of the three keyword sets that make a
    // mapping a constraint leaf, plus the one composition keyword that acts
    // as a leaf constraint.
    let mut upstream: BTreeSet<String> = BTreeSet::new();
    for block in [
        "var typeIdentityKeywords = map[string]bool{",
        "var constraintKeywords = map[string]bool{",
        "var aggregaSpecificKeywords = map[string]bool{",
    ] {
        let body = delimited_block(&vocabulary, block)
            .unwrap_or_else(|| panic!("keyword block {block:?} not found; the vocabulary moved"));
        upstream.extend(quoted_literals(&body));
    }
    // The single composition keyword the leaf-shape test admits. Pinned by
    // its own needle so a change to that carve-out fails here too.
    assert!(
        vocabulary.contains("key == \"valuesType\""),
        "the composition-keyword carve-out in the leaf-shape test changed shape; \
         the plugin's copy assumes valuesType is the only composition keyword \
         that makes a mapping a leaf"
    );
    upstream.insert("valuesType".to_string());

    let ours: BTreeSet<String> = COMPACT_KEYWORDS.iter().map(|s| s.to_string()).collect();
    assert_eq!(
        ours, upstream,
        "the compact-form keyword vocabulary drifted. Missing from the plugin: {:?}. \
         Extra in the plugin: {:?}. A keyword the plugin does not know reclassifies an \
         authored node, which silently changes which leaves it reads as declared.",
        upstream.difference(&ours).collect::<Vec<_>>(),
        ours.difference(&upstream).collect::<Vec<_>>(),
    );
}

#[test]
fn schema_container_keyword_vocabulary_matches_the_source_of_truth() {
    let Some(dispatch) = read_compiler_source(
        &["pkg", "compactform", "dispatch.go"],
        "schema_container_keyword_vocabulary_matches_the_source_of_truth",
    ) else {
        return;
    };

    let body = delimited_block(&dispatch, "structural := []string{")
        .expect("the structural keyword list not found; the dispatch shape moved");
    let upstream = quoted_literals(&body);
    let ours: BTreeSet<String> = SCHEMA_CONTAINER_KEYWORDS
        .iter()
        .map(|s| s.to_string())
        .collect();
    assert_eq!(
        ours, upstream,
        "the authored-JSON-Schema keyword list drifted. Missing from the plugin: {:?}. \
         Extra in the plugin: {:?}.",
        upstream.difference(&ours).collect::<Vec<_>>(),
        ours.difference(&upstream).collect::<Vec<_>>(),
    );
}

#[test]
fn selector_operator_vocabulary_matches_the_source_of_truth() {
    let Some(parse) = read_compiler_source(
        &["pkg", "selector", "parse.go"],
        "selector_operator_vocabulary_matches_the_source_of_truth",
    ) else {
        return;
    };

    // The closed operator set. The slot rule reads only the two forms that
    // can pin a key to one value and explicitly excludes the rest, so a
    // seventh operator is a fact it must be re-examined against.
    let body = delimited_block(&parse, "const (").expect("the operator constant block not found");
    let upstream = quoted_literals(&body);
    let known: BTreeSet<String> = ["=", "!=", "in", "notin", "exists", "doesnotexist"]
        .iter()
        .map(|s| s.to_string())
        .collect();
    assert_eq!(
        upstream, known,
        "the label-selector operator set changed. The slot rule pins only on the equality \
         and set-membership forms and treats every other form as not pinning; a new operator \
         has to be classified before it can be ignored."
    );

    // The surface tokens the rule matches on, and the precedence that makes
    // the inequality token win over the equality token it contains. Both are
    // read out of the clause classifier itself rather than the whole file, so
    // the operator-constant block's own ordering cannot stand in for them.
    let clause_classifier = delimited_block(&parse, "func parseOne(s string) (Requirement, error) {")
        .expect("the clause classifier not found; the selector parser's shape moved");
    for token in slot_rule::SELECTOR_OPERATOR_TOKENS {
        assert!(
            clause_classifier.contains(&format!("\"{token}\"")),
            "the clause classifier no longer carries the surface token {token:?} the slot rule \
             matches on"
        );
    }
    let inequality = clause_classifier
        .find("\"!=\"")
        .expect("inequality token in the clause classifier");
    let equality = clause_classifier
        .find("\"=\"")
        .expect("equality token in the clause classifier");
    assert!(
        inequality < equality,
        "the clause classifier now reads the equality form before the inequality form; the \
         slot rule mirrors the opposite order and would read a not-equals clause as a pin"
    );
}
