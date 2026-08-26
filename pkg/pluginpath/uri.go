// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package pluginpath

import (
	"strings"

	"github.com/aggrega-ai/aggrega-cli/pkg/namesegment"
)

// isLocalPathRef reports whether uri is an explicit filesystem-path reference —
// a POSIX relative prefix (./ or ../) or an absolute path. This is the Tier-1
// discriminator: an explicit local path is loaded from disk verbatim, distinct
// from a bare catalog/registry URI that resolves through the cache/registry
// tiers. It mirrors the resolver's local-path-intent convention: the ./ or ../
// prefix disambiguates local-file intent from a type-name lookup, so no
// string-content sniffing of the body is required.
func isLocalPathRef(uri string) bool {
	return strings.HasPrefix(uri, "./") ||
		strings.HasPrefix(uri, "../") ||
		strings.HasPrefix(uri, "/")
}

// guestFileEncoding is the segment encoding a plugin's on-disk guest filename is
// written in.
//
// A plugin's reference is composed of a socket, a name, and — where the
// reference pins one — a version, and a filename has to carry all of them in one
// flat string. Dropping a part is lossy: two plugins sharing a bare name in
// different sockets, or two versions of one plugin, would reach one filename,
// and a filename two plugins reach identifies neither. Encoding the parts rather
// than joining them raw is what keeps two distinct part lists at two distinct
// filenames, because a bare name is unconstrained and may itself contain
// whatever character a raw join would separate on.
//
// The alphabet is stated here rather than taken from the encoding, because it is
// a property of what a filename has to be rather than of the encoding.
var guestFileEncoding = namesegment.NewEncoding(isGuestFileRune)

// isGuestFileRune reports whether a character is carried into a guest filename
// as itself: a lowercase ASCII letter or a digit. Every other character is
// escaped, which is what two of the alphabet's edges are for.
//
// An uppercase letter is escaped rather than admitted. A plugin's identity
// coordinate stays cased, so admitting uppercase would give a derivation
// injective as a string and not injective as a filename: a filesystem that
// compares names case-insensitively would fold two distinct coordinates onto one
// file. Escaping the case keeps them apart wherever the file lands.
//
// The full stop is escaped for the same class of reason. It would otherwise let
// an encoded segment be a relative-path reference, and let a derived name begin
// with the character that hides a file. Escaping it leaves the suffix the sole
// full stop in the filename.
func isGuestFileRune(r rune) bool {
	return r >= 'a' && r <= 'z' || r >= '0' && r <= '9'
}

// guestFileSuffix is the extension every derived guest filename ends in. It is
// appended unconditionally, which is also what keeps a derived name from ever
// being a relative-path reference: a name ending in these five characters is
// neither "." nor "..", so joining one to a directory can only land inside it.
const guestFileSuffix = ".wasm"

// PluginLeaf maps a plugin reference to the flat on-disk filename its .wasm
// guest is written under, beside the Plugin entity that declares it.
//
// The filename carries every part of the reference that tells two plugins apart:
// the socket, the name, and the version where the reference pins one. So a bare
// name shared across two sockets reaches two filenames, and two pinned versions
// of one plugin reach two filenames. Each part is encoded into lowercase letters
// and digits and the parts are joined by a sequence no encoded part can contain,
// so two references reach one filename only where they carry the same parts —
// which matters because a plugin's bare name is unconstrained, so it may itself
// carry the characters a raw join would separate on.
//
// Two spellings are read as one reference before the parts are taken, and are
// the only way two references reach one filename: a leading "oci://" scheme, and
// a trailing ".wasm" the caller has already written. Both name the same guest as
// the reference without them.
//
// It is the single home of the mapping for everything that resolves or stages a
// guest through this module: the resolver computes a co-located artifact's path
// with it, and a caller staging a .wasm beside its Plugin entity keys the file
// by it. A caller staging one must derive the filename from the same reference
// the resolver resolves by, since a reference that pins a version and the bare
// reference beneath it are — correctly — two different filenames.
//
// Every derivation here is deterministic: identical input yields byte-identical
// output.
func PluginLeaf(uri string) string {
	return pluginLeaf(uri)
}

func pluginLeaf(uri string) string {
	return guestFileEncoding.Name(pluginLeafParts(uri)...) + guestFileSuffix
}

// pluginLeafParts splits a plugin reference into the parts its guest filename is
// built from: the reference's path segments, followed by the pinned version when
// the reference carries one.
//
// The version is a part in its own right rather than text appended to the last
// segment, so a reference that pins a version and one that does not are told
// apart by the shape of the split and not by what the version happens to spell —
// which is what keeps a reference whose pin is empty distinct from one carrying
// no pin at all.
//
// A version is separated at the LAST colon, so where a reference carries more
// than one it is the trailing pin that is read as the version and everything
// before it that stays in the path.
func pluginLeafParts(uri string) []string {
	ref := strings.TrimSuffix(strings.TrimPrefix(uri, "oci://"), guestFileSuffix)
	version, pinned := "", false
	if i := strings.LastIndex(ref, ":"); i >= 0 {
		ref, version, pinned = ref[:i], ref[i+1:], true
	}
	parts := strings.Split(ref, "/")
	if pinned {
		parts = append(parts, version)
	}
	return parts
}
