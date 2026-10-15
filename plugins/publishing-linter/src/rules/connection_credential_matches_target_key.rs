// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//! Advise where the two ends of a key-checked connection name different
//! secrets, or where only one end names one.
//!
//! A server that checks a key holds the key it verifies in a slot of its own
//! Component, and a caller presents a key on the connection it presents it
//! on. Neither end can see the other's attachments: an attachment on a
//! connection lands on the caller alone, and a server renders the same
//! whatever its callers carry. So the two agree only by authoring — usually
//! one parameter both attachments read — and nothing in a build compares
//! them. This rule does, over a whole Release.
//!
//! Both ends are found by the names the standard catalog gives them. A
//! target's caller key is the slot `api_key` its Component declares, and a
//! caller's key is the slot `credential` on the connection. The names are a
//! catalog convention no schema keyword marks, so a slot named differently is
//! not read, and a target declaring no `api_key` slot draws nothing. A slot
//! that is not the caller key — a model server's registry pull token, say —
//! is never read, whatever secret it holds.
//!
//! A target whose Component is labelled `component.aggrega.ai/hosting:
//! managed` is not judged either, whatever its `api_key` slot holds. A managed
//! Component stands for a service its vendor runs, so nothing the Release runs
//! checks a key on it: where it declares `api_key`, the slot holds the key the
//! Release presents to that vendor, under the name the vendor's product gives
//! it, and never a key the Component requires of its callers. A target
//! labelled any other way, or carrying no hosting label, is judged.
//!
//! For each connection whose target declares the caller key and is not
//! managed, the rule reports one of three things:
//!
//!   - the connection carries a credential and the target's key slot is
//!     unfilled, so the target checks no key;
//!   - the target's key slot is filled and the connection carries no
//!     credential, so the caller presents no key on it;
//!   - both are filled and the locators the two ends write differ, so they
//!     may name different secrets.
//!
//! A connection on the socket `scrape_target` is a scrape: the standard
//! catalog gives that name to the socket a metrics scraper reaches the
//! components it scrapes through. A scrape presents whatever credential the
//! target's metrics take, and whether that is the key the target requires of
//! its callers is a property of the target's product rather than of the
//! connection. So for a scrape the rule reads what the target's Component
//! states in its annotation `observability.aggrega.ai/metrics-credential`:
//!
//!   - `none` — its metrics are served with no credential, so a scrape
//!     connection to it is not judged at all;
//!   - `separate-token` — its metrics take a token of their own, so a scrape
//!     connection's credential is not compared with the target's `api_key`,
//!     and none of the three cases above is reported on it;
//!   - `target-key` — its metrics take the key the target requires of every
//!     caller, so a scrape connection to it is judged like any other.
//!
//! The rule reads only those three values: a target writing no such
//! annotation, or writing any other value, has its scrape connections judged
//! like any other. A connection on any other socket — a model, a tool, a
//! datastore — is judged the same whatever the target's annotation says. The
//! socket name and the annotation are catalog conventions no schema keyword
//! marks, like the two slot names, so a scraping socket named differently is
//! judged like any other socket.
//!
//! What is compared is each end's locator as its author wrote it — the
//! `origin` an attachment carries, in its `values` where the Aggregate
//! attaches it and in its `infra` where the Release does. Two ends reading one
//! parameter write the same reference and compare equal; two literals compare
//! by their text. Nothing is resolved, and no finding quotes either locator or
//! any part of one: the text an author writes there can be the secret itself.
//!
//! The rule is a standard-catalog convention, not a constraint. Agreement
//! between two authored locators is not something a build checks, so the rule
//! emits a non-halting `warn`, and a workspace whose two ends deliberately
//! differ may opt out.
//!
//! Scope and limits, each deliberate:
//!
//!   - It reads the attachments written on the target itself and on the
//!     connection, in the Aggregate and in the Release. Where an attachment
//!     named for the target's key slot reaches the target from a subsystem or
//!     a boundary rather than being written on it, the target's end is not
//!     read and the connection draws nothing: the build folds such an
//!     attachment into the target leaf by leaf, and a second reading of that
//!     fold here could disagree with the build's. An attachment an annotation
//!     adds is not in the documents read at all, so a key slot filled only
//!     that way reads as unfilled.
//!   - An end whose attachment carries no locator, or carries one in both
//!     pillars, has no locator to compare, so the third case stays silent for
//!     it; the first two read only whether a slot is filled.
//!   - It reads the components the Aggregate itself declares. A component an
//!     imported module contributes is not among them, so a connection reaching
//!     one draws nothing.
//!   - A component authored with `active: false` takes its connections with
//!     it. An `active` written as a template is not evaluated here and is read
//!     as present.

