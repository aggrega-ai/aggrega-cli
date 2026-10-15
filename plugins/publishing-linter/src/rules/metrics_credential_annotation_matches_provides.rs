// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//! Advise where a Component's statement of how its metrics authenticate and
//! its declaration of whether an instance's metrics need a credential
//! disagree.
//!
//! A Component can state two facts about its metrics, and they are one fact
//! about the product seen from two sides:
//!
//!   - the annotation `observability.aggrega.ai/metrics-credential` states
//!     how the product's metrics authenticate — `none`, they are served with
//!     no credential; `target-key`, they take the key the Component requires
//!     of its callers; `separate-token`, they take a token of their own;
//!   - the provides key `metrics_credential_required`, declared in the
//!     `provides` pillar of `spec.schema`, is the key each Recipe publishes to
//!     say whether that instance's metrics need a credential. It is published
//!     per instance because the answer can depend on how an instance is
//!     configured — metrics that take the caller key need one only while the
//!     instance checks a key at all — which no annotation on the Component can
//!     express.
//!
//! So neither stands in for the other, and the two must agree. A Component
//! annotated `none` has metrics no instance needs a credential for, so it
//! declares no such key; one annotated `target-key` or `separate-token` has
//! metrics that take a credential, so it declares the key its Recipes publish.
//!
//! The rule is a standard-catalog convention, not a constraint. The build
//! attaches no meaning to the annotation and does not compare it with any
//! declaration, so the rule emits a non-halting `warn`.
//!
//! Scope and limits, each deliberate:
//!
//!   - It reads only the annotation's three values. A Component writing no
//!     such annotation, or writing any other value, draws nothing.
//!   - It reads the linted Component alone, as it is written. A Component
//!     extending another writes `schema: inherited` and carries its base's
//!     pillars, which are not in the document read here, so it draws nothing;
//!     an annotation it only inherits is not in the document either. Its base
//!     is read where it is declared.
//!   - It reads the `provides` pillar one level deep, which is where a
//!     provides key is declared. Where the pillar is a shape the reader cannot
//!     enumerate — a reference, a composition, or an escaped body written as
//!     text — nothing is known about it and the rule stays silent.

use std::collections::BTreeSet;

use aggrega_pdk_rust::{Diagnostic, LinterInput};
use serde_json::Value;

use crate::metrics_credential::{self, MetricsCredential, ANNOTATION};
use crate::{raw, sensitivity};

/// The rule's identifier, as the host dispatches it.
pub const RULE_ID: &str = "metrics-credential-annotation-matches-provides";

/// The provides key a Recipe publishes to say whether an instance's metrics
/// need a credential, by the standard catalog's convention.
pub const REQUIRED_KEY: &str = "metrics_credential_required";

/// The authored pillar the key is declared in.
const PROVIDES_PILLAR: &str = "provides";

pub fn classify(input: &LinterInput) -> Vec<Diagnostic> {
    if input.entity_kind != "Component" {
        return vec![];
    }
    let component = raw::parse(&input.proposed);
    let Some(stated) = metrics_credential::declared(&component) else {
        return vec![];
    };
    // An unreadable pillar is not an empty one: reporting the key missing from a
    // pillar this reader could not open would be a finding about not having
    // looked.
    let Some(declared) = declared_provides(&component) else {
        return vec![];
    };
    let declares_key = declared.contains(REQUIRED_KEY);
    let finding = match stated {
        MetricsCredential::NoCredential if declares_key => Some(Mismatch::Extra),
        MetricsCredential::TargetKey | MetricsCredential::SeparateToken if !declares_key => {
            Some(Mismatch::Missing)
        }
        _ => None,
    };
    finding
        .map(|mismatch| vec![finding_for(mismatch, stated, &entity_name(&component))])
        .unwrap_or_default()
}

/// The provides keys a Component declares in its own `spec.schema`, or `None`
/// where they cannot be read from the document: a Component extending another
/// carries its base's schema rather than one of its own, and a pillar whose
/// own node the reader cannot enumerate holds keys it cannot see. A Component
/// declaring no schema, or no provides pillar, declares no key, which is a real
/// answer.
fn declared_provides(component: &Value) -> Option<BTreeSet<String>> {
    let spec = component.get("spec");
    if spec.and_then(|s| s.get("extends")).is_some() {
        return None;
    }
    match spec.and_then(|s| s.get("schema")) {
        None => Some(BTreeSet::new()),
        Some(schema) if schema.is_object() => {
            sensitivity::pillar_property_names(schema, PROVIDES_PILLAR)
        }
        Some(_) => None,
    }
}

/// The Component's authored name, for the finding to point at. Empty rather
/// than absent when the entity carries none, so the message shape does not
/// change on a malformed entity.
fn entity_name(component: &Value) -> String {
    component
        .get("metadata")
        .and_then(|m| m.get("name"))
        .and_then(Value::as_str)
        .unwrap_or_default()
        .to_string()
}

/// The two ways the annotation and the declaration can disagree.
enum Mismatch {
    /// The annotation says the metrics take no credential, and the key is
    /// declared.
    Extra,
    /// The annotation says the metrics take a credential, and the key is not
    /// declared.
    Missing,
}

/// What each value says, in the words a finding uses.
fn says(stated: MetricsCredential) -> &'static str {
    match stated {
        MetricsCredential::NoCredential => "its metrics are served with no credential",
        MetricsCredential::TargetKey => "its metrics take the key it requires of every caller",
        MetricsCredential::SeparateToken => {
            "its metrics take a token of their own rather than the key it requires of its callers"
        }
    }
}

fn finding_for(mismatch: Mismatch, stated: MetricsCredential, name: &str) -> Diagnostic {
    let value = stated.value();
    let saying = says(stated);
    let disagreement = match mismatch {
        Mismatch::Extra => format!(
            "component {name:?} is annotated {ANNOTATION}: {value:?}, saying {saying}, but it declares \
             the provides key {REQUIRED_KEY:?} in the provides pillar of its schema, which its \
             instances publish to say whether their metrics need a credential. A component whose \
             metrics take no credential has nothing for an instance to require, so it declares no \
             such key: removing the key, or annotating how its metrics do authenticate, makes the two \
             agree."
        ),
        Mismatch::Missing => format!(
            "component {name:?} is annotated {ANNOTATION}: {value:?}, saying {saying}, but it does not \
             declare the provides key {REQUIRED_KEY:?} in the provides pillar of its schema, so no \
             instance publishes whether its metrics need that credential. A component whose metrics \
             take a credential declares the key and each of its recipes publishes it: declaring the \
             key, or annotating {none:?} where its metrics take no credential, makes the two agree.",
            none = metrics_credential::NONE
        ),
    };
    Diagnostic {
        rule_id: RULE_ID.to_string(),
        severity: "warn".to_string(),
        message: format!(
            "{disagreement} The two state one fact about the product — the annotation how its metrics \
             authenticate, the published key whether an instance's metrics need a credential — so they \
             must agree; that agreement is the standard-catalog convention, and the build does not \
             check it."
        ),
        ..Default::default()
    }
}
