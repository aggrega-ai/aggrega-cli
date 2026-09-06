// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package publishops

import (
	"context"
	"fmt"
	"io"
	"os"

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

	// PushTarget overrides the production remote-repository construction. A
	// caller may inject an in-memory or file-backed oras.Target via this field;
	// nil in production. When nil and DryRun is false, PushArtifact constructs
	// the target from the reference.
	PushTarget oras.Target

	// Out is the sink for the operation's user-facing status output (the dry-run
	// line and the publication line). Nil defaults to os.Stdout, so a caller
	// that wants to capture the output passes a buffer rather than mutating a
	// process global.
	Out io.Writer
}

// PushArtifact publishes the file at cfg.FilePath as a standalone OCI data
// artifact at <registry>/docs/<name>:<tag>. It reads the file bytes verbatim
// and hands them to the kind-agnostic pkg/oci.Push transport under cfg.MediaType
// — it never parses an entity envelope, never dispatches on a Kind, and never
// resolves a mediatype through MediaTypeForKind. The bytes are opaque: any
// media type is accepted, defaulting to the documentation-bundle media type.
//
// Per Aggrega's deterministic-output discipline, callers SHOULD supply the
// bundle bytes pre-canonicalised (sorted tar entries, zero timestamps, gzip
// default compression); Push adds no canonicalisation but pins the manifest's
// creation annotation, so byte-identical content yields a byte-identical
// manifest digest.
//
// Signing is not performed here: the published artifact is signed by reference
// with the signing verb, which is kind-agnostic and needs only the reference this
// operation reports.
//
// When cfg.DryRun is true, PushArtifact prints the proposed <reference>:<tag>
// plus media type and byte count and returns without constructing a remote
// target.
func PushArtifact(cfg PushArtifactConfig) error {
	if cfg.FilePath == "" {
		return fmt.Errorf("artifact file path required (pass as the positional arg)")
	}
	if cfg.Name == "" {
		return fmt.Errorf("artifact name required (--name)")
	}
	if cfg.Tag == "" {
		return fmt.Errorf("version tag required (--tag)")
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
		return fmt.Errorf("workspaceconfig: %w", err)
	}

	wsCfg := rw.Config()
	token := cfg.Registry
	if token == "" {
		token = wsCfg.DefaultRegistry
	}
	if token == "" {
		return fmt.Errorf("no destination registry; set --registry or workspace.default_registry")
	}
	registry, err := wsCfg.ResolveRegistry(token)
	if err != nil {
		return err
	}

	reference := registry + "/" + dataArtifactNamespace + "/" + cfg.Name

	content, err := os.ReadFile(cfg.FilePath)
	if err != nil {
		return fmt.Errorf("read %s: %w", cfg.FilePath, err)
	}

	if cfg.DryRun {
		fmt.Fprintf(out, "aggrega push-artifact (dry-run): %s:%s\n  mediatype: %s\n  size: %d bytes\n",
			reference, cfg.Tag, mediaType, len(content))
		return nil
	}

	target := cfg.PushTarget
	if target == nil {
		httpClient, err := egressClient(wsCfg)
		if err != nil {
			return fmt.Errorf("%s: %w", reference, err)
		}
		repo, err := oci.NewRemoteRepository(reference, httpClient)
		if err != nil {
			return fmt.Errorf("%s: %w", reference, err)
		}
		target = repo
	}

	// Immutability guard on the version tag: a re-publication of a byte-identical
	// artifact is an idempotent no-op; a tag already pointing at different
	// content is refused. The proposed digest is previewed with the SAME
	// (mediaType, content, nil-annotations) triple oci.Push receives below, so
	// the preview digest equals the digest the publication would land.
	existingDigest, exists, err := oci.Resolve(context.Background(), target, cfg.Tag)
	if err != nil {
		return fmt.Errorf("resolve %s:%s: %w", reference, cfg.Tag, err)
	}
	proposedDigest, err := oci.ManifestDigest(context.Background(), mediaType, content, nil)
	if err != nil {
		return fmt.Errorf("%s:%s: %w", reference, cfg.Tag, err)
	}
	if exists {
		if existingDigest == proposedDigest {
			fmt.Fprintf(out, "Already published: %s:%s (%s)\n", reference, cfg.Tag, existingDigest)
			return nil
		}
		return fmt.Errorf("%s:%s is already published as %s; refusing to overwrite with %s — publish a new version: %w",
			reference, cfg.Tag, existingDigest, proposedDigest, oci.ErrImmutableTagConflict())
	}

	digest, err := oci.Push(context.Background(), target, cfg.Tag, mediaType, content, nil)
	if err != nil {
		return fmt.Errorf("push %s:%s: %w", reference, cfg.Tag, err)
	}
	fmt.Fprintf(out, "Push complete: %s:%s (%s)\n", reference, cfg.Tag, digest)
	return nil
}
