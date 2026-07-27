// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//! Advise when an attachment slot accepts only secret-kind material but
//! does not declare that it handles it.
//!
//! An entity declares an attachment slot under `spec.featuresSchema`. A
//! slot may constrain which Feature can fill it with a label selector,
//! and it may separately declare `sensitive: true` to record that
//! whatever fills it handles secret material. The two say different
//! things: the selector states which fills are admissible, and a reader
//! infers the handling from it; the marker states the handling itself.
//! So a slot whose selector pins the Feature kind to `secret` while
//! declaring no marker has a fact about it that every reader has to
//! re-derive from the selector rather than read.
//!
//! The rule is a standard-catalog convention, not a constraint. It emits
//! a non-halting `warning`: nothing is rejected, and an enterprise
//! catalog that declares slots differently is free to.
//!
//! Scope and limits, both deliberate:
//!
//!   - It reads the linted entity alone, which suits how slots compose:
//!     re-declaring a slot on an extending entity replaces the inherited
//!     slot whole rather than merging it field by field, so an entity
//!     that declares a slot's selector declares that slot's marker too,
//!     or declares it absent. A slot an entity does not re-declare is
//!     not on that entity to read, and is read where it is declared.
//!   - It fires only where the selector *pins* the kind to `secret` —
//!     `kind=secret`, or a set membership whose only member is `secret`.
//!     A selector that merely permits `secret` alongside other kinds is
//!     not a claim that the slot handles secret material.

use aggrega_pdk_rust::{Diagnostic, LinterInput};

use crate::{raw, sensitivity};

/// The rule's identifier, as the host dispatches it.
pub const RULE_ID: &str = "secret-slot-declares-sensitive";

/// The label a Feature carries to name what it produces, and the value
/// that names secret material.
pub const KIND_LABEL: &str = "feature.aggrega.ai/kind";
pub const SECRET_KIND: &str = "secret";

/// The label-selector operator tokens this rule reads. The selector
/// grammar is the host's; these are the tokens that can pin a key to a
/// single value (`=`) or to a value set (` in (`), plus the two that
/// exclude a value and therefore never pin (`!=`, ` notin (`) and the
/// negation prefix. A bare key tests existence and pins nothing.
///
/// Each token is declared once and the clause classifier reads it from
/// here, so the documented vocabulary and the matched vocabulary cannot
/// drift apart. `tests/sensitivity_parity_test.rs` pins the set against
/// the selector parser's own operator set.
pub const OP_EQUALS: &str = "=";
pub const OP_NOT_EQUALS: &str = "!=";
pub const OP_IN: &str = " in (";
pub const OP_NOT_IN: &str = " notin (";
pub const OP_NEGATION: &str = "!";

/// The tokens above as one set, for the vocabulary pin.
pub const SELECTOR_OPERATOR_TOKENS: &[&str] =
    &[OP_EQUALS, OP_NOT_EQUALS, OP_IN, OP_NOT_IN, OP_NEGATION];

pub fn classify(input: &LinterInput) -> Vec<Diagnostic> {
    // Both entity kinds that can declare attachment slots.
    if input.entity_kind != "Component" && input.entity_kind != "Feature" {
        return vec![];
    }
    let proposed = raw::parse(&input.proposed);
    let Some(slots) = proposed
        .get("spec")
        .and_then(|s| s.get("featuresSchema"))
        .and_then(serde_json::Value::as_object)
    else {
        return vec![];
    };

    let mut findings = Vec::new();
    for (name, slot) in slots {
        let Some(selector) = slot.get("selector").and_then(serde_json::Value::as_str) else {
            continue;
        };
        if !selector_pins_secret_kind(selector) {
            continue;
        }
        if sensitivity::slot_declares_sensitive(slot) {
            continue;
        }
        findings.push(finding(name, selector));
    }
    findings
}

/// `selector_pins_secret_kind` reports whether a slot selector admits
/// only a Feature whose kind label is `secret`. Clauses combine with AND,
/// so one pinning clause is enough regardless of what the others say.
fn selector_pins_secret_kind(selector: &str) -> bool {
    split_clauses(selector)
        .into_iter()
        .any(|clause| clause_pins_secret_kind(clause.trim()))
}

/// Splits a selector on the commas that are not inside a value set,
/// mirroring how the selector grammar separates clauses.
fn split_clauses(selector: &str) -> Vec<&str> {
    let mut parts = Vec::new();
    let mut depth = 0usize;
    let mut start = 0usize;
    for (i, ch) in selector.char_indices() {
        match ch {
            '(' => depth += 1,
            ')' => depth = depth.saturating_sub(1),
            ',' if depth == 0 => {
                parts.push(&selector[start..i]);
                start = i + ch.len_utf8();
            }
            _ => {}
        }
    }
    parts.push(&selector[start..]);
    parts
}

/// Classifies one clause. The token order matches the selector grammar's
/// own precedence, which matters because an inequality contains the
/// equality token: negation first, then the two set forms, then
/// inequality, then equality, then the bare-key existence form.
fn clause_pins_secret_kind(clause: &str) -> bool {
    if clause.starts_with(OP_NEGATION) {
        return false;
    }
    if let Some(idx) = clause.find(OP_IN) {
        if idx > 0 {
            // The set token carries its own opening bracket, so the value set
            // starts one byte before the token ends.
            let set = &clause[idx + OP_IN.len() - 1..];
            return clause[..idx].trim() == KIND_LABEL && value_set_is_only_secret(set);
        }
    }
    if let Some(idx) = clause.find(OP_NOT_IN) {
        if idx > 0 {
            return false;
        }
    }
    if let Some(idx) = clause.find(OP_NOT_EQUALS) {
        if idx > 0 {
            return false;
        }
    }
    if let Some(idx) = clause.find(OP_EQUALS) {
        if idx > 0 {
            return clause[..idx].trim() == KIND_LABEL
                && clause[idx + OP_EQUALS.len()..].trim() == SECRET_KIND;
        }
    }
    false
}

/// Reports whether a parenthesised value set holds `secret` and nothing
/// else. A set with a second member admits a non-secret fill, so it does
/// not pin.
fn value_set_is_only_secret(rest: &str) -> bool {
    let rest = rest.trim();
    let Some(inner) = rest
        .strip_prefix('(')
        .and_then(|body| body.strip_suffix(')'))
    else {
        return false;
    };
    let values: Vec<&str> = inner
        .split(',')
        .map(str::trim)
        .filter(|v| !v.is_empty())
        .collect();
    values == [SECRET_KIND]
}

fn finding(slot_name: &str, selector: &str) -> Diagnostic {
    Diagnostic {
        rule_id: RULE_ID.to_string(),
        severity: "warning".to_string(),
        message: format!(
            "attachment slot {slot_name:?} pins {KIND_LABEL} to {SECRET_KIND:?} in its selector \
             ({selector:?}) but does not declare sensitive: true. Only a secret-kind Feature can \
             fill the slot, so the slot handles secret material; the standard-catalog convention \
             declares sensitive: true on such a slot, which is what makes the handling readable \
             by inspection and policy tooling rather than only by reading the selector."
        ),
        ..Default::default()
    }
}
