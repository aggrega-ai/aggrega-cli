// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
	"oras.land/oras-go/v2"

	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/registryhost"
	"github.com/aggrega-ai/aggrega-cli/pkg/signing"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

var (
	pushEntityPath string
	pushRegistry   string
	pushDryRun     bool
	pushSign       bool
)

func newPushCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "push <entity-path>",
		Short: "Bundle and publish a catalog entity to an OCI registry",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			path := pushEntityPath
			if path == "" && len(args) > 0 {
				path = args[0]
			}
			rw, err := workspaceFromContext(cmd.Context())
			if err != nil {
				return err
			}
			pc := pushConfig{
				EntityPath: path,
				Registry:   pushRegistry,
				DryRun:     pushDryRun,
				ws:         rw,
			}
			// --sign is tri-state: when the operator passes it explicitly we
			// honour the value; otherwise the registry policy decides whether
			// to sign by default.
			if cmd.Flags().Changed("sign") {
				v := pushSign
				pc.Sign = &v
			}
			return pushRun(pc)
		},
	}
	c.Flags().StringVar(&pushEntityPath, "entity", "", "Path to the entity YAML to publish (alternatively pass as the positional arg)")
	c.Flags().StringVar(&pushRegistry, "registry", "", "Destination registry host (overrides the workspace's default_registry)")
	c.Flags().BoolVar(&pushDryRun, "dry-run", false, "Compute the bundle and print the proposed <reference>:<tag>; skip the network call")
	c.Flags().BoolVar(&pushSign, "sign", false, "Sign the published artifact and attach the signature as an OCI referrer. When omitted, signing follows the destination registry's policy.")
	return c
}

// pushConfig is the input contract for the push workflow. The cobra
// command populates it from the flag-bound globals at invocation
// time; pushRun reads ONLY from cfg (no package-level globals
// referenced inside the function body) so test bodies stay
// race-free under t.Parallel. Same shape as pullRun(pullConfig) /
// dispatchBuild(...).
type pushConfig struct {
	EntityPath string
	Registry   string
	DryRun     bool

	// Sign is the tri-state signing request. nil means "follow the
	// destination registry's policy" (sign by default to a
	// signature-requiring registry, do not sign otherwise). A non-nil
	// value is the operator's explicit --sign choice and overrides the
	// policy default — except that the strict-refuse guardrail below
	// always halts an unsigned push to a signature-requiring registry,
	// regardless of this field. There is no flag to bypass that refusal.
	Sign *bool

	// ws is the resolved workspace, threaded in by the command's RunE. Nil only
	// when a test invokes pushRun directly; workspaceOrResolve then resolves
	// from the working directory.
	ws *workspaceconfig.ResolvedWorkspace

	// PushTarget overrides the production remote.Repository
	// construction. Tests inject an in-memory oras.Target via this
	// field; nil in production. When nil and DryRun is false,
	// pushRun constructs the target via oci.NewRemoteRepository.
	PushTarget oras.Target

	// Signer produces the signature bundle when signing is requested.
	// Tests inject a fake; production constructs a real signer. When
	// signing is requested and Signer is nil, pushRun constructs the
	// keyless signer from the environment.
	Signer signing.Signer
}

// pushEntityHeader is the minimal YAML envelope pushRun parses out
// of the entity file: kind, metadata.name, metadata.version. The
// shape mirrors the per-kind envelope pkg/oci/bundle.go uses for
// validateEntityVersionPathSubdir; duplicated here because the push
// verb needs the same fields for every kind.
type pushEntityHeader struct {
	Kind     string `yaml:"kind"`
	Metadata struct {
		Name    string `yaml:"name"`
		Version string `yaml:"version"`
	} `yaml:"metadata"`
	Spec struct {
		// Provider is the execution engine for Recipe / FeatureRecipe. It is
		// part of those kinds' identity (recipeIdentityKey is
		// provider/name:version), so the OCI location must encode it — two
		// providers may ship a recipe of the same name.
		Provider string `yaml:"provider"`
	} `yaml:"spec"`
}

