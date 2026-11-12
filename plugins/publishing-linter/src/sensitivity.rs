// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

//! Reads an authored entity's schema the way the compiler compiles it.
//!
//! This module owns the classification of an authored mapping — whether it is a
//! constraint leaf, an object scope, authored JSON Schema, a reference, a
//! composition or an escaped body — and the keyword vocabularies that
//! classification turns on. Two readers are built on it: the sensitivity
//! declarations below, and [`pillar_property_names`], which answers which
//! properties one pillar exposes. Both re-derive something the compiler
//! computes and neither can call it, so both are held by the same shared
//! expectation; the reason and the mechanism are in the section below.
//!
//! Two declarations carry the sensitivity axis, and this module reads both from
//! the authored entity JSON a rule receives:
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
//! ## What the walk follows, and what it deliberately does not
//!
//! A declaration reaches a datum by four routes, and all four are
//! followed: a property map, a composing branch, a reference target, and
//! a marker sitting beside a reference or a composition keyword. A branch
//! and a reference target both compose onto the property carrying them,
//! so a declaration found through either is collected at that property's
//! own path rather than at a path inside the branch or the definition.
//! A property map written beside a reference is the referring node's own,
//! exactly as it would be on the same node without the reference.
//!
//! An escaped JSON Schema body is read the way the compiler reads it:
//! nothing inside it is classified by its shape. Every mapping in a schema
//! position there is a keyword node — a compact-form keyword such as
//! `sensitive` keeps its meaning, and a `properties:` map is a property map
//! wherever it is written — so the walk carries whether it is inside a body
//! down to every node it reaches, and a reference target is read in the
//! context of the scope it was found in.
//!
//! A pillar's own node is entered at the property maps it declares: its own
//! property map, and — when the pillar node carries a reference — the
//! property map of the definition that reference resolves to, followed in
//! turn when that definition is a reference too. A pillar declared wholly
//! through a reference is therefore read through its target, and one that
//! writes a property map beside the reference is read through both. No
//! marker is read on the pillar node itself, since no single datum sits
//! there.
//!
//! Two shapes are deliberately not followed, and both are the same line
//! the compiler's reader draws. An array's element type is not a property
//! of the enclosing object, so a marker there has no path any consumer
//! could look up. A composition written on the pillar node, or on a
//! definition a pillar reference reaches, is not entered on either side.
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
    "const",
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

/// The composition keywords whose value is an ARRAY of schemas. Mirrors
/// `branchListKeywords` in `pkg/resolver/sensitive_leaves.go`, and pinned
/// against it by `tests/sensitivity_parity_test.rs`: a keyword the
/// compiler descends and this reader does not leaves a declaration
/// uncollected on this side only.
pub const BRANCH_LIST_KEYWORDS: &[&str] = &["allOf", "anyOf", "oneOf"];

/// The composition keywords whose value is a SINGLE schema. Mirrors
/// `branchNodeKeywords` in `pkg/resolver/sensitive_leaves.go`, pinned by
/// the same test and for the same reason.
pub const BRANCH_NODE_KEYWORDS: &[&str] = &["if", "then", "else", "not"];

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
        // The document root and the pillar's own node are both reference
        // scopes: a definitions block authored at either place stays where it
        // was authored, so a pointer written beneath it resolves only when the
        // node it sits on is in scope.
        let root_scope = [Scope {
            node: schema,
            in_escape: false,
        }];
        walk_scope(
            node,
            "",
            false,
            &root_scope,
            &[],
            &mut collected,
            &mut blocked,
        );
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

