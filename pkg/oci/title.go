// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package oci

import (
	"strings"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/aggrega-ai/aggrega-cli/pkg/deprecation"
)

// Every content descriptor this package packs carries the standard
// org.opencontainers.image.title annotation: the file name a generic registry
// client writes the descriptor's bytes under when it pulls the manifest into a
// directory. A client that names files this way skips a descriptor that has no
// title and still reports success, so an untitled descriptor is content a
// consumer using standard tooling never receives. This product's own reads
// never consult the title — they select a
// descriptor by position or by media type and trust it by digest — so the title
// serves every other consumer and changes nothing for this one.
//
// The title names what the descriptor carries, and its extension is the media
// type's structured-syntax suffix, with each "+" read as a ".":
//
//   - everything an entity publication carries is called entity — an entity
//     published as its document alone is entity.yaml, one published with the
//     files it declares is entity.tar.gz, and a Plugin publishes entity.yaml
//     (the entity, as the config descriptor) beside entity.wasm (its
//     executable, as the first layer), followed when the publication carries
//     third-party notices by notices.markdown (the notices, as the second layer);
//   - a signature bundle attached as a referrer is signature.json;
//   - a deprecation marker attached as a referrer is deprecation.json;
//   - a publication date attached as a referrer is published-at.json;
//   - a software bill of materials attached as a referrer is sbom.json.
//
// A standalone data artifact carries no entity, so its title is not derived
// here: its publisher supplies the name (see PushNamed).
//
// The title is part of the manifest, so it is part of the manifest digest. It is
// derived from the media type alone, never from a local file name, which keeps a
// manifest digest a pure function of what is published: the same bytes packed
// from differently named files still produce the same manifest, and the
// publish-time immutability guard still recognises a re-publication of them.
const (
	titleStemEntity      = "entity"
	titleStemNotices     = "notices"
	titleStemSignature   = "signature"
	titleStemDeprecation = "deprecation"
	titleStemPublishedAt = "published-at"
	titleStemSBOM        = "sbom"
)

// contentTitle returns the title a content descriptor of mediaType is stamped
// with, or "" for a media type this package derives no title for.
func contentTitle(mediaType string) string {
	stem := titleStem(mediaType)
	if stem == "" {
		return ""
	}
	return stem + titleExtension(mediaType)
}

// titleStem returns the name of what a descriptor of mediaType carries. The
// entity kinds are read from the mediatype table rather than listed here, in
// both of the shapes a kind publishes in, so a kind added to that table is
// titled without a second list to update and an entity is called entity
// whichever shape its content gave it.
func titleStem(mediaType string) string {
	switch mediaType {
	case MediaTypeWasm:
		return titleStemEntity
	case MediaTypePluginNotices:
		return titleStemNotices
	case MediaTypeSignatureBundle:
		return titleStemSignature
	case deprecation.ArtifactType:
		return titleStemDeprecation
	case MediaTypePublishedAt:
		return titleStemPublishedAt
	case MediaTypeSPDXJSON:
		return titleStemSBOM
	}
	if _, _, ok := KindOfMediaType(mediaType); ok {
		return titleStemEntity
	}
	return ""
}

// titleExtension returns the file extension a media type's structured-syntax
// suffix names: "+yaml" is ".yaml", "+tar+gz" is ".tar.gz". A media type with
// no suffix names no extension.
func titleExtension(mediaType string) string {
	i := strings.IndexByte(mediaType, '+')
	if i < 0 {
		return ""
	}
	return "." + strings.ReplaceAll(mediaType[i+1:], "+", ".")
}

// withTitle returns desc carrying title as its org.opencontainers.image.title
// annotation, or desc unchanged when title is empty. The descriptor oras
// returns for a pushed blob carries no annotations, so the title is the only
// one a packed content descriptor carries.
func withTitle(desc ocispec.Descriptor, title string) ocispec.Descriptor {
	if title == "" {
		return desc
	}
	desc.Annotations = map[string]string{ocispec.AnnotationTitle: title}
	return desc
}
