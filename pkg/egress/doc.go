// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package egress is the single gate every network-egressing path crosses. It is
// the sole home of the offline decision and the sole constructor of the network
// clients and transports the rest of the compiler uses to reach a registry, a
// remote source, or an identity provider.
//
// The offline decision is DERIVED, never trusted from the caller. A caller does
// not hand the gate a boolean; it hands the gate the resolved workspace
// configuration, and the gate reads the offline field from it. The only
// legitimate source of a resolved configuration is the workspace-resolution
// choke-point, so a caller cannot fabricate an offline-false configuration to
// bypass the promise without first re-rolling workspace resolution — which a
// separate structural guard forbids. The two guards compose: resolution is
// single-sourced, and egress reads offline only from resolution's output. No
// package outside this one reads the offline field to make a network decision;
// callers ask the gate for the means to egress, and the refusal it returns in
// place of a client is the derived decision.
//
// When the resolved configuration is offline, the constructors refuse at
// construction, before any dial, returning ErrOfflineEgress. Offline forbids the
// fetch, not the math: cryptographic verification of already-cached or pinned
// material against a pinned key or root performs no egress and does not pass
// through this gate, and a build runs against the lockfile and content cache it
// already has — pinned inputs are still served from the store, and only an
// acquisition or a missing-blob refill is refused.
//
// A structural guard-test asserts that no package outside this one constructs a
// network client or reaches for a network library directly, so a future consumer
// that forgets the gate cannot silently egress — deny-by-default over the whole
// module. The narrow exception is the OCI-semantics package, which owns the
// oras-specific repository and registry construction and receives its transport
// from this gate.
//
// # One refusal, caller-chosen loudness
//
// The gate returns exactly one refusal and leaves the caller to decide how loud
// it is.
//
// A primary fetch verb — pull, acquire, push, sign, a registry update, a
// build-time cache fill, or a verification the user explicitly asked for —
// propagates the refusal and fails loudly. A best-effort adornment — the install
// hint offered after an unrecognised command, or the repository deprecation
// probe — reads it as "this adornment is unavailable" and skips silently.
//
// The direction of that split is the point: loudly offline, never silently
// online. A new adornment that forgets to special-case an offline workspace
// still cannot reach the network. The worst it can do is surface the refusal
// loudly where a skip would have been nicer, which is a cosmetic failure rather
// than a bypass.
//
// A verification the user explicitly requested refuses rather than skips, and
// that covers the closure-signature verify performed during a pull, the
// fetched-plugin verify, and the keyless branch that resolves a live trust root.
// The one verification that still succeeds offline is the one anchored on a
// trust root pinned on disk, which reads local material and never fetches.
// Skipping verification while offline is a separate and explicit choice the
// workspace makes by setting skip_signature_verification; it is not something
// being offline decides on the user's behalf.
//
// Offline also dominates the acquisition switch. External-input acquisition has
// its own control, the --no-acquire-inputs flag, which is orthogonal to being
// offline. Because the acquiring service takes its HTTP client from this gate,
// an offline workspace hands it no client at all, so acquisition and any
// missing-blob refill are mechanically impossible whatever that flag says. The
// flag stays the online "freeze the closure" knob, and nothing consults the
// offline decision a second time.
//
// # Transports this gate does not construct
//
// Three libraries build their own HTTP client internally — go-git for clones,
// the sigstore keyless signing flow, and the Helm getter — so watching for
// client construction cannot see their egress at all. Each is threaded with this
// gate's transport at its own home package through that library's public option
// API, and each home is pinned by an import-level guard, so a new package cannot
// import one of those libraries and egress around the gate.
//
// Injection is a structural guarantee only where the library routes all of its
// network through the injected client. Where a path builds its own transport,
// the guarantee is instead an explicit refusal taken before the library is
// invoked. What each path actually has:
//
//   - Clone over https or http: injection of this gate's client into the
//     library's HTTP transport, plus the pre-call refusal.
//   - Clone over ssh, git or file: the pre-call refusal only. The library builds
//     those transports itself and exposes no client seam to inject into, so
//     refusing on a nil client before the clone begins is the whole guarantee.
//   - Chart and repository-index fetches: injection of this gate's transport
//     into the getter, which routes every such fetch, plus the pre-call refusal.
//   - Keyless signing's certificate request and its timestamp request:
//     injection, through each option's own transport field.
//   - Keyless signing's transparency-log submission: the pre-call refusal only.
//     That option exposes no transport field, so the signing operation as a
//     whole refuses while offline before the log is reached; online, the
//     submission uses the signing client's own transport.
//   - The live trust-root fetch: the pre-call refusal, routed through the
//     sibling package that is the single home of that fetch and therefore the
//     single place the refusal has to be taken.
//
// The offline decision reaches all of them exactly as it reaches everything
// else, derived from the resolved configuration. Source resolution threads this
// gate's client onto the source specification, and a nil client is the offline
// signal the clone and chart adapters refuse on; the keyless signer carries the
// resolved configuration and asks this gate for a transport when it signs. The
// effect at the surface is that an offline keyless sign refuses early and by
// name instead of failing deep inside a certificate request.
package egress