use aggrega_pdk_rust::{Diagnostic, LinterInput};
use serde_json::{Map, Value};

use crate::metrics_credential::{self, MetricsCredential};
use crate::raw;

/// The rule's identifier, as the host dispatches it.
pub const RULE_ID: &str = "connection-credential-matches-target-key";

/// The slot a target's Component declares for the key it requires of every
/// caller, by the standard catalog's convention.
pub const TARGET_KEY_SLOT: &str = "api_key";

/// The slot a connection fills with the key the caller presents on it, by the
/// standard catalog's convention.
pub const CONNECTION_KEY_SLOT: &str = "credential";

/// The leaf a credential attachment names its secret by: the secret's name,
/// the store entry to fetch, or the value itself, depending on the
/// realization bound to it.
pub const LOCATOR_LEAF: &str = "origin";

/// The Component label recording who operates a component's underlying
/// resource, by the standard catalog's convention.
pub const HOSTING_LABEL: &str = "component.aggrega.ai/hosting";

/// The hosting value of a Component standing for a service its vendor runs. A
/// target carrying it is not judged: its `api_key` is the key the Release
/// presents to the vendor, not one the target requires of its callers.
pub const MANAGED: &str = "managed";

/// The socket a metrics scraper reaches the components it scrapes through, by
/// the standard catalog's convention. A connection on it is a scrape, and is
/// judged by what its target states about how its metrics authenticate.
pub const SCRAPE_SOCKET: &str = "scrape_target";

pub fn classify(input: &LinterInput) -> Vec<Diagnostic> {
    if input.entity_kind != "Aggregate" {
        return vec![];
    }
    if input.policies.allow_connection_credential_mismatches {
        return vec![];
    }
    // The rule reads a whole Release. Without one, a credential the Release
    // attaches is invisible, and reading the Aggregate alone would report it
    // missing.
    let Some(release) = raw::parse_opt(&input.release) else {
        return vec![];
    };
    if input.components.is_empty() {
        return vec![];
    }
    let aggregate = raw::parse(&input.proposed);
    let instances = aggregate_instances(&aggregate);
    let placements = release_placements(&release);

    let mut findings = Vec::new();
    for conn in connections(&instances, &placements) {
        let Some(target) = instances.iter().find(|i| i.name == conn.target) else {
            continue;
        };
        if !target.active {
            continue;
        }
        let Some(component) = target_component(input, &target.name) else {
            continue;
        };
        if !checks_caller_key(&component) || !compared_with_target_key(&conn, &component) {
            continue;
        }
        let target_end = target_end(target, &placements);
        let connection_end = connection_end(&conn);
        if let Some(case) = judge(&connection_end, &target_end) {
            findings.push(finding(case, &conn));
        }
    }
    findings
}

/// One component instance the Aggregate declares: its declaration, and
/// whether an attachment named for the target's key slot reaches it from a
/// subsystem it sits in.
struct Instance<'a> {
    name: String,
    decl: &'a Value,
    active: bool,
    key_from_broader_scope: bool,
}

/// Where the Release places one component instance: its entry, and whether an
/// attachment named for the target's key slot reaches it from a boundary it
/// sits in.
struct Placement<'a> {
    name: String,
    entry: &'a Value,
    key_from_broader_scope: bool,
}

/// The Aggregate's component instances, in document order, from its top-level
/// components and every nested subsystem.
fn aggregate_instances(aggregate: &Value) -> Vec<Instance<'_>> {
    let mut out = Vec::new();
    let Some(spec) = aggregate.get("spec") else {
        return out;
    };
    collect_instances(spec, false, &mut out);
    out
}

