// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//! Reads sensitivity declarations out of an authored entity's schema.
//!
//! Two declarations carry the axis, and this module reads both from the
//! authored entity JSON a rule receives:
//!
//!   - a schema leaf in `values`, `infra` or `provides` declaring
//!     `sensitive: true`, meaning that datum is secret material;
//!   - an attachment slot in `featuresSchema` declaring `sensitive:
//!     true`, meaning whatever fills the slot handles secret material.
//!
//! ## Why this re-derivation exists, and what holds it
//!
//! The compiler reads the same declarations, and it is the source of
//! truth for them — but it reads the *compiled* schema, where a leaf's
//! declaration has already become the `x-aggrega-sensitive` extension
//! keyword. A plugin guest cannot run schema compilation: it receives
//! the *authored* entity, so it must read the authored spelling
//! (`sensitive: true`) and reproduce the compiler's dispatch to know
//! whether a given mapping is a leaf carrying the marker, an object
//! whose children carry it, or authored JSON Schema where the same key
//! is ordinary content. There is no call that could replace this across
//! the plugin boundary, so the duplication is structural rather than a
//! choice.
//!
//! Because it is a duplication, it is held by a test rather than by eye:
//! `tests/sensitivity_parity_test.rs` runs the cases in
//! `tests/fixtures/sensitivity_parity.json` through this reader, the
//! compiler side runs the same cases through its own, and both assert
//! the same per-pillar path sets. The keyword vocabularies below are
//! pinned against their source-of-truth declarations by the same test.
//! Do not "unify" this module into a direct call — none can exist — and
//! do not trust it without that test.
//!
//! ## What is deliberately not read
//!
//! The walk descends only through property maps, so a declaration
//! reached only through a reference target, a composing branch, or an
//! array's element type is not collected. That is the same line the
//! compiler's reader draws.
//!
//! One divergence is real and reported rather than hidden: an escaped
//! JSON Schema body authored as a YAML text block needs a YAML parser to
//! open, and this crate has none. Paths blocked that way are returned in
//! [`DeclaredSensitiveLeaves::unreadable_escapes`] so a caller that must
//! not be wrong can degrade instead of reading silence as an answer.

use std::collections::BTreeSet;

use serde_json::{Map, Value};

/// The compact-form keywords whose presence on a mapping makes that
/// mapping a constraint leaf rather than an object scope. Mirrors the
/// compact-form keyword vocabulary in `pkg/compactform/vocabulary.go`
/// (the type-and-identity, constraint and Aggrega-specific sets, plus
/// the one composition keyword that acts as a leaf constraint).
pub const COMPACT_KEYWORDS: &[&str] = &[
    "type",
    "description",
    "default",
    "required",
    "enum",
    "min",
    "max",
    "minLength",
    "maxLength",
    "pattern",
    "format",
    "valuesType",
    "validate",
    "validateMessage",
    "when",
    "sensitive",
    "$jsonschema",
    "$mode",
];

/// The JSON Schema structural keywords whose presence steers a mapping
/// to the authored-JSON-Schema pathway. Mirrors the structural list in
/// `pkg/compactform/dispatch.go`.
pub const SCHEMA_CONTAINER_KEYWORDS: &[&str] = &[
    "properties",
    "items",
    "additionalProperties",
    "if",
    "then",
    "else",
    "not",
    "patternProperties",
    "propertyNames",
    "prefixItems",
    "contains",
    "minContains",
    "maxContains",
    "dependentRequired",
    "dependentSchemas",
    "unevaluatedItems",
    "unevaluatedProperties",
];

/// The authored key a schema leaf and an attachment slot both declare
/// sensitivity with.
pub const SENSITIVE_KEY: &str = "sensitive";

/// The three pillars the sensitivity axis covers, in the order the
/// compiler reads them.
pub const SENSITIVE_PILLARS: &[&str] = &["values", "infra", "provides"];