// pushRun bundles the entity at cfg.EntityPath and publishes it.
// Reads workspace config; parses the entity envelope (kind,
// metadata.name, metadata.version); bundles via pkg/oci.Bundle (which
// handles per-kind dispatch including per-version subdir enforcement for
// every publishable kind and Recipe / FeatureRecipe tar.gz directory
// walking); constructs the OCI repository reference; pushes via pkg/oci.Push.
//
// Returns wrapped errors on workspace-config validation failure,
// entity-file read / parse failure, bundle failure (including
// ErrUnknownEntityKind for non-publishable kinds and
// ErrEntityVersionPathMismatch for an entity whose metadata.version
// disagrees with its per-version subdirectory), and push failure.
//
// When cfg.DryRun is true, pushRun prints the proposed
// <reference>:<tag> + mediatype + bundle byte count and returns
// without constructing a remote target. Useful for CI authoring and
// for verifying the reference-shape construction without binding to
// a registry.
func pushRun(cfg pushConfig) error {
	if cfg.EntityPath == "" {
		return fmt.Errorf("aggrega push: entity path required (pass as positional arg or via --entity)")
	}

	rw, err := workspaceOrResolve(cfg.ws)
	if err != nil {
		return fmt.Errorf("workspaceconfig: %w", err)
	}
	wsCfg := rw.Config()

	registry := cfg.Registry
	if registry == "" {
		registry = wsCfg.DefaultRegistry
	}
	if registry == "" {
		return fmt.Errorf("aggrega push: no destination registry; set --registry or workspace.default_registry")
	}

	// Signing policy. The destination registry may require signatures; if
	// so, signing is on by default and an unsigned push is refused
	// outright. An explicit --sign value overrides the default for whether
	// to sign, but never relaxes the refusal: there is no flag to push
	// unsigned content to a signature-requiring registry.
	requiresSignatures := wsCfg.RegistryRequiresSignatures(registryhost.Of(registry))
	wantSign := requiresSignatures
	if cfg.Sign != nil {
		wantSign = *cfg.Sign
	}
	if requiresSignatures && !wantSign {
		return fmt.Errorf("aggrega push: registry %q requires signatures; re-run signing the artifact (there is no flag to push unsigned content here): %w",
			registry, signing.ErrUnsignedPushRefused)
	}

	data, err := os.ReadFile(cfg.EntityPath)
	if err != nil {
		return fmt.Errorf("aggrega push: read %s: %w", cfg.EntityPath, err)
	}
	var header pushEntityHeader
	if err := yaml.Unmarshal(data, &header); err != nil {
		return fmt.Errorf("aggrega push: parse %s: %w", cfg.EntityPath, err)
	}
	if header.Kind == "" {
		return fmt.Errorf("aggrega push: %s: missing kind field", cfg.EntityPath)
	}
	if header.Metadata.Name == "" {
		return fmt.Errorf("aggrega push: %s: missing metadata.name", cfg.EntityPath)
	}
	if header.Metadata.Version == "" {
		return fmt.Errorf("aggrega push: %s: missing metadata.version", cfg.EntityPath)
	}

	bundleBytes, mediaType, err := oci.Bundle(header.Kind, cfg.EntityPath)
	if err != nil {
		return fmt.Errorf("aggrega push: bundle %s: %w", cfg.EntityPath, err)
	}

	reference := buildPushReference(registry, header.Kind, header.Spec.Provider, header.Metadata.Name)
	tag := header.Metadata.Version

	if cfg.DryRun {
		fmt.Printf("aggrega push (dry-run): %s:%s\n  kind: %s\n  mediatype: %s\n  size: %d bytes\n",
			reference, tag, header.Kind, mediaType, len(bundleBytes))
		return nil
	}

	target := cfg.PushTarget
	if target == nil {
		repo, err := oci.NewRemoteRepository(reference)
		if err != nil {
			return fmt.Errorf("aggrega push: %s: %w", reference, err)
		}
		target = repo
	}

	ctx := context.Background()

	// Immutability guard: published bytes are frozen. HEAD the target tag
	// before publishing and compare the digest it points at with the digest
	// this content would produce. Re-pushing byte-identical content is an
	// idempotent no-op; overwriting an existing tag with different content is
	// refused. Changed content is published under a new version — aggrega lint
	// --pre-publish gates that bump against the previous release. There is no
	// --force escape: the registry has no overwrite path the operator can opt
	// into, by design.
	existingDigest, exists, err := oci.Resolve(ctx, target, tag)
	if err != nil {
		return fmt.Errorf("aggrega push: resolve %s:%s: %w", reference, tag, err)
	}
	proposedDigest, err := oci.ManifestDigest(ctx, mediaType, bundleBytes)
	if err != nil {
		return fmt.Errorf("aggrega push: %s:%s: %w", reference, tag, err)
	}
	if exists {
		if existingDigest == proposedDigest {
			fmt.Printf("Already published: %s:%s (%s)\n", reference, tag, existingDigest)
			return nil
		}
		return fmt.Errorf("aggrega push: %s:%s is already published as %s; refusing to overwrite with %s — publish a new version (aggrega lint --pre-publish verifies the bump): %w",
			reference, tag, existingDigest, proposedDigest, oci.ErrImmutableTagConflict)
	}

	digest, err := oci.Push(ctx, target, tag, mediaType, bundleBytes)
	if err != nil {
		return fmt.Errorf("aggrega push: push %s:%s: %w", reference, tag, err)
	}
	fmt.Printf("Push complete: %s:%s (%s)\n", reference, tag, digest)

	if wantSign {
		if cfg.Signer == nil {
			// A signature-requiring registry with no signer is refused
			// before the push (the guardrail above only fires when signing
			// was explicitly declined). Reaching here with no signer means
			// the operator requested --sign but supplied no signing
			// identity: halt rather than leave the artifact unsigned.
			return fmt.Errorf("aggrega push: signing requested but no signer configured: %w", signing.ErrUnsignedPushRefused)
		}
		if err := signPushedArtifact(ctx, target, tag, digest, cfg.Signer); err != nil {
			return fmt.Errorf("aggrega push: sign %s:%s: %w", reference, tag, err)
		}
		fmt.Printf("Signature attached: %s:%s\n", reference, tag)
	}
	return nil
}

