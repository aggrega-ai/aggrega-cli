// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package compilerops

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/internal/opprogress"
	"github.com/aggrega-ai/aggrega-cli/pkg/egress"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/ociref"
	"github.com/aggrega-ai/aggrega-cli/pkg/registryhost"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// registryReferenceScheme is what a reference to a published artifact begins
// with, as opposed to an entity identity or a filesystem path.
const registryReferenceScheme = "oci://"

// IsRegistryReference reports whether s names an artifact by the registry
// coordinate it is published at, rather than an entity by its identity or a
// document by its path.
//
// It is the one shape test every surface that reads a published entity routes
// by, so a command line, the daemon and the tool protocol send the same inputs
// to a registry and the same inputs to the catalog. No entity identity begins
// with the scheme, which is what makes the test unambiguous.
func IsRegistryReference(s string) bool {
	return strings.HasPrefix(s, registryReferenceScheme)
}

// PublishedEntity is an entity read straight from the coordinate it is published
// at: the entity document, and the identity that document declares for itself.
//
// Document is the entity exactly as it was published — unpacked from the
// archive the coordinate carries where its kind publishes as one — and nothing
// else: no inheritance is applied and nothing is resolved against a catalog. It
// is what the publisher put there, which is the question a reader asking about a
// coordinate is asking.
//
// Kind, Name, Version and Description are read off that document. There is no
// publish scope here, and the omission is deliberate rather than a gap: a caller
// holding this value already holds the coordinate, which is where the entity is
// published, so re-deriving where it publishes to from its content would state
// the same fact a second way.
//
// Archive reports whether the coordinate's bytes were an archive the document
// was unpacked from. MediaType is the artifact type the manifest declares, and
// Digest is that manifest's digest — the value to name when reading the same
// bytes again by digest.
type PublishedEntity struct {
	Reference   string
	Kind        string
	Name        string
	Version     string
	Description string
	Document    []byte
	Archive     bool
	MediaType   string
	Digest      string
}

// ReadPublishedEntity reads the entity published at a registry reference.
//
// It is the one reading every surface that shows a published entity runs, and it
// is a reader rather than an acquisition: it writes no cache entry and records no
// pin, so the only thing it leaves behind is the answer it returns. The network
// is reached through the workspace's egress gate, so an offline workspace is
// refused before anything is dialled, and the registry is addressed through the
// same repository construction a dependency pull uses — the same credential
// store, the same plain-HTTP rule for a registry on this machine.
//
// # What the answer is checked against
//
// The bytes are checked against the digest the artifact's own manifest records
// for them, which establishes that they are the bytes that manifest names. Nothing
// is checked against a record kept beforehand, no signature attached to the
// artifact is verified, and no withdrawal recorded against it is consulted — the
// reading reports what a coordinate carries, and says nothing about whether it
// should be trusted.
//
// # Which failures are named, and carry what to do about it
//
// A reference nothing is published under, a credential the registry refuses, an
// archive that does not unpack, an archive holding no entity or more than one, and
// a document that is not an entity each return an error wrapping the condition
// that names it, followed by a remediation; a registry that cannot be reached
// returns the network error as the client reported it. None returns an empty entity:
// an answer with nothing in it would read to a caller exactly like an entity with
// nothing to say about itself.
func ReadPublishedEntity(ctx context.Context, cfg workspaceconfig.Config, reference string) (PublishedEntity, error) {
	repository, target, err := splitRegistryReference(reference)
	if err != nil {
		return PublishedEntity{}, err
	}
	httpClient, err := egress.HTTPClient(cfg)
	if err != nil {
		return PublishedEntity{}, fmt.Errorf("read %s: %w", reference, err)
	}
	repo, err := oci.NewRemoteRepository(repository, httpClient)
	if err != nil {
		return PublishedEntity{}, fmt.Errorf("read %s: %w", reference, err)
	}
	end := opprogress.Begin(ctx, "reading "+repository+":"+target)
	published, err := oci.PullPublishedEntity(ctx, repo, target)
	end(err)
	if err != nil {
		return PublishedEntity{}, fmt.Errorf("read %s: %w%s", reference, err, fetchRemediation(err, repositoryHost(repository)))
	}
	document, err := oci.EntityYAML(published.Bytes)
	if err != nil {
		return PublishedEntity{}, fmt.Errorf("read %s: %w%s", reference, err, unpackRemediation(err))
	}
	header, err := publishedHeader(document)
	if err != nil {
		return PublishedEntity{}, fmt.Errorf("read %s: %w; remediation: check that the reference names the "+
			"coordinate an entity is published at rather than another artifact", reference, err)
	}
	return PublishedEntity{
		Reference:   reference,
		Kind:        header.Kind,
		Name:        header.Metadata.Name,
		Version:     header.Metadata.Version,
		Description: header.Metadata.Description,
		Document:    document,
		Archive:     oci.IsTarGzBundle(published.Bytes),
		MediaType:   published.MediaType,
		Digest:      published.Digest,
	}, nil
}

// CheckRegistryReference reports why reference is not a registry reference
// ReadPublishedEntity can read, or nil when it is one.
//
// It is the same check the reading performs first, asked on its own for a
// surface that answers a malformed request differently from a well-formed one
// the registry could not satisfy: the first is corrected by whoever composed the
// request, the second is a state of the registry. One check behind both is what
// keeps them from disagreeing about which references are well formed.
func CheckRegistryReference(reference string) error {
	_, _, err := splitRegistryReference(reference)
	return err
}

