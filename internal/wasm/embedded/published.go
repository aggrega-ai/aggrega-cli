// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package embedded

// PublishedPlugin is what the binary carries of a core plugin it does not embed:
// enough to select the plugin and to name the exact module it accepts, and none
// of the module's bytes. Such a plugin is built, stamped and published with each
// compiler release like every core plugin; the binary holds its generated Plugin
// entity and the content digest of the guest that release publishes, so a caller
// can choose it by what the entity advertises and hold any module it later reads
// to the digest.
//
// It is declared here, outside every build-specific file, so the default, light
// and guest builds share one shape; only the default build carries any.
type PublishedPlugin struct {
	// URI is the plugin's bare identity, <socket>/<name> — the identity a Plugin
	// entity derives from its pluginType and name, and the reference the plugin
	// cascade resolves its module by.
	URI string

	// Entity is the plugin's generated Plugin entity, the same document the
	// release publishes beside the guest.
	Entity []byte

	// Digest is the content digest of the decompressed guest, in the spelling
	// every content-addressing consumer compares (sha256:<lowercase hex>): the
	// digest a module read for this plugin must match.
	Digest string
}