/// Which leaves an entity's schema declares sensitive, one set per
/// pillar, keyed by the flat-dotted property path from the pillar root.
/// A path is unique only within its pillar, so the three sets are kept
/// apart.
///
/// `unreadable_escapes` names paths where the walk stopped at an escaped
/// JSON Schema body authored as YAML text, which this crate cannot open.
/// A non-empty entry means "unknown here", not "nothing here". Its paths
/// are pillar-qualified (`values.auth`, or just `values` when the pillar
/// root itself is the escape) because they span all three pillars.
#[derive(Debug, Default, Clone, PartialEq, Eq)]
pub struct DeclaredSensitiveLeaves {
    pub values: BTreeSet<String>,
    pub infra: BTreeSet<String>,
    pub provides: BTreeSet<String>,
    pub unreadable_escapes: BTreeSet<String>,
}

impl DeclaredSensitiveLeaves {
    /// `any` reports whether the entity declares at least one sensitive
    /// leaf in any pillar. It is the one home for "does this entity
    /// declare sensitivity at all", so no caller has to decide which
    /// pillars that question spans.
    pub fn any(&self) -> bool {
        !self.values.is_empty() || !self.infra.is_empty() || !self.provides.is_empty()
    }

    /// `pillar` returns one pillar's set by its authored name, or `None`
    /// for a name outside the axis.
    pub fn pillar(&self, name: &str) -> Option<&BTreeSet<String>> {
        match name {
            "values" => Some(&self.values),
            "infra" => Some(&self.infra),
            "provides" => Some(&self.provides),
            _ => None,
        }
    }

    fn pillar_mut(&mut self, name: &str) -> Option<&mut BTreeSet<String>> {
        match name {
            "values" => Some(&mut self.values),
            "infra" => Some(&mut self.infra),
            "provides" => Some(&mut self.provides),
            _ => None,
        }
    }
}

/// `declared_sensitive_leaves` reads the sensitivity declarations out of
/// an entity's authored `spec.schema` content.
///
/// The input is the schema value as authored — the same value the entity
/// carries on the wire. Only a schema is read: no value is inspected,
/// and a property name is used as a path segment rather than matched
/// against anything, so nothing here infers sensitivity from what a
/// datum is called. A path is sensitive because an author said so.
///
/// A pillar the schema does not declare, or one whose own node reads as
/// a leaf rather than an object scope, contributes nothing.
pub fn declared_sensitive_leaves(schema: &Value) -> DeclaredSensitiveLeaves {
    let mut out = DeclaredSensitiveLeaves::default();
    let Some(root) = schema.as_object() else {
        return out;
    };
    for pillar in SENSITIVE_PILLARS {
        let Some(node) = root.get(*pillar) else {
            continue;
        };
        // The pillar's own node is an object scope, never a leaf that
        // declares: the compiler reads a pillar's declarations out of the
        // properties it exposes, and reads no marker on the pillar itself.
        let mut collected = BTreeSet::new();
        let mut blocked = BTreeSet::new();
        walk_scope(node, "", &mut collected, &mut blocked);
        if let Some(dest) = out.pillar_mut(pillar) {
            dest.extend(collected);
        }
        out.unreadable_escapes.extend(blocked.into_iter().map(|p| {
            if p.is_empty() {
                (*pillar).to_string()
            } else {
                format!("{pillar}.{p}")
            }
        }));
    }
    out
}

/// `slot_declares_sensitive` reports whether one authored attachment
/// slot declares sensitivity. Only the boolean true declares: the
/// compiler decodes the key into a boolean field, so any other value is
/// not a declaration there either.
pub fn slot_declares_sensitive(slot: &Value) -> bool {
    slot.get(SENSITIVE_KEY) == Some(&Value::Bool(true))
}

/// How the compiler classifies one authored mapping. The variants are
/// only those that differ in where a sensitivity marker can sit and
/// which children the walk may descend into.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
enum Shape {
    /// An escaped JSON Schema body replaces this node.
    Escape,
    /// A reference; a marker beside it lands on the referring node, and
    /// the target is not walked.
    Reference,
    /// A composition; a marker beside it lands on the composing node,
    /// and the branches are not walked.
    Composition,
    /// A constraint leaf, which may still carry a property map.
    CompactConstraint,
    /// Authored JSON Schema, where `sensitive` is ordinary content
    /// rather than the compact-form marker.
    Passthrough,
    /// An object scope whose every key is a property name.
    ImplicitObject,
}

