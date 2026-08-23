// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//! The part of a governance plugin that is the same whichever policy language it
//! evaluates.
//!
//! A governance plugin is an adapter around a policy engine, and only a small
//! part of it is actually about the policy language. The rest — reading the
//! payload a role delivers, projecting rendered output into documents a policy
//! can navigate, turning a policy's own decision keys into the severities the
//! socket reports from, and turning a decided label or annotation into the change
//! the build applies — is the same work regardless of which language expressed the
//! decisions. That shared part lives here, once.
//!
//! Nothing in this crate names or depends on a policy engine. A guest brings its
//! engine and its own source-intake rules; it takes everything else from here.
//!
//! # Why this is one crate rather than one copy per plugin
//!
//! Two of these mechanics are contracts rather than conveniences, and a second
//! copy of either would drift somewhere it cannot be seen:
//!
//! - The **decision-key translation** is what an operator configures through the
//!   attachment's settings, typed by the schema this crate builds. Two copies
//!   would be two settings schemas, and an attachment accepted by one plugin and
//!   refused by the other with the same settings is a difference no reader could
//!   attribute.
//! - The **absent-list accommodation** in [`payload`] is documented there as a
//!   compensation to be removed once every sender writes an empty list. A
//!   compensation with two copies is removed from one of them, and the one left
//!   behind is the one nobody was looking at.
//! - The **decision keys a transform role reads** and the **change instruction a
//!   decided label becomes** are what an operator writes and what the build
//!   applies. Two copies would be two spellings of the same authored key and two
//!   opinions about how a label key containing the path separator is addressed —
//!   and the second is a correctness hazard rather than a naming one, because an
//!   unescaped key addresses something other than the label it names.
//!
//! # Determinism
//!
//! Nothing here iterates a hash map. Translation walks an ordered rule list;
//! artifacts keep the path-sorted order the host supplied and the document order
//! within each file; the source projection keeps the order it was given; and one
//! decided label becomes one change instruction with no reordering. So identical
//! input yields an identical sequence of everything this crate produces.
//!
//! Ordering **between** decisions is deliberately not decided here. What order a
//! plugin's decisions arrive in depends on what its own language can guarantee,
//! so each plugin establishes that itself and documents what it guarantees; a rule
//! imposed here would make one language's compensation another's behaviour.

pub mod annotations;
pub mod artifacts;
pub mod labels;
pub mod payload;
pub mod sources;
pub mod translation;
