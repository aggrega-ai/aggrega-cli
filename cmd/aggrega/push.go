// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"

	"github.com/aggrega-ai/aggrega-cli/internal/publishops"
)

// pushFlags carries the `push` command's flag state. It is constructed per
// command so two invocations in one process cannot observe each other's flag
// values. It is distinct from pushConfig, which is the input contract pushRun
// reads.
type pushFlags struct {
	entityPath string
	registry   string
	dryRun     bool
	sign       bool
	keyPath    string
	fulcio     string
	rekor      string
	timestamp  string
	idToken    string
	pluginPath string
	listKinds  bool
	pinSources bool
}

// pushConfig is the publication operation's input contract, which lives with the
// operation rather than with this command: the same contract serves every surface
// that publishes. The alias keeps the command layer reading in its own vocabulary.
type pushConfig = publishops.PushConfig

func newPushCmd() *cobra.Command {
	opts := &pushFlags{}
	c := &cobra.Command{
		Use:   "push <entity-path>",
		Short: "Bundle and publish a catalog entity to an OCI registry",
		Long: `Publish a single catalog entity to an OCI registry, where other workspaces
resolve it with "aggrega pull".

The entity is read from the path given, bundled for transport, and pushed to
<registry>/<kind>/<metadata.name>:<metadata.version>, the repository path
lowercased; a Recipe or FeatureRecipe carries its spec.capability, and a Plugin
its metadata.pluginType, as a segment between <kind> and <metadata.name>. The
tag is the entity's own metadata.version. Run "aggrega push --list-kinds" for
the kinds this binary publishes, one per line and nothing else, so a script can
read it directly. Release and Manifest are deployment-side inputs rather than
catalog artifacts and are refused.

Every kind has a directory of its own except Aggregate, Release and Manifest: an
entity of any other kind is refused when another entity sits in its directory,
before anything is pinned or bundled, and the refusal names the entity in the way.
The directory's name is not judged.

A Component, Feature, Recipe or FeatureRecipe is read the way a build reads it
before it is bundled, and one that read refuses is refused here for the same
reason: a document that fails its kind's schema, a path naming content outside
the entity's own directory, which would not travel with the entity, or a schema
a Component or Feature imports that cannot be read or used.

A Plugin is read against its kind's schema too, before its .wasm is read, so a
metadata.version that is not a semantic version is refused. Its .wasm must then
agree with the entity on what the plugin offers and on its version, and a .wasm
whose version says it was built from a working tree with uncommitted changes —
the -dirty mark a build adds — is refused. A version disagreement and a build
from uncommitted changes are both cleared by building the plugin from a clean
checkout of the commit its release tag names, never by copying the version it
reports into the entity. A development version such as 1.0.0-3-g1a2b3c4, built
from a commit, and a prerelease such as 1.0.0-rc.1 publish.

What is published is the entity document and the files the entity itself names
beside it — a Recipe's or FeatureRecipe's local source, each file an $import:
names, the entity.lock beside it, and each schema file a Component or Feature
imports — and nothing else in its directory. An entity that names nothing
publishes as its document, unchanged; one that names a file publishes as a
gzipped archive of the document and those files. A directory it names travels
with every file under it that the ignore rules recorded in its entity.lock keep:
the rules the plugin serving its capability declared, and the author's own
.aggregaignore beside it, composed when the entity was pinned. A symbolic link
among those files is refused rather than followed. A local source written as a
template names no one directory until a Release fills it in, so an entity with
one carries every such file in its own directory, and push says so.

Push warns about each file an archive carries whose path holds a part Windows
cannot hold as written: a name Windows reserves for a device — CON, PRN, AUX,
NUL, COM1 or LPT1, say, in any letter case and whatever extension follows — or a
part ending in a full stop or a space. On Windows, aggrega refuses to unpack
such a file, so a Windows workspace cannot use the version. The warning is
written to standard error on every platform, one line per file, the entity
document's own file name judged with the rest, and it refuses nothing: the push,
or its dry run, goes ahead or stops exactly as it would without it. An entity
that names nothing publishes as its document alone and is never warned about.

Each file travels with mode 0644, whatever its permission bits — the execute
bit included — in the checkout it is published from. The OCI manifest names the
digest of the published bytes, so "aggrega pull" refuses a download that does
not match it. The lockfile a pull writes records the entity by the digest of its
content — each file's path and its bytes — which is the digest a pull in the
author's own workspace records for the same entity in its catalog directory, so
a build on either side refuses content that no longer matches.

Push signs before it publishes: where signing applies, the signature is made
over the manifest digest before any bytes reach the registry, so a failed
signing refuses the push rather than leaving unsigned content published.

The manifest's creation time is pinned, so the digest depends only on what is
published and never on when. Push states when it published beside the manifest
instead, as a small published-at attachment carrying this machine's clock, in
UTC to the second — unless the signature it attached already carries a time a
transparency log attests, as a keyless signature does. So a keyed or unsigned
publication writes one, and a keyless one does not. The attachment moves no
digest and changes no tag. "aggrega registry update" records the date, with
which of the two it came from.

Push refuses to replace content already published at the same version:
publish changed content under a new version instead.

A Recipe or FeatureRecipe carries the pins for its own inputs in the entity.lock
beside it, and pinning them is a step you run before publishing: aggrega pin.
A push that does not run that step refuses an entity whose entity.lock is
missing a pin, or holds one an input no longer matches, and names the step.
Pass --pin-sources, or set pin_sources_at_push in the workspace configuration, to
have push run that same step first, so the lock it writes is part of what is
published; --pin-sources=false skips it for one run.`,
		Example: `  # Publish to the workspace's default registry
  aggrega push catalog/components/api-server.yaml

  # See what would be published, without the network call
  aggrega push catalog/components/api-server.yaml --dry-run

  # Publish to a specific registry and sign the artifact
  aggrega push catalog/recipes/api-server.yaml --registry registry.example.com --sign

  # Which entity kinds this binary publishes
  aggrega push --list-kinds`,
		Args: func(cmd *cobra.Command, args []string) error {
			// --list-kinds answers a question about the binary, not about an
			// entity, so naming one alongside it is refused rather than
			// ignored: a silently-dropped path reads as a push that happened.
			if opts.listKinds {
				return cobra.NoArgs(cmd, args)
			}
			return cobra.MaximumNArgs(1)(cmd, args)
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			// Answered before the workspace is resolved: the kind set is a
			// property of the binary, and resolution both reads the config
			// cascade and reports its warnings on stderr — neither belongs in
			// the path of a query a script parses.
			if opts.listKinds {
				return listPublishableKinds(cmd.OutOrStdout())
			}
			path := opts.entityPath
			if path == "" && len(args) > 0 {
				path = args[0]
			}
			rw, err := workspaceForSubject(cmd.Context(), path)
			if err != nil {
				return err
			}
			pc := pushConfig{
				EntityPath:   path,
				Registry:     opts.registry,
				DryRun:       opts.dryRun,
				KeyPath:      opts.keyPath,
				FulcioURL:    opts.fulcio,
				RekorURL:     opts.rekor,
				TimestampURL: opts.timestamp,
				IDToken:      opts.idToken,
				PluginPath:   opts.pluginPath,
				PinSources:   explicitBool(cmd, "pin-sources", opts.pinSources),
				Workspace:    rw,
				Out:          cmd.OutOrStdout(),
				Advisories:   cmd.ErrOrStderr(),
			}
			// --sign is tri-state: when the operator passes it explicitly we
			// honour the value; otherwise the registry policy decides whether
			// to sign by default.
			if cmd.Flags().Changed("sign") {
				v := opts.sign
				pc.Sign = &v
			}
			return pushRun(cmd.Context(), pc)
		},
	}
	c.Flags().StringVar(&opts.entityPath, "entity", "", "Path to the entity YAML to publish (alternatively pass as the positional arg)")
	c.Flags().StringVar(&opts.registry, "registry", "", "Destination registry host (overrides the workspace's default_registry)")
	c.Flags().BoolVar(&opts.dryRun, "dry-run", false, "Compute the bundle and print the proposed <reference>:<tag> with the digest a publication would land, without contacting the registry. An entity the publication would refuse for a missing pin is refused here too")
	c.Flags().BoolVar(&opts.sign, "sign", false, "Sign the published artifact and attach the signature as an OCI referrer. When omitted, signing follows the destination registry's policy.")
	c.Flags().StringVar(&opts.keyPath, "key", "", "Path to a held ECDSA P-256 private key PEM for offline keyed signing. When omitted, keyless signing is used.")
	c.Flags().StringVar(&opts.fulcio, "fulcio-url", "", "Fulcio certificate-authority URL for keyless signing. When omitted, the public-good Fulcio instance is used.")
	c.Flags().StringVar(&opts.rekor, "rekor-url", "", "Rekor transparency-log URL for keyless signing. When omitted, the public-good Rekor instance is used.")
	c.Flags().StringVar(&opts.timestamp, "timestamp-url", "", "Optional RFC3161 timestamp-authority URL for keyless signing.")
	c.Flags().StringVar(&opts.idToken, "identity-token", "", "OIDC identity token for keyless signing. Optional at an interactive terminal (a browser flow acquires one) and on GitHub Actions (the workflow OIDC token is minted automatically); required for other non-interactive/CI use, where it may instead be exported as AGGREGA_IDENTITY_TOKEN or SIGSTORE_ID_TOKEN.")
	c.Flags().StringVar(&opts.pluginPath, "plugin", "", "Path to the local .wasm artifact for a Plugin entity. Required when publishing a Plugin: publish pushes the .wasm artifact, computes its content digest, and pushes the entity.")
	c.Flags().BoolVar(&opts.listKinds, "list-kinds", false, "List every entity kind that can be published, then exit without publishing anything. The output is machine-readable and its shape is stable: one kind per line, in sorted order, with nothing else.")
	c.Flags().BoolVar(&opts.pinSources, "pin-sources", false, helpPushPinSources)
	return c
}