// RegistryReferenceHost reports the registry host a registry reference
// addresses — the host ReadPublishedEntity contacts for it, and names in a
// remediation — or why reference is not a registry reference it can read.
//
// It is derived exactly as the reading derives it, from the repository the same
// split produces, so a surface deciding whether to let a read go ahead decides on
// the host the read would contact rather than on a second parse of the reference
// that could come to disagree with it. The result is a host authority: no scheme,
// no path and no userinfo, with the port kept.
func RegistryReferenceHost(reference string) (string, error) {
	repository, _, err := splitRegistryReference(reference)
	if err != nil {
		return "", err
	}
	return repositoryHost(repository), nil
}

// repositoryHost is the host a repository split from a registry reference
// addresses. It is the one derivation both the reading and RegistryReferenceHost
// use, and it agrees with the host the registry client is constructed for on
// every repository that client accepts: that client takes the text before the
// first slash as the registry and refuses one carrying userinfo, which is what
// registryhost.Of returns for such a repository.
func repositoryHost(repository string) string {
	return registryhost.Of(repository)
}

// splitRegistryReference splits `oci://<host>/<repository>:<tag>` or
// `oci://<host>/<repository>@<digest>` into the repository the registry client is
// constructed for and the tag or digest resolved within it.
//
// The version is split off with the shared OCI-safe rule, so a port colon in the
// host is never mistaken for one. A reference naming neither a tag nor a digest is
// refused rather than read at some version the command would have to choose: the
// reader reports what one coordinate carries, and a coordinate names a version.
func splitRegistryReference(reference string) (repository, target string, err error) {
	bare := strings.TrimPrefix(reference, registryReferenceScheme)
	slash := strings.LastIndex(bare, "/")
	if slash > 0 {
		tail := bare[slash+1:]
		if at := strings.IndexByte(tail, '@'); at >= 0 {
			repository, target = bare[:slash+1+at], tail[at+1:]
		} else if base, version, ok := ociref.SplitVersionTag(bare); ok {
			repository, target = base, version
		}
	}
	if slash <= 0 || repository == "" || target == "" ||
		strings.HasPrefix(repository, "/") || strings.HasSuffix(repository, "/") {
		return "", "", fmt.Errorf("%q is not a registry reference this can read: write it as "+
			"oci://<host>/<repository>:<tag>, or oci://<host>/<repository>@<digest> to read one manifest exactly",
			reference)
	}
	return repository, target, nil
}

// publishedEntityHeader is the part of an entity document this reading reports
// beside the document itself.
type publishedEntityHeader struct {
	Kind     string `yaml:"kind"`
	Metadata struct {
		Name        string `yaml:"name"`
		Version     string `yaml:"version"`
		Description string `yaml:"description"`
	} `yaml:"metadata"`
}

// publishedHeader reads the identity an entity document declares, and refuses a
// document that does not declare a kind a registry publishes.
//
// The publishable set is read through oci.IsPublishableKind, which is where that
// set lives, so a kind added to it is readable here with nothing to update — and a
// Release or a Manifest, which an operator authors in the workspace and never
// publishes, is refused as the non-entity it is at a coordinate.
func publishedHeader(document []byte) (publishedEntityHeader, error) {
	var header publishedEntityHeader
	if err := yaml.Unmarshal(document, &header); err != nil {
		return publishedEntityHeader{}, fmt.Errorf("the published bytes do not parse as a document (%v): %w",
			err, oci.ErrPublishedArtifactNotAnEntity())
	}
	if header.Kind == "" {
		return publishedEntityHeader{}, fmt.Errorf("the published document declares no kind: %w",
			oci.ErrPublishedArtifactNotAnEntity())
	}
	if !oci.IsPublishableKind(header.Kind) {
		return publishedEntityHeader{}, fmt.Errorf("the published document declares kind %s, which a registry "+
			"does not publish: %w", header.Kind, oci.ErrPublishedArtifactNotAnEntity())
	}
	return header, nil
}

// fetchRemediation is what a reader does about a fetch that failed, in the terms
// of reading a reference rather than of resolving a Release's dependencies —
// which is why this does not reuse the remediation a pull prints, whose advice is
// about a lockfile this reading has none of.
func fetchRemediation(err error, host string) string {
	switch {
	case errors.Is(err, oci.ErrFetchNotFound()):
		return "; remediation: check the registry host, the repository path and the tag — the registry holds " +
			"nothing under this reference"
	case errors.Is(err, oci.ErrFetchUnauthorized()):
		return "; remediation: authenticate to " + host + " with `aggrega login " + host + "`, then run it again"
	case errors.Is(err, oci.ErrFetchIntegrity()):
		return "; remediation: the registry served bytes its own manifest does not name, which nothing on this " +
			"machine can repair — report it to whoever runs " + host
	default:
		return ""
	}
}

// unpackRemediation is what a reader does about published bytes that could not
// be opened as an entity. Each of the three is a fact about the artifact rather
// than about the reader, so each names who can change it.
func unpackRemediation(err error) string {
	switch {
	case errors.Is(err, oci.ErrBundleUnreadable()):
		return "; remediation: the published artifact is damaged, and only its publisher can replace it, by " +
			"publishing a new version"
	case errors.Is(err, oci.ErrBundleNoEntityDocument()):
		return "; remediation: check that the reference names the coordinate an entity is published at — the " +
			"artifact here carries no entity document at its root"
	case errors.Is(err, oci.ErrBundleAmbiguousEntityDocument()):
		return "; remediation: the artifact was published from a directory holding more than one entity, and " +
			"its publisher republishes each of them from a directory of its own"
	default:
		return ""
	}
}