/// `pillar_property_names` reads the property names one pillar of an
/// authored `spec.schema` exposes.
///
/// It answers the same question the compiler answers by compiling that
/// pillar and reading the property map the compilation produces. The
/// compiler is the source of truth for it, and this reader cannot call
/// the compiler, so the two are held by the shared expectation described
/// at the top of this module — the same handshake the sensitivity
/// readers are held by.
///
/// Only the pillar's OWN node is classified, and only its first level of
/// properties is read. A declaration nested below a property does not add
/// a name at this level, and neither reader descends for one.
///
/// A pillar node carrying a reference is read through it: the names are
/// those of the property map written beside the reference together with
/// those of the definition the reference resolves to, followed in turn
/// when that definition is a reference too. The pointer is resolved
/// against the pillar node alone, because the compiler compiles a pillar
/// as its own document, so a definitions block authored on the pillar node
/// is the only one it can name.
///
/// The two return shapes are different answers and must not be collapsed:
///
///   - `Some(set)` — the names the pillar exposes, and the set is empty
///     when the schema declares no such pillar or the pillar exposes no
///     property. An empty set is a real answer: it says the entity
///     declares no contract on that axis.
///   - `None` — the pillar's own node, or a definition its reference
///     reaches, is a shape this reader cannot enumerate: a reference that
///     resolves nowhere inside the pillar node, whose target is a contract
///     this reader cannot see; a composition, whose branches carry
///     properties — on the pillar node, or inside an escaped body where it
///     is one keyword beside the body's own property map; or an escaped
///     body authored as YAML text, which needs a parser this crate does not
///     carry. `None` means unknown, never empty, so a caller that must not
///     be wrong stays silent rather than reading silence as an absent
///     contract.
pub fn pillar_property_names(schema: &Value, pillar: &str) -> Option<BTreeSet<String>> {
    let Some(node) = schema.as_object().and_then(|root| root.get(pillar)) else {
        return Some(BTreeSet::new());
    };
    if !node.is_object() {
        return Some(BTreeSet::new());
    }
    // An escaped body replaces the node it sits on, exactly as it does in the
    // sensitivity walk: a body authored as a mapping is read on, and a body
    // authored as YAML text cannot be opened here.
    let Opened::Node {
        obj,
        value,
        in_escape,
    } = open_escapes(node, false)
    else {
        return None;
    };
    let mut names = BTreeSet::new();
    // The pillar node is the whole document a pointer on it resolves against.
    let mut scopes = vec![Scope {
        node: value,
        in_escape,
    }];
    let mut refs: Vec<String> = Vec::new();
    let (mut obj, mut in_escape) = (obj, in_escape);
    loop {
        let shape = shape_of(obj, in_escape);
        // An explicit mode settles the reading on its own, so only an unmoded
        // node is withheld for composing; outside a body such a node already
        // classifies as a composition.
        let composes =
            BRANCH_LIST_KEYWORDS.iter().any(|k| obj.contains_key(*k)) && !obj.contains_key("$mode");
        if shape == Shape::Composition || composes {
            return None;
        }
        names.extend(
            child_properties(obj, shape)
                .into_iter()
                .map(|(name, _)| name.clone()),
        );
        let Some(Value::String(reference)) = obj.get("$ref") else {
            return Some(names);
        };
        // A pointer already followed ends the chain, as it does on the
        // compiler's side: the names reached so far are the whole answer.
        if in_flight(&refs, reference) {
            return Some(names);
        }
        let target = resolve_pointer(reference, &scopes)?;
        refs.push(reference.clone());
        let Opened::Node {
            obj: next,
            value,
            in_escape: next_in_escape,
        } = open_escapes(target.node, target.in_escape)
        else {
            return None;
        };
        scopes.push(Scope {
            node: value,
            in_escape: next_in_escape,
        });
        obj = next;
        in_escape = next_in_escape;
    }
}

/// How the compiler classifies one authored mapping. The variants are
/// only those that differ in where a sensitivity marker can sit and
/// which children the walk may descend into.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
enum Shape {
    /// An escaped JSON Schema body replaces this node.
    Escape,
    /// A reference. Every other key of the node is read as it would be
    /// without the reference: a marker beside it lands on the referring
    /// node, and a property map beside it is the node's own.
    Reference,
    /// A composition; a marker beside it lands on the composing node.
    Composition,
    /// A keyword node: a constraint leaf, which may still carry a property
    /// map, and every mapping inside an escaped body.
    CompactConstraint,
    /// Authored JSON Schema, where `sensitive` is ordinary content
    /// rather than the compact-form marker.
    Passthrough,
    /// An object scope whose every key is a property name.
    ImplicitObject,
}