fn collect_instances<'a>(scope: &'a Value, inherited_key: bool, out: &mut Vec<Instance<'a>>) {
    if let Some(components) = scope.get("components").and_then(Value::as_object) {
        for (name, decl) in components {
            out.push(Instance {
                name: name.clone(),
                decl,
                active: decl.get("active") != Some(&Value::Bool(false)),
                key_from_broader_scope: inherited_key,
            });
        }
    }
    if let Some(subsystems) = scope.get("subsystems").and_then(Value::as_object) {
        for sub in subsystems.values() {
            let key_here = inherited_key || has_attachment(sub.get("features"), TARGET_KEY_SLOT);
            collect_instances(sub, key_here, out);
        }
    }
}

/// Every place the Release puts a component, from its flat components and
/// every nested boundary. A component a module contributes is routed under a
/// boundary's aggregates and is not read.
fn release_placements(release: &Value) -> Vec<Placement<'_>> {
    let mut out = Vec::new();
    let Some(spec) = release.get("spec") else {
        return out;
    };
    if let Some(components) = spec.get("components").and_then(Value::as_object) {
        push_placements(components, false, &mut out);
    }
    if let Some(boundaries) = spec.get("boundaries").and_then(Value::as_object) {
        for boundary in boundaries.values() {
            collect_placements(boundary, false, &mut out);
        }
    }
    out
}

fn collect_placements<'a>(boundary: &'a Value, inherited_key: bool, out: &mut Vec<Placement<'a>>) {
    let key_here = inherited_key || has_attachment(boundary.get("features"), TARGET_KEY_SLOT);
    if let Some(components) = boundary.get("components").and_then(Value::as_object) {
        push_placements(components, key_here, out);
    }
    if let Some(nested) = boundary.get("boundaries").and_then(Value::as_object) {
        for inner in nested.values() {
            collect_placements(inner, key_here, out);
        }
    }
}

fn push_placements<'a>(components: &'a Map<String, Value>, key_here: bool, out: &mut Vec<Placement<'a>>) {
    for (name, entry) in components {
        out.push(Placement {
            name: name.clone(),
            entry,
            key_from_broader_scope: key_here,
        });
    }
}

/// One connection a component draws, with the credential attachment each
/// document writes on it.
struct Connection<'a> {
    consumer: String,
    socket: String,
    /// The alias the connection is written under; none for a target listed in
    /// a socket's short form, which spells no alias.
    alias: Option<String>,
    target: String,
    aggregate_credential: Option<&'a Value>,
    release_credentials: Vec<&'a Value>,
}

