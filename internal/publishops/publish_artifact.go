// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package publishops

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"

	"oras.land/oras-go/v2"

	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// dataArtifactNamespace is the reserved top-level registry segment for
// standalone data artifacts that are not catalog entities. It sits alongside
// the entity-kind segments (component/, feature/, recipe/…) and the plugin
// segment without colliding: no catalog entity kind lowercases to
// "docs", so a bundle published here can never shadow an entity pull.
const dataArtifactNamespace = "docs"

// PushArtifactConfig is the input contract for the generic data-artifact
// publication workflow. PushArtifact reads ONLY from cfg, so concurrent callers
// cannot observe each other. Same shape discipline as PushConfig.
type PushArtifactConfig struct {
	FilePath  string
	Name      string
	Tag       string
	MediaType string
	Registry  string
	DryRun    bool

	// Workspace is the resolved workspace the caller threads in. Required:
	// PushArtifact refuses a nil rather than resolving one of its own, so a
	// caller declares the workspace it resolved.
	Workspace *workspaceconfig.ResolvedWorkspace

	// DestinationSupply carries the caller's own vocabulary into the refusal a
	// publication with nowhere to go draws. Zero yields the bare statement of what
	// is missing, which is true for every caller.
	//
	// There is no identity supply beside it because this operation resolves no
	// identity at all: a data artifact is signed afterwards, by reference.
	DestinationSupply DestinationSupply

	// PushTarget overrides the production remote-repository construction. A
	// caller may inject an in-memory or file-backed oras.Target via this field;
	// nil in production. When nil and DryRun is false, PushArtifact constructs
	// the target from the reference.
	PushTarget oras.Target

	// Now supplies the publication date the publication states beside the
	// artifact. Nil reads the live clock; tests inject a fixed time.
	Now func() time.Time

	// Out is the sink for the operation's user-facing status output (the dry-run
	// line, the publication line and the publication-date note). Nil defaults to
	// os.Stdout, so a caller that wants to capture the output passes a buffer
	// rather than mutating a process global.
	Out io.Writer
}

// PushArtifactResult reports what a data-artifact publication did, as the facts
// themselves rather than as the status lines PushArtifact also writes to cfg.Out.
//
// It carries the same shape discipline PushResult carries: every field is filled
// on every path, and the two booleans say which branch ran — a dry run contacted
// nothing, and an already-published re-publication landed nothing because the
// bytes were already there.
type PushArtifactResult struct {
	// Reference and Tag address the published artifact.
	Reference string
	Tag       string
	// MediaType is what the opaque bytes were published under, which is the
	// caller's value or the documentation-bundle default it fell through to.
	MediaType string
	// Size is the published file's byte count.
	Size int
	// Digest is the published manifest's digest, and EMPTY on a dry run for the
	// same reason PushResult.Digest is.
	Digest string
	// AlreadyPublished reports that the tag already pointed at byte-identical
	// content, so the publication was an idempotent no-op.
	AlreadyPublished bool
	// DryRun reports that nothing was published.
	DryRun bool
}

