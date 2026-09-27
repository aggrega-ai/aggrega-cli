// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/aggrega-ai/aggrega-cli/internal/publishops"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
)

// pushArtifactFlags carries the `push-artifact` command's flag state. It is
// constructed per command so two invocations in one process cannot observe each
// other's flag values. It is distinct from pushArtifactConfig, which is the
// input contract pushArtifactRun reads.
type pushArtifactFlags struct {
	name      string
	tag       string
	mediaType string
	registry  string
	dryRun    bool
}

// pushArtifactConfig is the data-artifact publication operation's input
// contract, which lives with the operation rather than with this command.
type pushArtifactConfig = publishops.PushArtifactConfig

func newPushArtifactCmd() *cobra.Command {
	opts := &pushArtifactFlags{}
	c := &cobra.Command{
		Use:   "push-artifact <file>",
		Short: "Publish a standalone data artifact (such as a documentation bundle) to an OCI registry",
		Long: "Publish a standalone data artifact — a documentation bundle or any other non-entity blob — to an OCI " +
			"registry under the reserved docs/ namespace. Unlike push, which bundles a catalog entity and dispatches on " +
			"its kind, push-artifact carries opaque bytes: it reads the file, packs it under the given media type " +
			"(defaulting to the documentation-bundle media type), and tags it at docs/<name>:<tag>. The published " +
			"artifact is signed separately by reference with aggrega sign. Beside it, push-artifact states when it " +
			"published, as a small published-at attachment carrying this machine's clock in UTC to the second; the " +
			"attachment moves no digest and changes no tag.",
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
			return pushArtifactRun(cmd.Context(), pushArtifactConfig{
				FilePath:  path,
				Name:      opts.name,
				Tag:       opts.tag,
				MediaType: opts.mediaType,
				Registry:  opts.registry,
				DryRun:    opts.dryRun,
				Workspace: rw,
			})
		},
	}
	c.Flags().StringVar(&opts.name, "name", "", "Artifact name within the reserved docs/ namespace (e.g. \"aggrega-docs\" publishes at docs/aggrega-docs)")
	c.Flags().StringVar(&opts.tag, "tag", "", "Version tag to publish the artifact under (required)")
	c.Flags().StringVar(&opts.mediaType, "media-type", oci.MediaTypeDocs, "OCI artifact media type; defaults to the documentation-bundle media type")
	c.Flags().StringVar(&opts.registry, "registry", "", "Destination registry host (overrides the workspace's default_registry)")
	c.Flags().BoolVar(&opts.dryRun, "dry-run", false, "Compute the reference and print the proposed <reference>:<tag>; skip the network call")
	return c
}

// pushArtifactRun is the command line's adapter onto the data-artifact
// publication operation: it names the command in any failure, which the
// operation itself does not, because a caller elsewhere never typed one.
//
// The facts the operation reports are discarded here for the reason the entity
// publication discards its own: the operation writes an account of itself to its
// status output which names all of them — the reference and tag and the digest on
// the line itself, the media type and the byte count beneath it — and whether
// nothing was published or the bytes were already there is which line was
// written. Restating that here would duplicate it rather than add to it.
func pushArtifactRun(ctx context.Context, cfg pushArtifactConfig) error {
	cfg.DestinationSupply = commandLineDestinationSupply
	// projection-omits PushArtifactResult: the publication writes an account of itself to this surface's own status output that names every fact the result carries, so projecting it here would restate in full what the terminal has just been shown
	if _, err := publishops.PushArtifact(ctx, cfg); err != nil {
		return fmt.Errorf("aggrega push-artifact: %w", err)
	}
	return nil
}