/// Every connection the Aggregate's active components draw, followed by every
/// connection only the Release wires, each carrying the credential attachments
/// both documents write on it.
fn connections<'a>(instances: &[Instance<'a>], placements: &[Placement<'a>]) -> Vec<Connection<'a>> {
    let mut out: Vec<Connection<'a>> = Vec::new();
    for inst in instances.iter().filter(|i| i.active) {
        let Some(uses) = inst.decl.get("uses").and_then(Value::as_object) else {
            continue;
        };
        for (socket, entry) in uses {
            match entry {
                Value::Array(targets) => {
                    for target in targets.iter().filter_map(Value::as_str) {
                        out.push(Connection {
                            consumer: inst.name.clone(),
                            socket: socket.clone(),
                            alias: None,
                            target: target.to_string(),
                            aggregate_credential: None,
                            release_credentials: Vec::new(),
                        });
                    }
                }
                Value::Object(aliases) => {
                    for (alias, conn) in aliases {
                        out.push(Connection {
                            consumer: inst.name.clone(),
                            socket: socket.clone(),
                            alias: Some(alias.clone()),
                            target: conn
                                .get("target")
                                .and_then(Value::as_str)
                                .unwrap_or_default()
                                .to_string(),
                            aggregate_credential: attachment(conn.get("features"), CONNECTION_KEY_SLOT),
                            release_credentials: Vec::new(),
                        });
                    }
                }
                _ => {}
            }
        }
    }

    for placement in placements {
        let Some(consumer) = instances.iter().find(|i| i.name == placement.name) else {
            continue;
        };
        if !consumer.active {
            continue;
        }
        let Some(uses) = placement.entry.get("uses").and_then(Value::as_object) else {
            continue;
        };
        for (socket, aliases) in uses {
            let Some(aliases) = aliases.as_object() else {
                continue;
            };
            for (alias, conn) in aliases {
                let credential = attachment(conn.get("features"), CONNECTION_KEY_SLOT);
                let existing = out.iter_mut().find(|c| {
                    c.consumer == placement.name && c.socket == *socket && c.alias.as_deref() == Some(alias.as_str())
                });
                match existing {
                    Some(c) => {
                        if c.target.is_empty() {
                            if let Some(t) = conn.get("target").and_then(Value::as_str) {
                                c.target = t.to_string();
                            }
                        }
                        c.release_credentials.extend(credential);
                    }
                    None => out.push(Connection {
                        consumer: placement.name.clone(),
                        socket: socket.clone(),
                        alias: Some(alias.clone()),
                        target: conn
                            .get("target")
                            .and_then(Value::as_str)
                            .unwrap_or_default()
                            .to_string(),
                        aggregate_credential: None,
                        release_credentials: credential.into_iter().collect(),
                    }),
                }
            }
        }
    }
    out
}

/// The Component the named instance resolves to. An instance whose Component
/// the host could not resolve is absent from the input, so nothing is read
/// about it and its connections are not judged.
fn target_component(input: &LinterInput, instance: &str) -> Option<Value> {
    input.components.get(instance).map(|doc| raw::parse(doc))
}

/// Whether a target's Component makes it one the rule judges: it declares the
/// caller key slot among its own slots, and it is not labelled managed.
fn checks_caller_key(component: &Value) -> bool {
    declares_caller_key(component) && !is_managed(component)
}

/// Whether a connection's credential is compared with its target's caller key.
/// Every connection's is, except a scrape's where the target's Component states
/// that its metrics take no credential or a token of their own. The socket is
/// read first, so a connection on any other socket is compared whatever the
/// target states.
fn compared_with_target_key(conn: &Connection<'_>, target: &Value) -> bool {
    if conn.socket != SCRAPE_SOCKET {
        return true;
    }
    !matches!(
        metrics_credential::declared(target),
        Some(MetricsCredential::NoCredential | MetricsCredential::SeparateToken)
    )
}

fn declares_caller_key(component: &Value) -> bool {
    component
        .get("spec")
        .and_then(|spec| spec.get("featuresSchema"))
        .and_then(|slots| slots.get(TARGET_KEY_SLOT))
        .is_some_and(Value::is_object)
}

fn is_managed(component: &Value) -> bool {
    component
        .get("metadata")
        .and_then(|metadata| metadata.get("labels"))
        .and_then(|labels| labels.get(HOSTING_LABEL))
        .and_then(Value::as_str)
        == Some(MANAGED)
}

/// What one end of a connection holds.
enum End<'a> {
    /// The slot is filled nowhere this rule reads.
    Unfilled,
    /// The slot is filled; the locator is the one the end writes, or none
    /// where it writes none or writes one in both pillars.
    Filled(Option<&'a Value>),
    /// An attachment reaches the slot from a broader scope, so the end is not
    /// read.
    Unknown,
}

fn target_end<'a>(target: &Instance<'a>, placements: &[Placement<'a>]) -> End<'a> {
    let mine: Vec<&Placement<'a>> = placements.iter().filter(|p| p.name == target.name).collect();
    if target.key_from_broader_scope || mine.iter().any(|p| p.key_from_broader_scope) {
        return End::Unknown;
    }
    let from_aggregate = attachment(target.decl.get("features"), TARGET_KEY_SLOT);
    let from_release: Vec<&Value> = mine
        .iter()
        .filter_map(|p| attachment(p.entry.get("features"), TARGET_KEY_SLOT))
        .collect();
    end_of(from_aggregate, &from_release)
}

fn connection_end<'a>(conn: &Connection<'a>) -> End<'a> {
    end_of(conn.aggregate_credential, &conn.release_credentials)
}

