// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//! Reserved-prefix discipline for catalog-entity annotation keys.
//!
//! The `aggrega.ai/` and `feature.aggrega.ai/` prefixes carry reserved
//! semantics:
//!
//! - **Bare `aggrega.ai/`** is platform-native. A pre-defined set of
//!   well-known annotation keys (per
//!   `aggrega-docs/language-reference/providers/well-known-annotations.md`)
//!   IS the canonical usage; everything else using the bare prefix is
//!   namespace pollution. The list is carried as a static constant
//!   below and lockstep-maintained with the public doc.
//! - **`feature.aggrega.ai/`** is reserved for Feature-scoped
//!   conventions. The Feature kind is the canonical context (e.g.,
//!   `feature.aggrega.ai/kind: secret` on Feature labels — compiler
//!   enforcement, separate from this rule). For catalog entities of
//!   any other kind, `feature.aggrega.ai/<key>` in
//!   `metadata.annotations` is outside the canonical context; the
//!   author likely meant to put the key on a Feature entity, or meant
//!   a custom domain.
//!
//! Scope: catalog-entity `metadata.annotations` only. Out of scope:
//! Feature `metadata.labels` (compiler-owned), Manifest
//! `features.annotations[]` (deployment-side; advisory namespace),
//! Release `annotations` (deployment-side; advisory). Those surfaces
//! have separate governance.
//!
//! Per-workspace opt-out: `policies.allow_aggrega_ai_prefix == true`
//! suppresses both checks. The flag covers the reserved-namespace
//! family uniformly; per-prefix granularity is a future iteration.

use aggrega_pdk_rust::{Diagnostic, LinterInput};

use crate::raw;

/// `classify` walks the entity's `metadata.annotations` object and
/// emits one finding per reserved-prefix violation. Returns an empty
/// vector when no violations are found OR when the per-workspace
/// opt-out is set.
pub fn classify(input: &LinterInput) -> Vec<Diagnostic> {
    let policies = &input.policies;
    if policies.allow_aggrega_ai_prefix {
        return vec![];
    }

    let proposed = raw::parse(&input.proposed);
    let annotations = match proposed
        .get("metadata")
        .and_then(|m| m.get("annotations"))
        .and_then(|a| a.as_object())
    {
        Some(map) => map,
        None => return vec![],
    };

    let entity_kind = input.entity_kind.as_str();
    let mut findings = Vec::new();

    for key in annotations.keys() {
        if is_bare_aggrega_ai(key) && !WELL_KNOWN_ANNOTATIONS.contains(&key.as_str()) {
            findings.push(Diagnostic {
                rule_id: "annotation-prefix".to_string(),
                severity: "error".to_string(),
                message: format!(
                    "annotation key {key:?} uses the reserved bare aggrega.ai/ prefix; \
                     this prefix is reserved for platform-native annotations. Use a sub-prefix \
                     (e.g., feature.aggrega.ai/ for Feature-scoped conventions) or a custom non-aggrega.ai/ prefix."
                ),
                ..Default::default()
            });
            continue;
        }
        if is_feature_aggrega_ai(key) && entity_kind != "Feature" {
            findings.push(Diagnostic {
                rule_id: "annotation-prefix".to_string(),
                severity: "error".to_string(),
                message: format!(
                    "annotation key {key:?} uses the feature.aggrega.ai/ prefix on entity kind {entity_kind:?}; \
                     this prefix is reserved for Feature-scoped conventions. \
                     Use a custom non-aggrega.ai/ prefix for cross-kind annotations."
                ),
                ..Default::default()
            });
        }
    }

    findings
}

fn is_bare_aggrega_ai(key: &str) -> bool {
    // Match any key whose prefix is the bare `aggrega.ai/` form,
    // regardless of how many path segments follow. Examples that
    // match: `aggrega.ai/custom`, `aggrega.ai/team/payments/x`.
    // The well-known-list check exempts the canonical platform
    // annotation keys (the doc-documented single-segment forms);
    // every other shape under the bare prefix is namespace pollution
    // and gets flagged. Sub-prefixes like `feature.aggrega.ai/...`
    // are handled by the separate is_feature_aggrega_ai check, not
    // this one — strip_prefix above only succeeds for keys that
    // literally start with `aggrega.ai/`.
    if let Some(rest) = key.strip_prefix("aggrega.ai/") {
        return !rest.is_empty();
    }
    false
}

fn is_feature_aggrega_ai(key: &str) -> bool {
    if let Some(rest) = key.strip_prefix("feature.aggrega.ai/") {
        return !rest.is_empty();
    }
    false
}

/// Well-known platform-native annotation keys exempt from the
/// bare-`aggrega.ai/` rule. Derived from
/// `aggrega-docs/language-reference/providers/well-known-annotations.md`
/// (Categories 1-5 + provenance labels). The list MUST be maintained
/// in lockstep with the public doc; if the platform adds new
/// well-known annotations, this array gains the matching entries.
const WELL_KNOWN_ANNOTATIONS: &[&str] = &[
    // Placement
    "aggrega.ai/namespace",
    "aggrega.ai/region",
    "aggrega.ai/account-id",
    "aggrega.ai/project-name",
    // Registry
    "aggrega.ai/image-registry",
    "aggrega.ai/image-pull-secret",
    // Networking
    "aggrega.ai/cluster-domain",
    "aggrega.ai/ingress-class",
    "aggrega.ai/service-mesh",
    // Security
    "aggrega.ai/service-account",
    "aggrega.ai/pod-security-standard",
    "aggrega.ai/iam-role",
    // Packaging
    "aggrega.ai/chart-name",
    "aggrega.ai/chart-version",
    "aggrega.ai/stack-name",
    "aggrega.ai/tf-backend",
    // Provenance labels emitted by providers
    "aggrega.ai/component-id",
    "aggrega.ai/recipe-uri",
    "aggrega.ai/boundary-path",
    "aggrega.ai/aggregate-name",
    "aggrega.ai/compiler-version",
    "aggrega.ai/environment",
    "aggrega.ai/sync-wave",
];

