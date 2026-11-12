// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

//! Reads the annotation a Component states how its product's metrics
//! authenticate with.
//!
//! The annotation `observability.aggrega.ai/metrics-credential`, under a
//! Component's `metadata.annotations`, takes one of three values:
//!
//!   - `none` — the product serves its metrics with no credential;
//!   - `target-key` — its metrics take the key the Component requires of every
//!     caller, the one its `api_key` slot holds;
//!   - `separate-token` — its metrics take a token of their own, which is not
//!     the key the Component requires of its callers.
//!
//! The key and its values are a standard-catalog convention no schema keyword
//! marks. The compiler attaches no meaning to the annotation and carries it as
//! it carries any other; the publishing-linter rules read it, and they read only
//! these three values: an absent annotation, or any other value, reads as no
//! statement at all.
//!
//! Two rules read it, and both read it here, so the key, its values and what
//! counts as a statement have one home.

use serde_json::Value;

/// The annotation a Component states how its metrics authenticate with, by the
/// standard catalog's convention.
pub const ANNOTATION: &str = "observability.aggrega.ai/metrics-credential";

/// The value stating that the product serves its metrics with no credential.
pub const NONE: &str = "none";

/// The value stating that the product's metrics take the key the Component
/// requires of every caller.
pub const TARGET_KEY: &str = "target-key";

/// The value stating that the product's metrics take a token of their own
/// rather than the key the Component requires of its callers.
pub const SEPARATE_TOKEN: &str = "separate-token";

/// How a Component states its product's metrics authenticate.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum MetricsCredential {
    /// The metrics are served with no credential.
    NoCredential,
    /// The metrics take the key the Component requires of every caller.
    TargetKey,
    /// The metrics take a token of their own.
    SeparateToken,
}

impl MetricsCredential {
    /// `value` is the annotation value that states this, as an author writes it.
    pub fn value(self) -> &'static str {
        match self {
            MetricsCredential::NoCredential => NONE,
            MetricsCredential::TargetKey => TARGET_KEY,
            MetricsCredential::SeparateToken => SEPARATE_TOKEN,
        }
    }
}

/// `declared` reads the statement a Component document makes in its
/// annotation. `None` when the Component writes no such annotation, or writes
/// one whose value is not one of the three — a value the rules do not know
/// states nothing they can act on, so it is read exactly as an absent one.
pub fn declared(component: &Value) -> Option<MetricsCredential> {
    let value = component
        .get("metadata")
        .and_then(|metadata| metadata.get("annotations"))
        .and_then(|annotations| annotations.get(ANNOTATION))
        .and_then(Value::as_str)?;
    match value {
        NONE => Some(MetricsCredential::NoCredential),
        TARGET_KEY => Some(MetricsCredential::TargetKey),
        SEPARATE_TOKEN => Some(MetricsCredential::SeparateToken),
        _ => None,
    }
}