// PushArtifact publishes the file at cfg.FilePath as a standalone OCI data
// artifact at <registry>/docs/<name>:<tag>, registry being the destination's
// location prefix, where a documentation reader looks for it through the same
// default. It reads the file bytes verbatim
// and hands them to the kind-agnostic pkg/oci.PushNamed transport under
// cfg.MediaType, named cfg.Name — it never parses an entity envelope, never
// dispatches on a Kind, and never resolves a mediatype through
// MediaTypeFor. The bytes are opaque: any media type is accepted,
// defaulting to the documentation-bundle media type.
//
// Per Aggrega's deterministic-output discipline, callers SHOULD supply the
// bundle bytes pre-canonicalised (sorted tar entries, zero timestamps, gzip
// default compression); the transport adds no canonicalisation but pins the
// manifest's creation annotation, so byte-identical content published under
// the same name yields a byte-identical manifest digest.
//
// Signing is not performed here: the published artifact is signed by reference
// with the signing verb, which is kind-agnostic and needs only the reference this
// operation reports. So the publication states when it landed, as a published-at
// referrer beside the artifact, which moves no digest.
//
// When cfg.DryRun is true, PushArtifact prints the proposed <reference>:<tag>
// plus media type and byte count and returns without constructing a remote
// target.
func PushArtifact(ctx context.Context, cfg PushArtifactConfig) (PushArtifactResult, error) {
	if cfg.FilePath == "" {
		return PushArtifactResult{}, fmt.Errorf("artifact file path required (pass as the positional arg)")
	}
	if cfg.Name == "" {
		return PushArtifactResult{}, fmt.Errorf("artifact name required (--name)")
	}
	if cfg.Tag == "" {
		return PushArtifactResult{}, fmt.Errorf("version tag required (--tag)")
	}
	mediaType := cfg.MediaType
	if mediaType == "" {
		mediaType = oci.MediaTypeDocs
	}
	out := cfg.Out
	if out == nil {
		out = os.Stdout
	}

	rw, err := requireWorkspace(cfg.Workspace)
	if err != nil {
		return PushArtifactResult{}, err
	}

	// Publishing an artifact is a network round trip, refused before the file is
	// read. The dry run is excluded by what it does rather than by its name: it
	// returns below without constructing a target.
	if !cfg.DryRun {
		if err := refuseIfOffline(rw, "publishing a data artifact is a network round trip"); err != nil {
			return PushArtifactResult{}, err
		}
	}

	wsCfg := rw.Config()
	dest, err := destination(wsCfg, cfg.Registry, cfg.DestinationSupply)
	if err != nil {
		return PushArtifactResult{}, err
	}

	reference := dest.Prefix() + "/" + dataArtifactNamespace + "/" + cfg.Name

	content, err := os.ReadFile(cfg.FilePath)
	if err != nil {
		return PushArtifactResult{}, fmt.Errorf("read %s: %w", cfg.FilePath, err)
	}

	result := PushArtifactResult{
		Reference: reference,
		Tag:       cfg.Tag,
		MediaType: mediaType,
		Size:      len(content),
	}

	if cfg.DryRun {
		fmt.Fprintf(out, "aggrega push-artifact (dry-run): %s:%s\n", reference, cfg.Tag)
		writePublicationFacts(out, "", mediaType, len(content), 0)
		result.DryRun = true
		return result, nil
	}

	target := cfg.PushTarget
	if target == nil {
		httpClient, err := egressClient(wsCfg)
		if err != nil {
			return PushArtifactResult{}, fmt.Errorf("%s: %w", reference, err)
		}
		repo, err := oci.NewRemoteRepository(reference, httpClient)
		if err != nil {
			return PushArtifactResult{}, fmt.Errorf("%s: %w", reference, err)
		}
		target = repo
	}

	// Immutability guard on the version tag: a re-publication of a byte-identical
	// artifact is an idempotent no-op; a tag already pointing at different
	// content is refused. The proposed digest is previewed with the SAME
	// (mediaType, file name, content, nil-annotations) inputs oci.PushNamed
	// receives below, so the preview digest equals the digest the publication
	// would land.
	//
	// The file name a standard registry client writes the artifact to is the name
	// the publisher gave it. A data artifact carries no entity, so no name can be
	// derived from what it is, and the publisher's name is already the artifact's
	// identity at docs/<name> — which keeps the manifest a function of that
	// identity rather than of the local file it was read from.
	//
	// The last point before anything reaches the registry. Everything above is
	// local — reading the file, deriving the reference, building the content — and
	// a caller that withdrew discards all of it; what follows is published and
	// cannot be taken back.
	if serr := stopped(ctx); serr != nil {
		return PushArtifactResult{}, serr
	}

	existingDigest, exists, err := oci.Resolve(ctx, target, cfg.Tag)
	if err != nil {
		return PushArtifactResult{}, fmt.Errorf("resolve %s:%s: %w", reference, cfg.Tag, err)
	}
	proposedDigest, err := oci.ManifestDigestNamed(ctx, mediaType, cfg.Name, content, nil)
	if err != nil {
		return PushArtifactResult{}, fmt.Errorf("%s:%s: %w", reference, cfg.Tag, err)
	}
	if exists {
		if existingDigest == proposedDigest {
			fmt.Fprintf(out, "Already published: %s:%s (%s)\n", reference, cfg.Tag, existingDigest)
			writePublicationFacts(out, "", mediaType, len(content), 0)
			result.Digest = existingDigest
			result.AlreadyPublished = true
			return result, nil
		}
		return PushArtifactResult{}, fmt.Errorf("%s:%s is already published as %s; refusing to overwrite with %s — publish a new version: %w",
			reference, cfg.Tag, existingDigest, proposedDigest, oci.ErrImmutableTagConflict())
	}

	digest, err := oci.PushNamed(ctx, target, cfg.Tag, mediaType, cfg.Name, content, nil)
	if err != nil {
		return PushArtifactResult{}, fmt.Errorf("push %s:%s: %w", reference, cfg.Tag, err)
	}
	fmt.Fprintf(out, "Push complete: %s:%s (%s)\n", reference, cfg.Tag, digest)
	writePublicationFacts(out, "", mediaType, len(content), 0)
	result.Digest = digest
	if err := recordPublicationDate(ctx, out, target, reference, cfg.Tag, nil, cfg.Now); err != nil {
		return PushArtifactResult{}, err
	}
	return result, nil
}
