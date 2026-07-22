// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"
	"go.yaml.in/yaml/v3"
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
	pushKeyPath    string
	pushFulcio     string
	pushRekor      string
	pushTimestamp  string
	pushIDToken    string
	pushPluginPath string
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
				EntityPath:   path,
				Registry:     pushRegistry,
				DryRun:       pushDryRun,
				KeyPath:      pushKeyPath,
				FulcioURL:    pushFulcio,
				RekorURL:     pushRekor,
				TimestampURL: pushTimestamp,
				IDToken:      pushIDToken,
				PluginPath:   pushPluginPath,
				ws:           rw,
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
	c.Flags().StringVar(&pushKeyPath, "key", "", "Path to a held ECDSA P-256 private key PEM for offline keyed signing. When omitted, keyless signing is used.")
	c.Flags().StringVar(&pushFulcio, "fulcio-url", "", "Fulcio certificate-authority URL for keyless signing. When omitted, the public-good Fulcio instance is used.")
	c.Flags().StringVar(&pushRekor, "rekor-url", "", "Rekor transparency-log URL for keyless signing. When omitted, the public-good Rekor instance is used.")
	c.Flags().StringVar(&pushTimestamp, "timestamp-url", "", "Optional RFC3161 timestamp-authority URL for keyless signing.")
	c.Flags().StringVar(&pushIDToken, "identity-token", "", "OIDC identity token for keyless signing. Optional at an interactive terminal (a browser flow acquires one) and on GitHub Actions (the workflow OIDC token is minted automatically); required for other non-interactive/CI use, where it may instead be exported as AGGREGA_IDENTITY_TOKEN or SIGSTORE_ID_TOKEN.")
	c.Flags().StringVar(&pushPluginPath, "plugin", "", "Path to the local .wasm artifact for a Plugin entity. Required when publishing a Plugin: publish pushes the .wasm artifact, computes its content digest, and pushes the entity.")
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

	// KeyPath, FulcioURL, RekorURL, TimestampURL, and IDToken are the
	// signing inputs from the CLI flags. They override the workspace
	// config's signing block; an empty value falls through to the
	// per-registry then global config, then to the public-good endpoint
	// default. IDToken is never sourced from config — it is a short-lived
	// secret supplied only by flag or the ambient OIDC environment.
	KeyPath      string
	FulcioURL    string
	RekorURL     string
	TimestampURL string
	IDToken      string

	// PluginPath is the local filesystem path to the .wasm artifact, required
	// when publishing a Plugin entity. Publish reads these bytes,
	// cross-checks them against the entity's authored claims, computes their
	// content digest, and pushes them as an OCI artifact addressed by that
	// digest before pushing the entity. Empty for every non-Plugin kind.
	PluginPath string

	// ws is the resolved workspace, threaded in by the command's RunE. Nil only
	// when a test invokes pushRun directly; workspaceOrResolve then resolves
	// from the working directory.
	ws *workspaceconfig.ResolvedWorkspace

	// EnvLookup reads the environment for the identity-token secret resolution.
	// Nil means production (os.Getenv); tests inject a frozen lookup so they
	// stay race-free under t.Parallel.
	EnvLookup func(string) string

	// TokenGetter acquires a keyless OIDC identity token interactively when no
	// explicit token is supplied. Nil means production
	// (acquireInteractiveIDToken); tests inject a fake so no browser opens.
	TokenGetter func() (string, error)

	// IsTTY reports whether stdin is an interactive terminal, gating the
	// interactive acquisition path. Nil means production (isTerminal); tests
	// inject a fixed value so the decision is deterministic.
	IsTTY func() bool

	// PushTarget overrides the production remote.Repository
	// construction. Tests inject an in-memory oras.Target via this
	// field; nil in production. When nil and DryRun is false,
	// pushRun constructs the target via oci.NewRemoteRepository.
	PushTarget oras.Target

	// Signer produces the signature bundle when signing is requested.
	// Tests inject a fake; production constructs a real signer from the
	// resolved signing inputs. When signing is requested and Signer is
	// nil, pushRun builds a keyed or keyless signer via buildSigner.
	Signer signing.Signer

	// Out is the sink for the command's user-facing status output (the
	// dry-run line, "Push complete", "Already published", and the
	// signature-attached note). Nil defaults to os.Stdout at the top of
	// pushRun (production); tests pass a buffer to capture the output
	// without mutating a process global.
	Out io.Writer
}

// componentCategoryLabel is the metadata.labels key whose value the standard
// catalog convention sets to an entity's category. Its value is stamped as the
// searchable category annotation at publish so a registry index can filter by
// category without fetching the entity body.
const componentCategoryLabel = "component.aggrega.ai/category"

