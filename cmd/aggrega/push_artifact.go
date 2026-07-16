// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"
	"oras.land/oras-go/v2"

	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// dataArtifactNamespace is the reserved top-level registry segment for
// standalone data artifacts that are not catalog entities. It sits alongside
// the entity-kind segments (component/, feature/, recipe/…) and the plugin
// segment (wasm/) without colliding: no catalog entity kind lowercases to
// "docs", so a bundle published here can never shadow an entity pull.
const dataArtifactNamespace = "docs"

var (
	pushArtifactName      string
	pushArtifactTag       string
	pushArtifactMediaType string
	pushArtifactRegistry  string
	pushArtifactDryRun    bool
)

func newPushArtifactCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "push-artifact <file>",
		Short: "Publish a standalone data artifact (such as a documentation bundle) to an OCI registry",
		Long: "Publish a standalone data artifact — a documentation bundle or any other non-entity blob — to an OCI " +
			"registry under the reserved docs/ namespace. Unlike push, which bundles a catalog entity and dispatches on " +
			"its kind, push-artifact carries opaque bytes: it reads the file, packs it under the given media type " +
			"(defaulting to the documentation-bundle media type), and tags it at docs/<name>:<tag>. The published " +
			"artifact is signed separately by reference with aggrega sign.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			path := ""
			if len(args) > 0 {
				path = args[0]
			}
			rw, err := workspaceFromContext(cmd.Context())
			if err != nil {
				return err
			}
			return pushArtifactRun(pushArtifactConfig{
				FilePath:  path,
				Name:      pushArtifactName,
				Tag:       pushArtifactTag,
				MediaType: pushArtifactMediaType,
				Registry:  pushArtifactRegistry,
				DryRun:    pushArtifactDryRun,
				ws:        rw,
			})
		},
	}
	c.Flags().StringVar(&pushArtifactName, "name", "", "Artifact name within the reserved docs/ namespace (e.g. \"aggrega-docs\" publishes at docs/aggrega-docs)")
	c.Flags().StringVar(&pushArtifactTag, "tag", "", "Version tag to publish the artifact under (required)")
	c.Flags().StringVar(&pushArtifactMediaType, "media-type", oci.MediaTypeDocs, "OCI artifact media type; defaults to the documentation-bundle media type")
	c.Flags().StringVar(&pushArtifactRegistry, "registry", "", "Destination registry host (overrides the workspace's default_registry)")
	c.Flags().BoolVar(&pushArtifactDryRun, "dry-run", false, "Compute the reference and print the proposed <reference>:<tag>; skip the network call")
	return c
}

// pushArtifactConfig is the input contract for the generic data-artifact push
// workflow. The cobra command populates it from the flag-bound globals;
// pushArtifactRun reads ONLY from cfg (no package-level globals referenced
// inside the function body) so test bodies stay race-free under t.Parallel.
// Same shape discipline as pushConfig.
type pushArtifactConfig struct {
	FilePath  string
	Name      string
	Tag       string
	MediaType string
	Registry  string
	DryRun    bool

	// ws is the resolved workspace, threaded in by the command's RunE. Nil only
	// when a test invokes pushArtifactRun directly; workspaceOrResolve then
	// resolves from the working directory.
	ws *workspaceconfig.ResolvedWorkspace

	// PushTarget overrides the production remote.Repository construction. Tests
	// inject an in-memory or file-backed oras.Target via this field; nil in
	// production. When nil and DryRun is false, pushArtifactRun constructs the
	// target via oci.NewRemoteRepository.
	PushTarget oras.Target

	// Out is the sink for the command's user-facing status output (the dry-run
	// line and "Push complete"). Nil defaults to os.Stdout at the top of
	// pushArtifactRun (production); tests pass a buffer to capture the output
	// without mutating a process global.
	Out io.Writer
}

// pushArtifactRun publishes the file at cfg.FilePath as a standalone OCI data
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
// with aggrega sign, which is kind-agnostic and needs only the reference this
// command prints.
//
// When cfg.DryRun is true, pushArtifactRun prints the proposed <reference>:<tag>
// plus media type and byte count and returns without constructing a remote
// target.
func pushArtifactRun(cfg pushArtifactConfig) error {
	if cfg.FilePath == "" {
		return fmt.Errorf("aggrega push-artifact: artifact file path required (pass as the positional arg)")
	}
	if cfg.Name == "" {
		return fmt.Errorf("aggrega push-artifact: artifact name required (--name)")
	}
	if cfg.Tag == "" {
		return fmt.Errorf("aggrega push-artifact: version tag required (--tag)")
	}
	mediaType := cfg.MediaType
	if mediaType == "" {
		mediaType = oci.MediaTypeDocs
	}
	out := cfg.Out
	if out == nil {
		out = os.Stdout
	}

	rw, err := workspaceOrResolve(cfg.ws)
	if err != nil {
		return fmt.Errorf("workspaceconfig: %w", err)
	}

	wsCfg := rw.Config()
	token := cfg.Registry
	if token == "" {
		token = wsCfg.DefaultRegistry
	}
	if token == "" {
		return fmt.Errorf("aggrega push-artifact: no destination registry; set --registry or workspace.default_registry")
	}
	registry, err := wsCfg.ResolveRegistry(token)
	if err != nil {
		return fmt.Errorf("aggrega push-artifact: %w", err)
	}

	reference := registry + "/" + dataArtifactNamespace + "/" + cfg.Name

	content, err := os.ReadFile(cfg.FilePath)
	if err != nil {
		return fmt.Errorf("aggrega push-artifact: read %s: %w", cfg.FilePath, err)
	}

	if cfg.DryRun {
		fmt.Fprintf(out, "aggrega push-artifact (dry-run): %s:%s\n  mediatype: %s\n  size: %d bytes\n",
			reference, cfg.Tag, mediaType, len(content))
		return nil
	}

	target := cfg.PushTarget
	if target == nil {
		repo, err := oci.NewRemoteRepository(reference)
		if err != nil {
			return fmt.Errorf("aggrega push-artifact: %s: %w", reference, err)
		}
		target = repo
	}

	digest, err := oci.Push(context.Background(), target, cfg.Tag, mediaType, content, nil)
	if err != nil {
		return fmt.Errorf("aggrega push-artifact: push %s:%s: %w", reference, cfg.Tag, err)
	}
	fmt.Fprintf(out, "Push complete: %s:%s (%s)\n", reference, cfg.Tag, digest)
	return nil
}