/// An end's state from the attachment the Aggregate writes for it and the
/// attachments the Release writes for it. The Aggregate's locator is in
/// `values`, the Release's in `infra`; exactly one of the two is how an
/// attachment names its secret, so an end yielding any other number of
/// locators has none to compare.
fn end_of<'a>(from_aggregate: Option<&'a Value>, from_release: &[&'a Value]) -> End<'a> {
    if from_aggregate.is_none() && from_release.is_empty() {
        return End::Unfilled;
    }
    let mut locators: Vec<&'a Value> = Vec::new();
    if let Some(att) = from_aggregate {
        locators.extend(locator(att, "values"));
    }
    for att in from_release {
        locators.extend(locator(att, "infra"));
    }
    End::Filled(match locators.as_slice() {
        [only] => Some(*only),
        _ => None,
    })
}

fn locator<'a>(attachment: &'a Value, pillar: &str) -> Option<&'a Value> {
    attachment
        .get(pillar)
        .and_then(|p| p.get(LOCATOR_LEAF))
        .filter(|v| !v.is_null())
}

/// The attachment a features block writes under `slot`, when it writes one.
fn attachment<'a>(features: Option<&'a Value>, slot: &str) -> Option<&'a Value> {
    features
        .and_then(|f| f.get(slot))
        .filter(|a| a.is_object())
}

fn has_attachment(features: Option<&Value>, slot: &str) -> bool {
    attachment(features, slot).is_some()
}

/// The three things a connection can be reported for.
enum Case {
    TargetChecksNoKey,
    ConnectionPresentsNoKey,
    LocatorsDiffer,
}

fn judge(connection: &End<'_>, target: &End<'_>) -> Option<Case> {
    match (connection, target) {
        (End::Unknown, _) | (_, End::Unknown) => None,
        (End::Filled(_), End::Unfilled) => Some(Case::TargetChecksNoKey),
        (End::Unfilled, End::Filled(_)) => Some(Case::ConnectionPresentsNoKey),
        (End::Filled(Some(a)), End::Filled(Some(b))) if a != b => Some(Case::LocatorsDiffer),
        _ => None,
    }
}

/// Builds the finding. It names the consumer, the connection, the target and
/// the two slots, and nothing an attachment holds: the locator an author writes
/// can be the secret itself, so neither end's is quoted, in part or in whole.
fn finding(case: Case, conn: &Connection<'_>) -> Diagnostic {
    let consumer = &conn.consumer;
    let socket = &conn.socket;
    let target = &conn.target;
    let connection = match &conn.alias {
        Some(alias) => format!("connection {alias:?} on socket {socket:?} of component {consumer:?}"),
        None => format!(
            "the connection to {target:?} listed on socket {socket:?} of component {consumer:?}"
        ),
    };
    let message = match case {
        Case::TargetChecksNoKey => format!(
            "{connection} carries a credential in its {CONNECTION_KEY_SLOT} slot, but its target \
             {target:?} leaves its {TARGET_KEY_SLOT} slot — the key it requires of every caller — \
             unfilled, so the target checks no key and verifies nothing this connection presents. \
             Filling the target's {TARGET_KEY_SLOT} slot from the parameter the connection's \
             {CONNECTION_KEY_SLOT} reads keeps the two ends one key; that agreement is the \
             standard-catalog convention, and the build does not check it."
        ),
        Case::ConnectionPresentsNoKey => format!(
            "{connection} carries nothing in its {CONNECTION_KEY_SLOT} slot, but its target \
             {target:?} holds a key in its {TARGET_KEY_SLOT} slot, which every caller must present, so \
             this caller presents no key on this connection. Attaching a {CONNECTION_KEY_SLOT} that \
             reads the parameter the target's {TARGET_KEY_SLOT} reads keeps the two ends one key; that \
             agreement is the standard-catalog convention, and the build does not check it."
        ),
        Case::LocatorsDiffer => format!(
            "{connection} carries a credential in its {CONNECTION_KEY_SLOT} slot and its target \
             {target:?} holds a key in its {TARGET_KEY_SLOT} slot, but the two ends write their \
             secret's locator differently, so they may name different secrets and the target would \
             refuse the key this caller presents. The locators are compared as written, and neither \
             is shown here. Reading one parameter at both ends keeps them one key; that agreement is \
             the standard-catalog convention, and the build does not check it."
        ),
    };
    Diagnostic {
        rule_id: RULE_ID.to_string(),
        severity: "warn".to_string(),
        message,
        ..Default::default()
    }
}
