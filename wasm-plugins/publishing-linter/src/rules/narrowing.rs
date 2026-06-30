// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//! Monotonic-narrowing rule on `extends` chains.
//!
//! When an entity uses `extends`, the child's schema MAY narrow the
//! parent's field types — broadening violates the
//! monotonic-narrowing policy when the workspace opts in. The
//! platform makes no claim that monotonic narrowing is the only
//! correct direction; legitimate broadening cases exist. The rule
//! defaults OFF; workspaces that prefer the discipline opt in via
//! `enforce_monotonic_narrowing: true`.
//!
//! Input model: the rule reads `LinterInput.proposed` (child entity)
//! and `LinterInput.parent` (resolved parent in the extends chain,
//! populated by the host caller before invocation). When `parent`
//! is `None`, the rule passes silently — either the entity doesn't
//! use `extends` or the host didn't resolve the chain; nothing to
//! enforce.
//!
//! Detection: conservative structural shrink. If the child's schema
//! at any field path is structurally LARGER than the parent's
//! (added object key; longer array; type changed; loosened
//! constraint), that's broadening = violation. JSON-Schema-semantic
//! equivalence refinements (e.g., `integer` → `number` widening IS
//! NOT subtractive in JSON Schema semantics) defer to a future
//! iteration.
//!
//! Annotation keywords (`title`, `description`, `examples`,
//! `default`, `readOnly`, `writeOnly`, `comment`, `$comment`,
//! `deprecated`) carry documentation, not constraint shape. Diffs
//! at those keys are excluded from broadening detection: a child
//! that revises its `description` or adds an `examples` entry is
//! not loosening the parent's constraints.
//!
//! The skip is position-aware: a JSON property literally named (e.g.)
//! `examples` under `properties.<name>` is a user-defined field, not
//! the annotation keyword in its native schema position. The walker
//! recognises `properties`, `patternProperties`, `$defs`,
//! `definitions`, and `dependentSchemas` as namespacing keys whose
//! immediate children are user-defined names, and never skips a key
//! that sits under one of them.
//!
//! Per-entity/path exemptions live in `policies.narrowing_exemptions`:
//! a violation at `entity X` `path P` is exempted when an exemption
//! entry has `entity == X` AND `paths` contains `P`.

use aggrega_pdk_rust::{LinterFinding, LinterInput, NarrowingExemption};
use serde_json::Value;

use crate::raw;

const ANNOTATION_KEYWORDS: &[&str] = &[
    "title",
    "description",
    "examples",
    "default",
    "readOnly",
    "writeOnly",
    "comment",
    "$comment",
    "deprecated",
];

// Keys whose immediate children are user-defined names, not JSON
// Schema keywords. A child segment under one of these is never
// treated as an annotation keyword, even if its spelling matches.
const NAMESPACING_KEYS: &[&str] = &[
    "properties",
    "patternProperties",
    "$defs",
    "definitions",
    "dependentSchemas",
];

pub fn classify(input: &LinterInput) -> Vec<LinterFinding> {
    let policies = &input.policies;

    // Default OFF — workspaces opt in explicitly.
    if !policies.enforce_monotonic_narrowing {
        return vec![];
    }

    let parent = match raw::parse_opt(&input.parent) {
        Some(p) => p,
        None => {
            // No parent supplied; nothing to compare against. The
            // entity may not use extends, or the host did not resolve
            // the chain. Either way, the rule has nothing to enforce.
            return vec![];
        }
    };

    let proposed = raw::parse(&input.proposed);

    let child_schema = match proposed.get("spec").and_then(|s| s.get("schema")) {
        Some(s) => s,
        None => return vec![],
    };
    let parent_schema = match parent.get("spec").and_then(|s| s.get("schema")) {
        Some(s) => s,
        None => return vec![],
    };

    let entity_name = proposed
        .get("metadata")
        .and_then(|m| m.get("name"))
        .and_then(|n| n.as_str())
        .unwrap_or("");

    let mut violations: Vec<String> = Vec::new();
    walk_for_broadening(parent_schema, child_schema, "spec.schema", &mut violations);

    let exemptions: &[NarrowingExemption] = &policies.narrowing_exemptions;
    violations
        .into_iter()
        .filter(|path| !is_exempted(entity_name, path, exemptions))
        .map(|path| LinterFinding {
            rule_id: "narrowing".to_string(),
            severity: "error".to_string(),
            message: format!(
                "entity {entity_name:?} extends-chain narrowing violation at {path}: \
                 child's schema is broader than parent's. The workspace's \
                 enforce_monotonic_narrowing flag is on; suppress per-path via \
                 narrowing_exemptions[] or workspace-wide by flipping the flag false."
            ),
            ..Default::default()
        })
        .collect()
}

