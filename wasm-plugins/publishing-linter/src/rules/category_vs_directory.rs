// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//! Reject Components whose component.aggrega.ai/category label does
//! not match their filesystem path's first directory segment under
//! core/.
//!
//! The standard catalog organizes Components under
//! `core/<category>/<name>/...` where `<category>` is one of fifteen
//! closed-set values: ai-agent, ai-model, ai-tool, ai-pipeline,
//! datastore, gateway, mesh, network, messaging, storage, compute,
//! observability, security, bigdata, devtool. The
//! component.aggrega.ai/category label MUST mirror that directory
//! segment.
//!
//! Default: ON for Components under core/. Per-workspace opt-out:
//! `allow_category_mismatches: true` under `lint.publishing_discipline`
//! in `.aggrega-config.yaml` uniformly suppresses every finding from
//! this rule. The rule also scopes itself to `/core/<category>/<...>`
//! paths; entities outside core/ (e.g., enterprise-namespace) pass
//! silently regardless of the flag.
//!
//! The workspace-flag guard fires BEFORE any path derivation so the
//! same suppression covers mismatch findings, missing-label findings,
//! and unknown-category findings uniformly. Future maintainers must
//! preserve that invariant; the test
//! `category_workspace_flag_suppresses_unknown_category_too` pins it.

use aggrega_pdk_rust::{LinterFinding, LinterInput, LinterSeverity};

use crate::policies;

const KNOWN_CATEGORIES: &[&str] = &[
    "ai-agent",
    "ai-model",
    "ai-tool",
    "ai-pipeline",
    "datastore",
    "gateway",
    "mesh",
    "network",
    "messaging",
    "storage",
    "compute",
    "observability",
    "security",
    "bigdata",
    "devtool",
];

pub fn classify(input: &LinterInput) -> Vec<LinterFinding> {
    if input.entity_kind != "Component" {
        return vec![];
    }
    let source_path = match input.source_path.as_deref() {
        Some(p) => p,
        None => return vec![],
    };

    let policies = policies::effective(input.policies.as_ref());
    if policies.allow_category_mismatches {
        return vec![];
    }

    let derived = match derive_core_category(source_path) {
        Some(c) => c,
        None => return vec![],
    };
    if !KNOWN_CATEGORIES.contains(&derived.as_str()) {
        return vec![finding(format!(
            "Component at {source_path:?} sits under core/{derived}/ \
             but {derived:?} is not in the closed set of standard-catalog \
             categories ({KNOWN_CATEGORIES:?})"
        ))];
    }
    let label = input
        .proposed
        .get("metadata")
        .and_then(|m| m.get("labels"))
        .and_then(|l| l.get("component.aggrega.ai/category"))
        .and_then(|v| v.as_str());
    match label {
        Some(actual) if actual == derived => vec![],
        Some(actual) => vec![finding(format!(
            "Component at {source_path:?} declares component.aggrega.ai/category={actual:?} \
             but its path derives category={derived:?}; standard-catalog convention \
             requires the label to match the first directory segment under core/"
        ))],
        None => vec![finding(format!(
            "Component at {source_path:?} is missing the component.aggrega.ai/category label; \
             standard-catalog convention requires every core/{derived}/... entry to \
             declare component.aggrega.ai/category={derived:?}"
        ))],
    }
}

fn derive_core_category(path: &str) -> Option<String> {
    let segments: Vec<&str> = path.split('/').collect();
    let core_idx = segments.iter().position(|&s| s == "core")?;
    let category = segments.get(core_idx + 1)?;
    segments.get(core_idx + 2)?;
    Some((*category).to_string())
}

fn finding(message: String) -> LinterFinding {
    LinterFinding {
        rule_id: "category-matches-directory".to_string(),
        severity: LinterSeverity::Error,
        classification: None,
        message,
    }
}