// pushEntityHeader is the minimal YAML envelope pushRun parses out
// of the entity file: kind, metadata.name, metadata.version,
// metadata.description, and metadata.labels. The shape mirrors the
// per-kind envelope pkg/oci/bundle.go uses for
// validateEntityVersionPathSubdir; duplicated here because the push
// verb needs the same fields for every kind. Description and labels feed
// the searchable annotations stamped onto the published manifest.
type pushEntityHeader struct {
	Kind     string `yaml:"kind"`
	Metadata struct {
		Name        string            `yaml:"name"`
		Version     string            `yaml:"version"`
		Description string            `yaml:"description"`
		Labels      map[string]string `yaml:"labels"`
	} `yaml:"metadata"`
	Spec struct {
		// Capability is the render-engine capability for Recipe / FeatureRecipe.
		// It is part of those kinds' identity (recipeIdentityKey is
		// provider/name:version), so the OCI location must encode it — two
		// providers may ship a recipe of the same name.
		Capability string `yaml:"capability"`
	} `yaml:"spec"`
}

// pushRun bundles the entity at cfg.EntityPath and publishes it.
// Reads workspace config; parses the entity envelope (kind,
// metadata.name, metadata.version); bundles via pkg/oci.Bundle (which
// handles per-kind dispatch including per-version subdir enforcement for
// every publishable kind and Recipe / FeatureRecipe tar.gz directory
// walking); constructs the OCI repository reference; pushes via pkg/oci.Push.
//
// Signing is sign-before-push: when signing is requested, pushRun computes
// the proposed manifest digest, constructs the signer, and signs the digest
// BEFORE any bytes reach the registry. If the signer cannot be constructed or
// signing fails, pushRun refuses before the push — zero bytes land, so an
// unsigned artifact can never leak onto a signature-requiring registry. The
// signature bundle is attached as a referrer only after the push succeeds; the
// bundle commits to the digest string, so the pre-push signature is valid for
// the published bytes.
//
// Returns wrapped errors on workspace-config validation failure,
// entity-file read / parse failure, bundle failure (including
// ErrUnknownEntityKind for non-publishable kinds and
// ErrEntityVersionPathMismatch for an entity whose metadata.version
// disagrees with its per-version subdirectory), signer-construction /
// signing failure (refused before the push), and push failure.
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
		return fmt.Errorf("aggrega push: no destination registry; set --registry or workspace.default_registry")
	}
	registry, err := wsCfg.ResolveRegistry(token)
	if err != nil {
		return fmt.Errorf("aggrega push: %w", err)
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

	// Signing policy. A push is refused unsigned when EITHER the destination
	// registry requires signatures OR the workspace signing policy requires a
	// signature for this artifact kind. Signing is on by default whenever
	// either condition holds. An explicit --sign value overrides the default
	// for whether to sign, but never relaxes the refusal: there is no flag to
	// push unsigned content when a signature is required. The kind is read from
	// the entity header (parsed above) so the org policy's kind scope can be
	// evaluated before any bytes reach the registry — the refusal, like the
	// per-registry one, halts before the push network call so an unsigned
	// artifact never leaks.
	requiresSignatures := wsCfg.RegistrySigningRequiredForKind(registryhost.Of(registry), header.Kind)
	orgRequired := wsCfg.SigningRequiredForKind(header.Kind)
	wantSign := requiresSignatures || orgRequired
	if cfg.Sign != nil {
		wantSign = *cfg.Sign
	}
	if (requiresSignatures || orgRequired) && !wantSign {
		// Distinguish the cause: the org-policy refusal names the kind and
		// wraps its own sentinel; the per-registry refusal keeps the
		// registry-specific message and sentinel. When both conditions hold the
		// registry requirement is reported (it is the more specific
		// destination-scoped cause).
		if requiresSignatures {
			return fmt.Errorf("aggrega push: registry %q requires signatures; re-run signing the artifact (there is no flag to push unsigned content here): %w",
				registry, signing.ErrUnsignedPushRefused)
		}
		return fmt.Errorf("aggrega push: the signing policy requires a signature for kind %q; re-run signing the artifact (there is no flag to push unsigned content here): %w",
			header.Kind, signing.ErrSigningPolicyRequiresSignature)
	}

	// A Plugin publishes as ONE manifest — the entity YAML as the config blob,
	// the .wasm as an inline layer addressed by its content digest — with a
	// publish-time describe()-cross-check against the .wasm bytes. The entity
	// body still carries a machine-derived host-function record; that plus the
	// entity-and-executable-in-one-manifest packing differs enough from the
	// single-artifact catalog-kind path that it has its own helper; signing,
	// the immutability guard, and the content-digest spelling are reused, not
	// re-spelled.
	if header.Kind == "Plugin" {
		return pushPlugin(out, cfg, registry, wantSign, header.Metadata.Name, header.Metadata.Version, cfg.EntityPath, data)
	}

	bundleBytes, mediaType, err := oci.Bundle(header.Kind, cfg.EntityPath)
	if err != nil {
		return fmt.Errorf("aggrega push: bundle %s: %w", cfg.EntityPath, err)
	}

	reference := buildPushReference(registry, header.Kind, header.Spec.Capability, header.Metadata.Name)
	tag := header.Metadata.Version

	// Searchable catalog annotations, built once and stamped identically onto
	// the digest preview and the real push. Building them once is load-bearing:
	// the previewed digest is signed and compared by the immutability guard, so
	// the preview must stamp exactly the annotations the push will.
	annotations := oci.SearchableMetadata{
		Description: header.Metadata.Description,
		Category:    header.Metadata.Labels[componentCategoryLabel],
		Kind:        header.Kind,
	}.Annotations()

	if cfg.DryRun {
		fmt.Fprintf(out, "aggrega push (dry-run): %s:%s\n  kind: %s\n  mediatype: %s\n  size: %d bytes\n",
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
	proposedDigest, err := oci.ManifestDigest(ctx, mediaType, bundleBytes, annotations)
	if err != nil {
		return fmt.Errorf("aggrega push: %s:%s: %w", reference, tag, err)
	}
	if exists {
		if existingDigest == proposedDigest {
			fmt.Fprintf(out, "Already published: %s:%s (%s)\n", reference, tag, existingDigest)
			return nil
		}
		return fmt.Errorf("aggrega push: %s:%s is already published as %s; refusing to overwrite with %s — publish a new version (aggrega lint --pre-publish verifies the bump): %w",
			reference, tag, existingDigest, proposedDigest, oci.ErrImmutableTagConflict)
	}

	// Sign-before-push: produce the signature bundle BEFORE any bytes reach
	// the registry. The manifest digest is known pre-push, and the bundle
	// commits to the digest string, so the signature is valid for the bytes
	// that will land. If the signer cannot be constructed or signing fails,
	// refuse here — nothing is pushed, so an unsigned artifact never leaks
	// onto a signature-requiring registry. The bundle is held and attached
	// as a referrer only after the push succeeds.
	var (
		signatureBundle []byte
		signatureType   string
	)
	if wantSign {
		signatureBundle, signatureType, err = signPushDigest(ctx, cfg, registry, reference, tag, proposedDigest)
		if err != nil {
			return err
		}
	}

	digest, err := retryTransient(ctx, func() (string, error) {
		return oci.Push(ctx, target, tag, mediaType, bundleBytes, annotations)
	}, isRetryablePublishError)
	if err != nil {
		return fmt.Errorf("aggrega push: push %s:%s: %w", reference, tag, err)
	}
	fmt.Fprintf(out, "Push complete: %s:%s (%s)\n", reference, tag, digest)

	if wantSign {
		if err := attachBundle(ctx, target, tag, signatureType, signatureBundle); err != nil {
			return fmt.Errorf("aggrega push: attach signature to %s:%s: %w", reference, tag, err)
		}
		fmt.Fprintf(out, "Signature attached: %s:%s\n", reference, tag)
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
// Plugin kinds carry a pluginType scope in their identity: the path
// segment is `plugin`, and the caller passes the plugin's
// metadata.pluginType as the scope — e.g., pluginType="linter" with a
// bare metadata.name="aggrega-publishing" produces
// <registry>/plugin/linter/aggrega-publishing. The pluginType segment
// is part of the plugin's identity (same name in different sockets is a
// different plugin); deriving the location from pluginType plus the bare
// name keeps the URI symmetric with the catalog filesystem layout
// `aggrega-hub/aggrega-catalog/plugin/<pluginType>/<name>`.
//
// The push-side construction here MUST agree with the consume-side
// catalog.SplitURIForOCI which parses URIs back into their
// (reference, tag) pair at the closure walker's Tier-3 dispatch.
// TestPushReferenceRoundTripsThroughSplitURIForOCI pins the
// symmetry across all publishable catalog kinds.
func buildPushReference(registry, kind, provider, metadataName string) string {
	return registry + "/" + catalog.OCILocation(kind, provider, metadataName)
}
