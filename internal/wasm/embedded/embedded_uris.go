// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//go:build !wasip1

package embedded

// PublishingLinterURI is the digest-free repository reference the embedded
// publishing-linter plugin's decompressed bytes register under — the built-in
// linter a lint invocation resolves by default when no linter plugin is
// configured. It is exported so the lint command can single-source the
// default-linter reference from the embedded package rather than restate the
// string. It lives here — shared by both the default and light host builds —
// so a light binary (which embeds no bytes) still compiles against the same
// identity while resolving the linter through the later cascade tiers.
const PublishingLinterURI = "oci://hub.aggrega.ai/plugins/core/publishing-linter"