fn is_exempted(entity_name: &str, path: &str, exemptions: &[NarrowingExemption]) -> bool {
    exemptions
        .iter()
        .any(|ex| ex.entity == entity_name && ex.paths.iter().any(|p| p == path))
}

/// `is_annotation_position` decides whether `key`, sitting under the
/// container reached by `path_to_parent`, is a JSON Schema annotation
/// keyword in its native position (skip) versus a user-defined name
/// that happens to match an annotation keyword (walk).
///
/// The rule: if `key` matches an annotation keyword AND the immediate
/// segment of `path_to_parent` is not a JSON Schema namespacing key
/// (`properties`, `patternProperties`, `$defs`, `definitions`,
/// `dependentSchemas`), the position is native — skip. Otherwise walk.
fn is_annotation_position(path_to_parent: &str, key: &str) -> bool {
    if !ANNOTATION_KEYWORDS.contains(&key) {
        return false;
    }
    match path_to_parent.rsplit('.').next() {
        Some(parent_seg) if NAMESPACING_KEYS.contains(&parent_seg) => false,
        _ => true,
    }
}

/// `walk_for_broadening` walks parent and child in lockstep at the
/// same dotted field path; a child being structurally LARGER than
/// the parent counts as broadening. Conservative: any structural
/// growth at any depth fires.
///
/// Annotation keywords in their native schema position are skipped —
/// they carry documentation, not constraint shape, so diffs there
/// are never broadening. Position is taken into account: a property
/// literally named `examples` (or any other annotation-keyword name)
/// under `properties.<name>` is a user-defined field and its subtree
/// is walked normally.
fn walk_for_broadening(parent: &Value, child: &Value, path: &str, violations: &mut Vec<String>) {
    if let Some((parent_path, last_seg)) = path.rsplit_once('.') {
        if is_annotation_position(parent_path, last_seg) {
            return;
        }
    }
    match (parent, child) {
        (Value::Object(p_map), Value::Object(c_map)) => {
            // A child key not in parent is broadening (added field),
            // unless the added key is an annotation keyword sitting
            // at a JSON Schema annotation position.
            for c_key in c_map.keys() {
                if is_annotation_position(path, c_key) {
                    continue;
                }
                if !p_map.contains_key(c_key) {
                    violations.push(format!("{path}.{c_key} (added in child)"));
                }
            }
            // Recurse on keys present in both, comparing sub-structures.
            for (key, p_val) in p_map {
                if let Some(c_val) = c_map.get(key) {
                    walk_for_broadening(p_val, c_val, &format!("{path}.{key}"), violations);
                }
            }
        }
        (Value::Array(p_arr), Value::Array(c_arr)) => {
            if c_arr.len() > p_arr.len() {
                violations.push(format!("{path} (array grew from {} to {})", p_arr.len(), c_arr.len()));
            }
            // Compare same-index elements conservatively.
            for (i, p_item) in p_arr.iter().enumerate() {
                if let Some(c_item) = c_arr.get(i) {
                    walk_for_broadening(p_item, c_item, &format!("{path}[{i}]"), violations);
                }
            }
        }
        (p, c) if p == c => {
            // Identical scalars / null — no diff.
        }
        (p, c) => {
            // Type change or scalar difference. Conservative: count
            // as broadening when the discriminant differs OR when a
            // scalar value changes in a direction that could indicate
            // loosened constraint. The simple equality check above
            // already ruled out byte-identical values; anything left
            // is a meaningful change.
            if std::mem::discriminant(p) != std::mem::discriminant(c) {
                violations.push(format!("{path} (type changed)"));
            } else {
                // Same type, different value. Conservative: report
                // as a violation. JSON-Schema-semantic-equivalence
                // refinements (e.g., `type: number` widening from
                // `type: integer` SHOULD be permitted) defer to a
                // future iteration.
                violations.push(format!("{path} (value changed)"));
            }
        }
    }
}
