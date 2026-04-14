// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package provider defines the Intent 002 rendering boundary: pluggable engines that turn a
// target-agnostic types.IRGraph plus a types.Release scope into physical artifact bytes (Helm,
// Compose, etc.). Selection is by scope provider name via GetEngine.
package provider
