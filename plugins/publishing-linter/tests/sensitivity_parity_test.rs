// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//! The plugin half of the authored-schema parity check, plus the
//! vocabulary pins the plugin's reader depends on.
//!
//! The shared fixture holds two questions over the same bytes — which
//! leaves a schema declares sensitive, and which property names one
//! pillar exposes — because they are the same structural duplication and
//! one handshake holds both. A second fixture would mean a second copy of
//! that handshake.
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
//!  4. **A handshake between the two halves.** Reading the same file is
//!     not agreement: two halves that each check themselves against it
//!     and never look at each other cannot tell "we agree" apart from
//!     "the other one never ran", because silencing one leaves the other
//!     passing and the pair still reporting parity. So each half records
//!     the revision of the fixture it verified, and each reads the
//!     other's record and the other's test declarations. A half that is
//!     absent, suppressed, or left behind by a fixture edit is named
//!     rather than read as agreement.
//!
//! The compiler sources these pins read ship in every checkout of this
//! crate, because the crate lives inside the repository that owns them.
//! As in the other cross-language test in this crate, a missing source
//! file is a failure naming the path it was expected at, never a skip.

use std::collections::BTreeSet;
use std::path::PathBuf;

use publishing_linter::rules::provides_declared_by_component::PROVIDES_KEY as PROVIDES_PILLAR;
use publishing_linter::rules::secret_slot_declares_sensitive as slot_rule;
use publishing_linter::sensitivity::{
    self, DeclaredSensitiveLeaves, BRANCH_LIST_KEYWORDS, BRANCH_NODE_KEYWORDS, COMPACT_KEYWORDS,
    SCHEMA_CONTAINER_KEYWORDS, SENSITIVE_PILLARS,
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

fn fixture_text() -> String {
    let path = fixture_path();
    std::fs::read_to_string(&path)
        .unwrap_or_else(|e| panic!("read shared fixture at {}: {e}", path.display()))
}

fn load_fixture() -> Value {
    let text = fixture_text();
    serde_json::from_str(&text)
        .unwrap_or_else(|e| panic!("parse shared fixture at {}: {e}", fixture_path().display()))
}

/// This half's record of the fixture it verified, derived from the
/// fixture's own bytes rather than tracked by hand so that no edit can
/// leave it behind. The compiler half carries the same value in its own
/// source, and the two are compared against the fixture and against each
/// other.
///
/// This is deliberately a SECOND copy of a value the compiler half also
/// holds, and unifying the two would destroy the only thing it does: one
/// copy cannot disagree with itself, and disagreement is the signal.
/// When the fixture changes, both copies move, and a change that updates
/// only one is exactly the half-verified state this pins.
const SENSITIVITY_PARITY_FIXTURE_REVISION: &str = "e167c0ee91c5720f";

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
        cases.len() >= 30,
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

#[test]
fn pillar_key_cases_match_the_shared_expectation() {
    let fixture = load_fixture();
    let cases = fixture
        .get("pillarKeyCases")
        .and_then(Value::as_array)
        .expect("pillarKeyCases is an array");
    assert!(
        cases.len() >= 15,
        "the shared fixture lost pillar-key cases ({} left); it is the only source of \
         cases for both sides, so a deletion here silently narrows both",
        cases.len()
    );

    let mut failures: Vec<String> = Vec::new();
    let mut readable_seen = 0usize;
    let mut unreadable_seen = 0usize;
    for case in cases {
        let name = case.get("name").and_then(Value::as_str).unwrap_or("<unnamed>");
        let schema = case.get("schema").expect("case carries a schema");
        let readable = case
            .get("guestReadable")
            .and_then(Value::as_bool)
            .expect("case carries guestReadable");
        let got = sensitivity::pillar_property_names(schema, PROVIDES_PILLAR);

        match (readable, got) {
            (true, Some(keys)) => {
                readable_seen += 1;
                let want = string_set(case.get("expectKeys"));
                if keys != want {
                    failures.push(format!(
                        "{name}: guest exposed {keys:?}, shared expectation {want:?}"
                    ));
                }
            }
            (true, None) => failures.push(format!(
                "{name}: the shared expectation says this pillar is enumerable here, but the \
                 guest reported it unknown"
            )),
            (false, None) => unreadable_seen += 1,
            (false, Some(keys)) => failures.push(format!(
                "{name}: the shared expectation says the guest cannot enumerate this pillar, \
                 but it returned {keys:?}. Reporting a key set for a shape it cannot read is \
                 how the rule over this reader invents an undeclared key"
            )),
        }
    }

    // Both halves of the line have to be populated, or one arm of the match
    // above is never taken and the check reports on a set it never saw.
    assert!(
        readable_seen > 0 && unreadable_seen > 0,
        "the shared cases must cover both sides of the enumerable/unenumerable line; \
         saw {readable_seen} enumerable and {unreadable_seen} unenumerable"
    );
    assert!(
        failures.is_empty(),
        "pillar-key parity violated:\n  {}",
        failures.join("\n  ")
    );
}

// ---------------------------------------------------------------------
// Vocabulary pins against the source of truth
// ---------------------------------------------------------------------

/// Resolves a compiler source file in the repository holding this crate,
/// two directories above it, as the name-shape symmetry test reaches the
/// same tree.
fn compiler_source(segments: &[&str]) -> PathBuf {
    let mut candidate = PathBuf::from(env!("CARGO_MANIFEST_DIR")).join("..").join("..");
    for s in segments {
        candidate = candidate.join(s);
    }
    candidate
}

/// Reads a compiler source file, panicking with the path it was expected
/// at when the file is absent.
fn read_compiler_source(segments: &[&str], test_name: &str) -> String {
    let path = compiler_source(segments);
    if !path.exists() {
        panic!(
            "{test_name}: the compiler source is missing at {}; it is {} in the repository \
             holding this crate, two directories above it",
            path.display(),
            segments.join("/")
        );
    }
    std::fs::read_to_string(&path).unwrap_or_else(|e| panic!("read {}: {e}", path.display()))
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
    let vocabulary = read_compiler_source(
        &["pkg", "compactform", "vocabulary.go"],
        "compact_form_keyword_vocabulary_matches_the_source_of_truth",
    );

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
    let dispatch = read_compiler_source(
        &["pkg", "compactform", "dispatch.go"],
        "schema_container_keyword_vocabulary_matches_the_source_of_truth",
    );

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

/// The composition keywords each reader descends. Both sets are pinned,
/// and separately: one side descending the list-valued keywords and not
/// the single-schema ones would pass a pin over their union while
/// collecting a different set of declarations. The compiler side carries
/// the mirror of this test, which is what catches drift for anyone running
/// the compiler's suite rather than this crate's.
#[test]
fn composition_keyword_sets_match_the_source_of_truth() {
    let reader = read_compiler_source(
        &["pkg", "resolver", "sensitive_leaves.go"],
        "composition_keyword_sets_match_the_source_of_truth",
    );

    for (needle, ours) in [
        ("branchListKeywords = []string{", BRANCH_LIST_KEYWORDS),
        ("branchNodeKeywords = []string{", BRANCH_NODE_KEYWORDS),
    ] {
        let body = delimited_block(&reader, needle).unwrap_or_else(|| {
            panic!("the keyword set {needle:?} was not found; the reader's declaration shape moved and this pin needs re-aiming rather than passing vacuously")
        });
        let upstream = quoted_literals(&body);
        assert!(
            !upstream.is_empty(),
            "{needle:?} parsed empty upstream; a pin over an empty set holds no matter what this \
             reader descends"
        );
        let mine: BTreeSet<String> = ours.iter().map(|s| s.to_string()).collect();
        assert_eq!(
            mine, upstream,
            "the composition-keyword set {needle:?} drifted. Missing from the plugin: {:?}. \
             Extra in the plugin: {:?}. A keyword one side descends and the other does not leaves \
             a declaration collected on one side only.",
            upstream.difference(&mine).collect::<Vec<_>>(),
            mine.difference(&upstream).collect::<Vec<_>>(),
        );
    }
}

#[test]
fn selector_operator_vocabulary_matches_the_source_of_truth() {
    let parse = read_compiler_source(
        &["pkg", "selector", "parse.go"],
        "selector_operator_vocabulary_matches_the_source_of_truth",
    );

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
    let clause_classifier = delimited_block(&parse, "func parseOne(s string) (Requirement, string) {")
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

// ---------------------------------------------------------------------
// The cross-half handshake
// ---------------------------------------------------------------------

/// Derives a fixture's revision from its own bytes.
///
/// Derived rather than authored, because an authored revision is one an
/// editor can forget to bump, and a forgotten bump leaves both halves
/// recording a value that no longer describes what they read — the stale
/// record this handshake exists to refuse. Any change to the fixture,
/// down to a byte, moves it.
///
/// FNV-1a is used because what is needed is a short, exactly reproducible
/// fingerprint of a file both halves already read, in two languages, with
/// no dependency added to this crate to get it. The risk being managed is
/// an edit going unnoticed, not an adversary constructing a collision.
/// The compiler half reaches the identical function through its standard
/// library; that duplication is deliberate and is explained where the
/// recorded revision is declared.
fn fixture_revision(bytes: &[u8]) -> String {
    let mut hash: u64 = 0xcbf2_9ce4_8422_2325;
    for byte in bytes {
        hash ^= u64::from(*byte);
        hash = hash.wrapping_mul(0x0000_0100_0000_01b3);
    }
    format!("{hash:016x}")
}

/// Returns the index of the quote that closes a character literal
/// opening at `i`, or `None` when what opens there is not one.
///
/// The closing quote is what distinguishes the two things a single quote
/// can begin in the languages read here: a character literal closes, and
/// a lifetime does not. Anything unrecognised is left to be treated as
/// ordinary text, which is the safe direction — a lifetime read as a
/// character literal would swallow the code after it.
fn character_literal_end(chars: &[char], i: usize) -> Option<usize> {
    // The widest escape form, plus its two quotes.
    const LONGEST_ESCAPE: usize = 9;
    if chars.get(i + 1) == Some(&'\\') {
        for (offset, c) in chars.iter().enumerate().take(i + LONGEST_ESCAPE + 1) {
            if offset >= i + 2 && *c == '\'' {
                return Some(offset);
            }
        }
        return None;
    }
    (chars.get(i + 2) == Some(&'\'')).then_some(i + 2)
}

/// Removes line and block comments, leaving code text and line structure
/// intact. The two halves are written in languages that share C-style
/// comments, double-quoted strings and single-quoted character literals,
/// so one reader serves both.
///
/// Stripping first is what makes the checks below safe rather than merely
/// convenient. Every token they hunt is also a word this file and its
/// counterpart use in explanation — a test's name, the call that skips a
/// test, a revision. A scan over raw text would read a sentence
/// describing the mechanism as an instance of it. What survives stripping
/// is code, and only code can silence a test.
fn strip_source_comments(src: &str) -> String {
    enum State {
        Code,
        LineComment,
        BlockComment,
        StringLiteral,
    }

    let bytes: Vec<char> = src.chars().collect();
    let mut out = String::with_capacity(src.len());
    let mut state = State::Code;
    let mut i = 0;
    while i < bytes.len() {
        let c = bytes[i];
        let next = bytes.get(i + 1).copied();
        match state {
            State::Code => {
                if c == '/' && next == Some('/') {
                    state = State::LineComment;
                    i += 1;
                } else if c == '/' && next == Some('*') {
                    state = State::BlockComment;
                    i += 1;
                } else if c == '\'' {
                    // Skipped whole when it is a character literal, because
                    // a double quote inside one is an ordinary thing to
                    // write and reading it as the start of a string would
                    // invert every string boundary in the rest of the file
                    // — leaving comments unstripped in the tail, which is
                    // exactly where a silenced test would then hide.
                    match character_literal_end(&bytes, i) {
                        Some(end) => {
                            out.extend(bytes[i..=end].iter());
                            i = end;
                        }
                        None => out.push(c),
                    }
                } else {
                    if c == '"' {
                        state = State::StringLiteral;
                    }
                    out.push(c);
                }
            }
            State::LineComment => {
                if c == '\n' {
                    state = State::Code;
                    out.push(c);
                }
            }
            State::BlockComment => {
                if c == '*' && next == Some('/') {
                    state = State::Code;
                    i += 1;
                } else if c == '\n' {
                    out.push(c);
                }
            }
            State::StringLiteral => {
                out.push(c);
                if c == '\\' {
                    if let Some(escaped) = next {
                        out.push(escaped);
                        i += 1;
                    }
                } else if c == '"' {
                    state = State::Code;
                }
            }
        }
        i += 1;
    }
    out
}

/// Reads the value a declaration assigns, refusing both zero matches and
/// more than one. Zero means the other half's record of what it verified
/// is gone, and a comparison against nothing passes no matter what that
/// half does — the exact shape of failure this handshake replaces. More
/// than one means the wrong record could be read.
fn pinned_value(source: &str, declaration: &str, where_: &str) -> Result<String, String> {
    match source.matches(declaration).count() {
        0 => {
            return Err(format!(
                "{where_} no longer declares {declaration}…\": without it there is no record of \
                 which fixture that half verified, and this check would compare against nothing"
            ))
        }
        1 => {}
        n => {
            return Err(format!(
                "{where_} declares {declaration}…\" {n} times; the record has to be unambiguous \
                 or the wrong one can be read"
            ))
        }
    }
    let start = source.find(declaration).expect("counted one match") + declaration.len();
    let rest = &source[start..];
    match rest.find('"') {
        Some(end) => Ok(rest[..end].to_string()),
        None => Err(format!(
            "{where_}: the value assigned by {declaration}…\" is unterminated"
        )),
    }
}

/// The compiler-side tests this half requires to be declared and live.
///
/// The compiler's own half of the handshake is in the list on purpose.
/// Leaving it out would let it be removed while this half's view of the
/// compiler survived, which is half a handshake and reports as a whole
/// one.
const COMPILER_HALF_CHECKS: &[&str] = &[
    "TestSensitivityLeafParityAgainstSharedFixture",
    "TestSensitivitySlotParityAgainstSharedFixture",
    "TestSensitivityParityFixtureCoversEveryDivergenceShape",
    "TestProvidesPillarKeyParityAgainstSharedFixture",
    "TestBothHalvesPinTheSameFixtureRevision",
];

/// Reports a compiler-side test that is missing, commented out, or
/// turned into a no-op by skipping itself.
///
/// Only the named test's own body is read, rather than the whole file, so
/// a skip in some unrelated test cannot be mistaken for one here.
///
/// The declaration is required to open a LINE rather than merely to occur
/// somewhere, because a substring search would find a declaration that had
/// been commented out and report the check as live.
fn compiler_check_liveness(source: &str, name: &str) -> Option<String> {
    let signature = format!("func {name}(t *testing.T) {{");
    let declares_it = source
        .lines()
        .any(|line| line.trim_start().starts_with(&signature));
    let Some(body) = declares_it
        .then(|| delimited_block(source, &signature))
        .flatten()
    else {
        return Some(format!(
            "the compiler half no longer declares the check {name}. Either it was renamed — in \
             which case this list needs the new name — or it was removed or commented out, in \
             which case the compiler side of the comparison is not running and this half's green \
             says nothing about it"
        ));
    };
    body.contains("t.Skip").then(|| {
        format!(
            "the compiler half declares {name} and then skips it, so it does not run and the \
             parity claim covers one side only"
        )
    })
}

#[test]
fn both_halves_pin_the_same_fixture_revision() {
    let revision = fixture_revision(fixture_text().as_bytes());
    let mut failures: Vec<String> = Vec::new();

    if revision != SENSITIVITY_PARITY_FIXTURE_REVISION {
        failures.push(format!(
            "this half records having verified fixture revision \
             {SENSITIVITY_PARITY_FIXTURE_REVISION}, but the fixture it just read is revision \
             {revision}. The fixture changed and this half's record did not follow it; update the \
             recorded revision to {revision} in the same change that edits the fixture, and make \
             sure the other half's record moves too"
        ));
    }

    let compiler = strip_source_comments(&read_compiler_source(
        &["internal", "lintops", "sensitivity_parity_test.go"],
        "both_halves_pin_the_same_fixture_revision",
    ));
    match pinned_value(
        &compiler,
        "const sensitivityParityFixtureRevision = \"",
        "the compiler half",
    ) {
        Ok(pinned) if pinned != revision => failures.push(format!(
            "the compiler half records having verified fixture revision {pinned}, but the \
             fixture is revision {revision}. That half has not checked itself against the \
             current cases, so whatever this half reports covers one reader only. Re-run the \
             compiler half against the current fixture and update its recorded revision to \
             {revision}"
        )),
        Ok(_) => {}
        Err(message) => failures.push(message),
    }
    for name in COMPILER_HALF_CHECKS {
        if let Some(message) = compiler_check_liveness(&compiler, name) {
            failures.push(message);
        }
    }

    assert!(
        failures.is_empty(),
        "the two halves of the sensitivity parity check are not both verifying the current \
         fixture:\n  {}",
        failures.join("\n  ")
    );
}