// listPublishableKinds writes each publishable entity kind on its own line. The
// enumeration and its ordering belong to the publication layer; this is the
// command's route to it.
func listPublishableKinds(out io.Writer) error {
	return publishops.ListPublishableKinds(out)
}

// pushRun is the command line's adapter onto the publication operation. It fills
// the seams whose production value belongs to a terminal — the interactive
// identity rung and the terminal check — and names the command in any failure.
//
// Both halves are what a surface adapter is for. The operation itself defaults to
// no interactive rung at all, because most surfaces have nobody watching; the
// command line is the surface that does, so it supplies them here. And the
// operation's diagnostics carry no command name, because a caller elsewhere never
// typed one — so the name is added at this boundary, where it is true.
//
// The facts the operation reports are discarded here, and that is the command
// line's own shape rather than an oversight: the operation writes its own account
// of the publication to its status output, and its warnings to its advisory output,
// and between them they name every fact the result carries. The reference and tag,
// the digest, and whether the bytes were already there or nothing was published at
// all are the identity of the line a person is shown; the kind, the media type and
// the byte counts follow it as its continuation lines; a signature that was
// attached says so on its own line; and each entry Windows cannot use is named on
// a warning line of its own. A second rendering here would restate in full an
// account the terminal has just been given, which is a duplicate a reader has to
// reconcile rather than a fact they were missing.
//
// The two are still not the same thing, and neither substitutes for the other:
// the prose is written for a person watching, and a surface that answers a caller
// reads the result, because re-deriving these values by parsing the prose would
// turn a display format into a contract. What makes the discard honest here is
// that this surface IS the person watching.
func pushRun(ctx context.Context, cfg pushConfig) error {
	cfg.TokenGetter = signInGetter(ctx, cfg.TokenGetter)
	cfg.IsTTY = ttyOrDefault(cfg.IsTTY)
	cfg.EnvLookup = envLookupOrGetenv(cfg.EnvLookup)
	cfg.IdentitySupply = commandLineIdentitySupply
	cfg.DestinationSupply = commandLineDestinationSupply
	cfg.PinSourcesSupply = commandLinePinSourcesSupply
	// The catalog's notices about the files the pin step reads go to this
	// command's error stream, beside its warnings.
	if cfg.Notices == nil {
		cfg.Notices = os.Stderr
	}
	// projection-omits PushResult: the publication writes an account of itself to this surface's own status output, and its warnings to this surface's error stream, and between them they name every fact the result carries, so projecting it here would restate in full what the terminal has just been shown
	if _, err := publishops.Push(ctx, cfg); err != nil {
		return fmt.Errorf("aggrega push: %w", err)
	}
	return nil
}

// buildPushReference composes the canonical OCI repository reference for a
// catalog entity. The rule belongs to the publication layer, which publishes
// through it; this is the command package's route to the same rule, so a test
// pinning the round trip against the consume-side parser reads one construction.
func buildPushReference(registry, kind, provider, metadataName string) string {
	return publishops.BuildPushReference(registry, kind, provider, metadataName)
}
