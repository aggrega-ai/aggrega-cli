// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

//! Advise when a Recipe publishes a `provides` key the Component it
//! implements does not declare.
//!
//! Two surfaces name the same output contract from opposite sides. A
//! Recipe's `spec.provides` is what it actually publishes: one entry per
//! key, each a template the build evaluates. The Component's `provides`
//! pillar, under `spec.schema`, is what the type declares it exposes —
//! and it is that declaration, not the Recipe's bag, an `inject` `from:`
//! reference is checked against. So a key a Recipe publishes without the
//! Component declaring it is a key that is produced and then refused
//! where a consumer wires it.
//!
//! The rule is a standard-catalog convention, not a constraint. A Recipe
//! publishing more than its Component declares is accepted at build time,
//! so the rule emits a non-halting `warn` and an enterprise catalog that
//! deliberately publishes beyond the declared contract may opt out.
//!
//! Scope and limits, all three deliberate:
//!
//!   - It covers one direction only. The opposite direction — a Component
//!     declaring a key its Recipe never publishes — already halts the
//!     build, so a second report of it would be noise.
//!   - It compares against the Component the host resolved and handed
//!     over. Where the host resolved none, there is nothing to compare
//!     against and the rule is silent; an absent reference is never read
//!     as an empty contract.
//!   - It reads the Component's pillar one level deep, which is the level
//!     an `inject` `from:` reference names. A nested property is not a
//!     published key.

use std::collections::BTreeSet;

use aggrega_pdk_rust::{Diagnostic, LinterInput};
use serde_json::Value;

use crate::{raw, sensitivity};

/// The rule's identifier, as the host dispatches it.
pub const RULE_ID: &str = "provides-declared-by-component";

/// The authored key naming the contract on both sides: the Recipe's
/// published bag and the Component schema's pillar are spelled the same,
/// which is what makes the two readable as one contract.
pub const PROVIDES_KEY: &str = "provides";

pub fn classify(input: &LinterInput) -> Vec<Diagnostic> {
    if input.entity_kind != "Recipe" {
        return vec![];
    }
    if input.policies.allow_undeclared_recipe_provides {
        return vec![];
    }
    let Some(component) = raw::parse_opt(&input.implemented_component) else {
        return vec![];
    };
    let published = published_keys(&raw::parse(&input.proposed));
    if published.is_empty() {
        return vec![];
    }
    // An unreadable pillar is not an empty one. Reporting every published key
    // against a contract this reader could not open would name the whole bag as
    // undeclared on the strength of not having looked.
    let Some(declared) = declared_keys(&component) else {
        return vec![];
    };
    let component_name = entity_name(&component);
    let declared_list = describe_declared(&declared);
    published
        .into_iter()
        .filter(|key| !declared.contains(key))
        .map(|key| finding(&key, &component_name, &declared_list))
        .collect()
}

/// The keys a Recipe publishes, read from `spec.provides`.
///
/// Collected into an ordered set rather than yielded in the order the
/// parsed document happens to hold them: the findings a rule emits are
/// part of the output, and an output whose order depends on a parser's
/// map implementation is not reproducible.
fn published_keys(recipe: &Value) -> BTreeSet<String> {
    recipe
        .get("spec")
        .and_then(|spec| spec.get(PROVIDES_KEY))
        .and_then(Value::as_object)
        .map(|bag| bag.keys().cloned().collect())
        .unwrap_or_default()
}

/// The keys a Component declares, read from the `provides` pillar of its
/// `spec.schema`. `None` when that pillar is a shape the schema reader
/// cannot enumerate.
fn declared_keys(component: &Value) -> Option<BTreeSet<String>> {
    let Some(schema) = component.get("spec").and_then(|spec| spec.get("schema")) else {
        return Some(BTreeSet::new());
    };
    sensitivity::pillar_property_names(schema, PROVIDES_KEY)
}

/// The Component's authored name, for the finding to point at. Empty
/// rather than absent when the entity carries none, so the message shape
/// does not change on a malformed entity.
fn entity_name(component: &Value) -> String {
    component
        .get("metadata")
        .and_then(|m| m.get("name"))
        .and_then(Value::as_str)
        .unwrap_or_default()
        .to_string()
}

/// The declared set as the finding prints it. A Component that declares
/// nothing is said so in words: an empty list rendered as a bare pair of
/// brackets reads as a formatting fault rather than as the finding's most
/// important fact.
fn describe_declared(declared: &BTreeSet<String>) -> String {
    if declared.is_empty() {
        return "it declares no provides pillar at all".to_string();
    }
    format!(
        "it declares {}",
        declared
            .iter()
            .map(|k| format!("{k:?}"))
            .collect::<Vec<_>>()
            .join(", ")
    )
}

fn finding(key: &str, component_name: &str, declared_list: &str) -> Diagnostic {
    Diagnostic {
        rule_id: RULE_ID.to_string(),
        severity: "warn".to_string(),
        message: format!(
            "this recipe publishes the provides key {key:?}, which component {component_name:?} \
             does not declare in the provides pillar of its schema — {declared_list}. Publishing \
             a key the component does not declare is accepted when the build renders, so nothing \
             is refused here; an inject from: reference to that key does halt, because such a \
             reference is checked against the component's declared contract rather than against \
             what a recipe publishes. Declaring {key:?} on the component is what makes the two \
             agree, and it is the standard-catalog convention."
        ),
        ..Default::default()
    }
}