// signPushedArtifact signs the pushed artifact's manifest digest and
// attaches the signature bundle as an OCI referrer of the just-published
// artifact. The artifact itself is untouched — the referrer is a sibling
// manifest pointing back at digest, so signing never re-pushes or mutates
// published bytes. subjectRef is the tag the artifact was published under.
func signPushedArtifact(ctx context.Context, target oras.Target, subjectRef, digest string, signer signing.Signer) error {
	bundleBytes, artifactType, err := signer.Sign(ctx, digest)
	if err != nil {
		return err
	}
	if _, err := oci.AttachReferrer(ctx, target, subjectRef, artifactType, bundleBytes); err != nil {
		return err
	}
	return nil
}

// buildPushReference composes the canonical OCI repository reference
// for a catalog entity: <registry>/<kindPathSegment>/<metadata.name>.
// For most kinds the path segment is the lowercased kind name
// (Component → component, Feature → feature, etc.) per the mediatype
// convention (application/vnd.aggrega.<kindLower>.v1...);
// metadata.name may itself contain "/" separators (e.g.,
// "core/server", "core/compute/api-server") and is preserved verbatim.
//
// Wasm-plugin kinds are special: the path segment is the canonical
// `wasm` (not `wasmplugin`), and the plugin's metadata.name path-
// encodes the socket — e.g., metadata.name="linter/aggrega-publishing"
// produces <registry>/wasm/linter/aggrega-publishing. The
// socket segment is part of the plugin's identity (same name in
// different sockets is a different plugin); embedding it in the
// metadata.name keeps the URI symmetric with the catalog filesystem
// layout `aggrega-hub/aggrega-catalog/plugin/<socket>/<name>.wasm`.
//
// The push-side construction here MUST agree with the consume-side
// catalog.SplitURIForOCI which parses URIs back into their
// (reference, tag) pair at the closure walker's Tier-3 dispatch.
// TestPushReferenceRoundTripsThroughSplitURIForOCI pins the
// symmetry across all publishable catalog kinds.
func buildPushReference(registry, kind, provider, metadataName string) string {
	return registry + "/" + catalog.OCILocation(kind, provider, metadataName)
}