/// `shape_of` classifies one authored mapping as the compiler's dispatch
/// does. `in_escape` says whether the mapping sits inside an escaped body,
/// where the compiler runs no shape heuristic: past an explicit mode, a
/// nested escape and a reference, such a mapping is a keyword node.
fn shape_of(obj: &Map<String, Value>, in_escape: bool) -> Shape {
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
    if in_escape {
        return Shape::CompactConstraint;
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

/// What a node reads as once every escaped body on it has been opened.
enum Opened<'a> {
    /// The mapping read in the node's place, the value it is (pushed as a
    /// reference scope), and whether it sits inside an escaped body.
    Node {
        obj: &'a Map<String, Value>,
        value: &'a Value,
        in_escape: bool,
    },
    /// An escaped body authored as YAML text, which this crate cannot open.
    Text,
    /// Neither a mapping nor an escape this reader can read.
    Nothing,
}

/// `open_escapes` replaces a node by the escaped body it carries, as the
/// compiler does, repeatedly for a body that is itself an escape. A body
/// authored as a mapping is read on, inside an escape from then on; a body
/// authored as YAML text cannot be opened here.
fn open_escapes(node: &Value, in_escape: bool) -> Opened<'_> {
    let mut value = node;
    let mut in_escape = in_escape;
    loop {
        let Some(obj) = value.as_object() else {
            return Opened::Nothing;
        };
        if shape_of(obj, in_escape) != Shape::Escape {
            return Opened::Node {
                obj,
                value,
                in_escape,
            };
        }
        match obj.get("$jsonschema") {
            Some(body @ Value::Object(_)) => {
                value = body;
                in_escape = true;
            }
            Some(Value::String(_)) => return Opened::Text,
            _ => return Opened::Nothing,
        }
    }
}

/// One enclosing node a pointer may resolve against, and whether that node
/// sits inside an escaped body — which is how a target found under it is
/// read, since the compiler compiled the target where it was authored.
#[derive(Clone, Copy)]
struct Scope<'a> {
    node: &'a Value,
    in_escape: bool,
}

/// `push_scope` returns a new scope stack with `node` appended, copying
/// rather than extending in place. The copy is load-bearing: sibling
/// branches of one node each extend the same stack, and an in-place
/// append would let one branch's entry land in the slot another branch is
/// about to write.
fn push_scope<'a>(scopes: &[Scope<'a>], node: &'a Value, in_escape: bool) -> Vec<Scope<'a>> {
    let mut out = scopes.to_vec();
    out.push(Scope { node, in_escape });
    out
}