fn shape_of(obj: &Map<String, Value>) -> Shape {
    // An explicit mode overrides the content-shape heuristic and is read
    // first, exactly as the compiler reads it. A mode value that is not
    // one of the two spellings halts compilation, so an entity carrying
    // one never reaches a reader; the heuristic below is applied so the
    // classification stays defined rather than arbitrary.
    if let Some(Value::String(mode)) = obj.get("$mode") {
        match mode.as_str() {
            "compact" => return Shape::CompactConstraint,
            "jsonschema" => return Shape::Passthrough,
            _ => {}
        }
    }
    if obj.contains_key("$jsonschema") {
        return Shape::Escape;
    }
    if obj.contains_key("$ref") {
        return Shape::Reference;
    }
    if obj.contains_key("allOf") || obj.contains_key("anyOf") || obj.contains_key("oneOf") {
        return Shape::Composition;
    }
    // The two structural compact keywords nest other content rather than
    // acting as a leaf constraint, so neither alone makes a leaf.
    if obj
        .keys()
        .any(|k| k != "$jsonschema" && k != "$mode" && COMPACT_KEYWORDS.contains(&k.as_str()))
    {
        return Shape::CompactConstraint;
    }
    if obj
        .keys()
        .any(|k| SCHEMA_CONTAINER_KEYWORDS.contains(&k.as_str()))
    {
        return Shape::Passthrough;
    }
    Shape::ImplicitObject
}

fn declares_marker(obj: &Map<String, Value>, shape: Shape) -> bool {
    match shape {
        // These three carry the marker as a compact-form declaration.
        Shape::CompactConstraint | Shape::Reference | Shape::Composition => {
            obj.get(SENSITIVE_KEY) == Some(&Value::Bool(true))
        }
        // Under an authored-JSON-Schema reading the key is content, not
        // the marker; an object scope cannot hold the key at all without
        // being classified as a leaf.
        Shape::Passthrough | Shape::Escape | Shape::ImplicitObject => false,
    }
}

/// Walks one object scope, recording the dotted path of every node
/// declaring the marker and recursing into the property maps the
/// compiler's own walk follows. `prefix` is empty at a pillar root,
/// where no marker is read.
fn walk_scope(
    node: &Value,
    prefix: &str,
    out: &mut BTreeSet<String>,
    blocked: &mut BTreeSet<String>,
) {
    let Some(obj) = node.as_object() else {
        return;
    };
    let shape = shape_of(obj);

    // An escaped body replaces the node it sits on. A body authored as a
    // mapping is read on; a body authored as YAML text cannot be opened
    // here, and that is reported rather than read as an absence.
    let (obj, shape) = match shape {
        Shape::Escape => match obj.get("$jsonschema") {
            Some(Value::Object(body)) => (body, Shape::Passthrough),
            Some(Value::String(_)) => {
                blocked.insert(prefix.to_string());
                return;
            }
            _ => return,
        },
        other => (obj, other),
    };

    if !prefix.is_empty() && declares_marker(obj, shape) {
        out.insert(prefix.to_string());
    }

    for (name, child) in child_properties(obj, shape) {
        let path = if prefix.is_empty() {
            name.clone()
        } else {
            format!("{prefix}.{name}")
        };
        walk_scope(child, &path, out, blocked);
    }
}

/// The property entries the walk descends into: the ones that become the
/// node's compiled property map. A reference's target, a composition's
/// branches, and an array's element type are not property maps and are
/// not walked.
fn child_properties(obj: &Map<String, Value>, shape: Shape) -> Vec<(&String, &Value)> {
    match shape {
        Shape::CompactConstraint | Shape::Passthrough => obj
            .get("properties")
            .and_then(Value::as_object)
            .map(|props| props.iter().collect())
            .unwrap_or_default(),
        // Every key of an object scope is a property name, except the two
        // definition maps, which the compiler lifts to the schema root.
        Shape::ImplicitObject => obj
            .iter()
            .filter(|(k, _)| k.as_str() != "$defs" && k.as_str() != "definitions")
            .collect(),
        Shape::Reference | Shape::Composition | Shape::Escape => Vec::new(),
    }
}
