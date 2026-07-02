// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package abtest holds the host-side A/B proof for the helm-provider Wasm
// guest: it renders a chart both through the in-tree Go helm engine and
// through the Wasm guest across the provider render ABI, and asserts the
// two produce byte-identical output. The proof body is guarded by the
// integration build tag.
package abtest