/// Walks one object scope, recording the dotted path of every node whose
/// declaration reaches the datum at `prefix`, by every route the
/// compiler's own walk follows: nested properties, each composition
/// branch, and the target of a reference. A properties descent extends
/// the path; a branch or reference descent keeps it, because both compose
/// onto the object carrying them.
///
/// `prefix` is empty at a pillar root, where no marker is read and no
/// composition is entered. A reference on the pillar node is followed, with
/// the target read as a pillar root in turn: the compiler enters a pillar at
/// the property maps it declares, and a pillar reference's target declares
/// them as much as the pillar node does. A pointer that resolves nowhere, or
/// revisits one already followed, collects nothing there — no single datum
/// sits at a pillar root to stand in for the target, as the referring
/// property does below one.
///
/// `in_escape` is carried to every node the walk reaches below an escaped
/// body, through properties and branches alike; a reference target takes
/// the flag of the scope that resolved it instead.
///
/// `refs` is the stack of pointers in flight. A pointer that revisits a
/// target already in flight stops rather than recursing forever.
fn walk_scope<'a>(
    node: &'a Value,
    prefix: &str,
    in_escape: bool,
    scopes: &[Scope<'a>],
    refs: &[String],
    out: &mut BTreeSet<String>,
    blocked: &mut BTreeSet<String>,
) {
    // An escaped body replaces the node it sits on. A body authored as a
    // mapping is read on; a body authored as YAML text cannot be opened
    // here, and that is reported rather than read as an absence.
    let (obj, scope_node, in_escape) = match open_escapes(node, in_escape) {
        Opened::Node {
            obj,
            value,
            in_escape,
        } => (obj, value, in_escape),
        Opened::Text => {
            blocked.insert(prefix.to_string());
            return;
        }
        Opened::Nothing => return,
    };
    let shape = shape_of(obj, in_escape);

    if !prefix.is_empty() && declares_marker(obj, shape) {
        out.insert(prefix.to_string());
    }

    // The node joins the scope stack before any descent so a definitions
    // block it carries is visible to the pointers written beneath it.
    let inner = push_scope(scopes, scope_node, in_escape);

    for (name, child) in child_properties(obj, shape) {
        let path = if prefix.is_empty() {
            name.clone()
        } else {
            format!("{prefix}.{name}")
        };
        walk_scope(child, &path, in_escape, &inner, refs, out, blocked);
    }

    if prefix.is_empty() {
        let Some(Value::String(reference)) = obj.get("$ref") else {
            return;
        };
        if in_flight(refs, reference) {
            return;
        }
        let Some(target) = resolve_pointer(reference, &inner) else {
            return;
        };
        let mut next = refs.to_vec();
        next.push(reference.clone());
        walk_scope(
            target.node,
            prefix,
            target.in_escape,
            &inner,
            &next,
            out,
            blocked,
        );
        return;
    }

    for keyword in BRANCH_LIST_KEYWORDS {
        if let Some(branches) = obj.get(*keyword).and_then(Value::as_array) {
            for branch in branches {
                walk_scope(branch, prefix, in_escape, &inner, refs, out, blocked);
            }
        }
    }
    for keyword in BRANCH_NODE_KEYWORDS {
        if let Some(branch) = obj.get(*keyword) {
            walk_scope(branch, prefix, in_escape, &inner, refs, out, blocked);
        }
    }

    let Some(Value::String(reference)) = obj.get("$ref") else {
        return;
    };
    // The in-flight key is the pointer as written. Two scopes resolving the
    // same spelling to different targets therefore read as one revisit,
    // which stops a descent that could have continued — the same direction
    // as an unresolvable pointer, so it withholds rather than leaks.
    if in_flight(refs, reference) {
        if ref_target_declares_sensitive(reference, &inner, &[]) {
            out.insert(prefix.to_string());
        }
        return;
    }
    let Some(target) = resolve_pointer(reference, &inner) else {
        // What the target declares is unknown, so naming the referring
        // property is the most precise declaration available and the only
        // one that cannot under-report.
        out.insert(prefix.to_string());
        return;
    };
    let mut next = refs.to_vec();
    next.push(reference.clone());
    walk_scope(
        target.node,
        prefix,
        target.in_escape,
        &inner,
        &next,
        out,
        blocked,
    );
}

/// `in_flight` reports whether a pointer is already being expanded.
/// Mirrors the shared cycle guard the compiler threads through the same
/// walk: a key already in flight is a cycle.
fn in_flight(refs: &[String], reference: &str) -> bool {
    refs.iter().any(|r| r == reference)
}

/// Splits a JSON Pointer reference into its unescaped segments, or `None`
/// for a reference this reader cannot follow. A reference naming another
/// document is never followable here: a cross-document reference is merged
/// into the consumer's own schema before either reader sees it, so one that
/// survived is a reference neither can resolve.
fn pointer_segments(reference: &str) -> Option<Vec<String>> {
    let pointer = reference.strip_prefix('#')?;
    if pointer.is_empty() {
        return Some(Vec::new());
    }
    let body = pointer.strip_prefix('/')?;
    Some(
        body.split('/')
            // Unescape in the order the JSON Pointer grammar requires:
            // "~1" first, so an escaped tilde cannot be re-read as a
            // separator.
            .map(|segment| segment.replace("~1", "/").replace("~0", "~"))
            .collect(),
    )
}

/// Resolves an in-document reference against a stack of enclosing scopes,
/// nearest first, and returns `None` when no scope holds the pointed-to
/// node. The node found carries the escape flag of the scope that held it.
///
/// Nearest-first matters because a definitions block is authored on the
/// node whose properties use it rather than hoisted to the document root,
/// so the innermost scope holding the pointer's own first segment is the
/// one an author wrote it against.
fn resolve_pointer<'a>(reference: &str, scopes: &[Scope<'a>]) -> Option<Scope<'a>> {
    let segments = pointer_segments(reference)?;
    for scope in scopes.iter().rev() {
        if let Some(node) = walk_pointer(scope.node, &segments) {
            return Some(Scope {
                node,
                in_escape: scope.in_escape,
            });
        }
    }
    None
}

fn walk_pointer<'a>(scope: &'a Value, segments: &[String]) -> Option<&'a Value> {
    let mut node = scope;
    for segment in segments {
        node = node.as_object()?.get(segment)?;
        if !node.is_object() {
            return None;
        }
    }
    Some(node)
}

