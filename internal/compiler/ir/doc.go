// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package ir implements Step 3 of the Aggrega compiler pipeline:
// Graph Finalization & Feature Normalization.
//
// It consumes a topology.TopologyResult and resolver.ResolveResult to produce
// a fully populated types.IRGraph. Core algorithm: 5-Tier Feature Normalization
// (Onion Model) determining the deterministic merge order of features from
// sub-aggregates, root aggregate, subsystems, and Release boundaries.
package ir
