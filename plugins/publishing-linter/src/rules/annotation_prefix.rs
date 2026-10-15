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
//!   below, and a parity check holds it and the public matrix to the
//!   same set of names — see that constant's own documentation.
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
                severity: "halt".to_string(),
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
                severity: "halt".to_string(),
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
/// bare-`aggrega.ai/` rule.
///
/// This array is one of the two homes of the platform's annotation
/// **reservation register** — the set of bare-prefix names the platform
/// owns. The other home is the matrix in
/// `aggrega-docs/language-reference/providers/well-known-annotations.md`.
/// Both homes carry the same set of names, and
/// `scripts/check-annotation-register-parity.sh` reads both, compares
/// them as sets, and fails on divergence, naming each key and the side
/// it is missing from. The two sides are therefore held together
/// mechanically: adding a name here without the matching public entry,
/// or the reverse, fails that check.
///
/// Both homes exist deliberately and neither is redundant. This array is
/// compiled into a guest that must build with no access to the
/// documentation tree, and the public matrix is the authoritative
/// human-facing statement of the vocabulary. Collapsing either into the
/// other would cost one of those two properties.
///
/// The register is a **reservation**, not a support claim. A name stays
/// listed once reserved even when no engine reads it, so an enterprise
/// catalog cannot take that name for its own meaning and collide when
/// the platform makes it live. The entries marked `reserved, not read`
/// have no consumer today. What an engine actually reads is the
/// separate, narrower statement each provider makes on its own
/// `wellKnownAnnotations` declaration.
const WELL_KNOWN_ANNOTATIONS: &[&str] = &[
    // Placement
    "aggrega.ai/namespace",
    "aggrega.ai/region",              // reserved, not read
    "aggrega.ai/account-id",          // reserved, not read
    "aggrega.ai/project-name",        // reserved, not read
    "aggrega.ai/kubernetes-version",
    // Registry
    "aggrega.ai/image-registry",
    "aggrega.ai/image-pull-secret",
    // Networking
    "aggrega.ai/cluster-domain",      // reserved, not read
    "aggrega.ai/ingress-class",       // reserved, not read
    "aggrega.ai/service-mesh",        // reserved, not read
    // Security
    "aggrega.ai/service-account",
    "aggrega.ai/pod-security-standard", // reserved, not read
    "aggrega.ai/iam-role",              // reserved, not read
    // Packaging
    "aggrega.ai/chart-name",
    "aggrega.ai/chart-version",       // reserved, not read
    "aggrega.ai/stack-name",          // reserved, not read
    "aggrega.ai/tf-backend",          // reserved, not read
    // Cloud target
    "aggrega.ai/aws-account",
    "aggrega.ai/aws-region",
    "aggrega.ai/aws-assume-role-arn",
    // Provenance labels emitted by providers
    "aggrega.ai/component-id",
    "aggrega.ai/recipe-uri",
    "aggrega.ai/boundary-path",
    "aggrega.ai/aggregate-name",
    "aggrega.ai/compiler-version",
    "aggrega.ai/environment",
    "aggrega.ai/sync-wave",
];