/// `ref_target_declares_sensitive` answers one narrower question than the
/// path walk above: does anything reachable from this reference declare
/// sensitivity at all, regardless of where. It exists because a reference
/// that revisits a target already in flight cannot be enumerated further,
/// and the two answers available there are opposite failures — stopping
/// silently leaves a recursive tail readable, while withholding
/// unconditionally invents sensitivity on a recursive schema whose author
/// declared none. This predicate separates the two cases, so the tail is
/// withheld exactly when the recursion carries a declaration.
///
/// It is deliberately a second, simpler walk rather than a reuse of the
/// path walk: it collects no paths and never withholds on a revisit, which
/// is sound for a question about mere reachability — a marker anywhere in
/// the reachable graph sits on some node reached by a non-repeating chain.
/// Folding the two together would make the path walk's revisit rule depend
/// on its own result.
///
/// It opens an escaped body authored as a mapping, as the path walk does.
/// A body authored as YAML text cannot be opened, so a declaration
/// reachable only through one inside a recursive target is invisible to it
/// on this side.
fn ref_target_declares_sensitive(reference: &str, scopes: &[Scope<'_>], refs: &[String]) -> bool {
    if in_flight(refs, reference) {
        return false;
    }
    let Some(target) = resolve_pointer(reference, scopes) else {
        return true;
    };
    let mut next = refs.to_vec();
    next.push(reference.to_string());
    node_declares_sensitive(target.node, target.in_escape, scopes, &next)
}

/// `node_declares_sensitive` reports whether `node`, or anything reachable
/// from it, declares sensitivity. The node joins the scope stack itself —
/// as the escaped body it carries, where it carries one — so a pointer
/// written beneath it resolves against it.
fn node_declares_sensitive(
    node: &Value,
    in_escape: bool,
    scopes: &[Scope<'_>],
    refs: &[String],
) -> bool {
    let Opened::Node {
        obj,
        value,
        in_escape,
    } = open_escapes(node, in_escape)
    else {
        return false;
    };
    let shape = shape_of(obj, in_escape);
    if declares_marker(obj, shape) {
        return true;
    }
    let inner = push_scope(scopes, value, in_escape);
    for (_, child) in child_properties(obj, shape) {
        if node_declares_sensitive(child, in_escape, &inner, refs) {
            return true;
        }
    }
    for keyword in BRANCH_LIST_KEYWORDS {
        if let Some(branches) = obj.get(*keyword).and_then(Value::as_array) {
            for branch in branches {
                if node_declares_sensitive(branch, in_escape, &inner, refs) {
                    return true;
                }
            }
        }
    }
    for keyword in BRANCH_NODE_KEYWORDS {
        if let Some(branch) = obj.get(*keyword) {
            if node_declares_sensitive(branch, in_escape, &inner, refs) {
                return true;
            }
        }
    }
    if let Some(Value::String(reference)) = obj.get("$ref") {
        return ref_target_declares_sensitive(reference, &inner, refs);
    }
    false
}

/// The property entries the walk descends into: the ones that become the
/// node's compiled property map, each contributing its own name to the
/// path. A reference's target and a composition's branches are reached
/// separately, at the carrying property's path rather than below it, and
/// an array's element type is reached by neither reader.
fn child_properties(obj: &Map<String, Value>, shape: Shape) -> Vec<(&String, &Value)> {
    match shape {
        // A reference node is a keyword node like a constraint leaf, so a
        // property map written beside the reference is its own.
        Shape::CompactConstraint | Shape::Passthrough | Shape::Reference => obj
            .get("properties")
            .and_then(Value::as_object)
            .map(|props| props.iter().collect())
            .unwrap_or_default(),
        // Every key of an object scope is a property name, except the two
        // definition maps: those name reference targets rather than data,
        // so they are reference scopes and never path segments.
        Shape::ImplicitObject => obj
            .iter()
            .filter(|(k, _)| k.as_str() != "$defs" && k.as_str() != "definitions")
            .collect(),
        // A property map beside a composition keyword is not one the
        // compiler emits as the node's own, outside an escaped body; inside
        // one, a composing node is a keyword node and never reaches here.
        Shape::Composition | Shape::Escape => Vec::new(),
    }
}
