// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/aggrega-ai/aggrega-cli/pkg/registryhost"
	"github.com/aggrega-ai/aggrega-cli/pkg/registrytrust"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// newRegistryCmd builds the `registry` parent command grouping the
// registry-management subcommands.
func newRegistryCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "registry",
		Short: "Manage registries and their signature trust policy",
	}
	c.AddCommand(newRegistryAddCmd())
	return c
}

func newRegistryAddCmd() *cobra.Command {
	var identities []string
	var keyPath string
	var trustedRoot string
	var requireSignatures bool
	var advisory bool
	var fetchTrust bool

	c := &cobra.Command{
		Use:   "add <host>",
		Short: "Register a registry and its signature trust policy in the workspace config",
		Long: `Register a registry host and its signature trust policy in the workspace config file.

By default the registry's advertised trust policy is fetched once from the
registry's own origin (https://<host>/.well-known/aggrega-trust) and pinned
locally — trust-on-first-use. Verification at pull time always runs against the
pinned local policy, never a live re-fetch. Pass --no-fetch-trust to skip the
fetch and pin only what the flags supply.

A trust policy can also be set by hand: keyless (one or more --identity entries,
each an "identity=issuer" pair) or keyed (--key pointing at a pinned public-key
PEM). Manual --identity / --key flags OVERRIDE the fetched set — when either is
supplied, the fetch is skipped and the policy is built from the flags alone.
Pass --trusted-root to pin a Sigstore trust-root bundle for air-gapped keyless
verification. Adding the official hub host with no policy flags pins the built-in
official baseline.

When the registry advertises no trust policy (a 404 or an unreachable endpoint),
the add does not fail: it writes the policy from the flags (or an empty policy)
and prints a notice suggesting manual --identity / --key pinning.

The entry is written to the workspace config file, preserving existing settings.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			rw, err := workspaceFromContext(cmd.Context())
			if err != nil {
				return err
			}
			return registryAddRun(registryAddConfig{
				Host:              args[0],
				Identities:        identities,
				KeyPath:           keyPath,
				TrustedRoot:       trustedRoot,
				RequireSignatures: cmd.Flags().Changed("require-signatures"),
				RequireValue:      requireSignatures,
				Advisory:          advisory,
				FetchTrust:        fetchTrust,
				ws:                rw,
				Out:               cmd.OutOrStdout(),
			})
		},
	}
	c.Flags().StringArrayVar(&identities, "identity", nil, "Trusted keyless identity as `identity=issuer` (repeatable; OR-set). E.g. dev@example.com=https://accounts.google.com. Overrides the fetched trust policy.")
	c.Flags().StringVar(&keyPath, "key", "", "Path to a pinned cosign-compatible public-key PEM for keyed verification. Overrides the fetched trust policy.")
	c.Flags().StringVar(&trustedRoot, "trusted-root", "", "Path to a pinned Sigstore trust-root bundle for air-gapped keyless verification")
	c.Flags().BoolVar(&requireSignatures, "require-signatures", false, "Require a signature on every artifact pulled from (and pushed to) this registry")
	c.Flags().BoolVar(&advisory, "advisory", false, "Downgrade verification failures for this registry to warnings instead of halting")
	c.Flags().BoolVar(&fetchTrust, "fetch-trust", true, "Fetch and pin the registry's advertised trust policy on first use. Use --no-fetch-trust to skip; manual --identity/--key also skip the fetch.")
	return c
}

// registryAddConfig is the input contract for `aggrega registry add`,
// populated by the cobra command from its flags.
type registryAddConfig struct {
	Host        string
	Identities  []string
	KeyPath     string
	TrustedRoot string
	// RequireSignatures reports whether --require-signatures was passed;
	// RequireValue carries its value. For the hub baseline the requirement
	// is on by default even when the flag is absent.
	RequireSignatures bool
	RequireValue      bool
	Advisory          bool
	// FetchTrust enables the trust-on-first-use fetch: when true and no manual
	// --identity / --key is supplied, the registry's advertised trust policy is
	// fetched from its own origin and pinned locally. Manual flags override the
	// fetched set, so a manual policy skips the fetch regardless of this value.
	FetchTrust bool

	ws  *workspaceconfig.ResolvedWorkspace
	Out interface{ Write([]byte) (int, error) }
	// trustClient transports the well-known trust fetch. Nil selects a default
	// client in the fetch path; tests inject an httptest client so no live
	// network is contacted.
	trustClient *http.Client
}

// registryAddRun assembles a RegistryConfig for the host and upserts it into
// the workspace config file at the resolved workspace root.
//
// Trust-policy precedence:
//
//   - Manual flags win. When --identity or --key is supplied, the policy is
//     built from the flags alone and the advertised-trust fetch is skipped —
//     the operator has stated the trust set explicitly.
//   - Otherwise, trust-on-first-use. With FetchTrust set and no manual policy,
//     the registry's advertised trust document is fetched once from its own
//     origin and its identities pinned locally. This is the first-contact
//     decision; verification at pull time runs against this pinned copy, never
//     a live re-fetch.
//   - Graceful fallback. A registry that advertises nothing (a 404 or an
//     unreachable endpoint) is not an error: the add prints a notice and writes
//     an empty/permissive policy (or, for the hub host, the embedded baseline),
//     exactly as before trust-on-first-use existed.
func registryAddRun(cfg registryAddConfig) error {
	rw, err := workspaceOrResolve(cfg.ws)
	if err != nil {
		return fmt.Errorf("workspaceconfig: %w", err)
	}
	host := registryhost.Of(cfg.Host)
	if host == "" {
		return fmt.Errorf("aggrega registry add: a registry host is required")
	}

	manualPolicy := len(cfg.Identities) > 0 || cfg.KeyPath != ""

	reg := workspaceconfig.RegistryConfig{Host: host}

	// Adding the official hub with no explicit manual policy pins the embedded
	// baseline (the maintainer + CI OR-set, signatures required).
	if host == workspaceconfig.HubBaselineHost && !manualPolicy {
		reg = workspaceconfig.DefaultBaseline()
	}

	// Trust-on-first-use: fetch and pin the advertised policy unless a manual
	// policy overrides it. A manual --identity/--key skips the fetch entirely.
	if cfg.FetchTrust && !manualPolicy {
		if perr := pinAdvertisedTrust(&reg, host, cfg.trustClient, cfg.Out); perr != nil {
			return perr
		}
	}

	for _, spec := range cfg.Identities {
		id, issuer, ok := strings.Cut(spec, "=")
		if !ok || id == "" || issuer == "" {
			return fmt.Errorf("aggrega registry add: --identity %q must be in the form identity=issuer", spec)
		}
		reg.TrustedIdentities = append(reg.TrustedIdentities, workspaceconfig.TrustedIdentity{
			Identity: id,
			Issuer:   issuer,
		})
	}

	if cfg.KeyPath != "" {
		pem, rerr := os.ReadFile(cfg.KeyPath)
		if rerr != nil {
			return fmt.Errorf("aggrega registry add: read public key %s: %w", cfg.KeyPath, rerr)
		}
		reg.PublicKey = string(pem)
	}
	if cfg.TrustedRoot != "" {
		reg.TrustedRoot = cfg.TrustedRoot
	}
	if cfg.RequireSignatures {
		reg.RequiresSignatures = cfg.RequireValue
	}
	if cfg.Advisory {
		reg.Advisory = true
	}

	cfgPath := filepath.Join(rw.Root(), workspaceconfig.ConfigFilename)
	if err := workspaceconfig.UpsertRegistry(cfgPath, reg); err != nil {
		return err
	}
	if cfg.Out != nil {
		fmt.Fprintf(cfg.Out, "Registered registry %s in %s\n", host, cfgPath)
	}
	return nil
}

// pinAdvertisedTrust fetches host's advertised trust document and copies its
// fields onto reg. A registry that advertises nothing — a 404 (mapped to
// ErrNoTrustAdvertised) or an unreachable endpoint (a transport error) — is
// tolerated: it prints a notice and leaves reg's policy for the caller's
// manual flags (or the empty/baseline default) to fill, rather than failing
// the add. A MALFORMED advertised document (one that fetched but did not
// decode, ErrMalformedTrust) is a hard error, since the endpoint answered with
// content that is not a usable trust policy — a real misconfiguration the
// operator should see.
func pinAdvertisedTrust(reg *workspaceconfig.RegistryConfig, host string, client *http.Client, out interface{ Write([]byte) (int, error) }) error {
	adv, ferr := registrytrust.Fetch(context.Background(), client, host)
	if ferr != nil {
		if errors.Is(ferr, registrytrust.ErrMalformedTrust) {
			return fmt.Errorf("aggrega registry add: %w", ferr)
		}
		if out != nil {
			fmt.Fprintf(out, "Notice: registry %s advertises no trust config (%v); pin identities manually with --identity/--key\n", host, ferr)
		}
		return nil
	}

	reg.RequiresSignatures = adv.RequiresSignatures
	reg.TrustedIdentities = advertisedIdentities(adv.TrustedIdentities)
	reg.TrustedDeprecationIdentities = advertisedIdentities(adv.TrustedDeprecationIdentities)
	if adv.PublicKey != "" {
		reg.PublicKey = adv.PublicKey
	}
	if out != nil {
		fmt.Fprintf(out, "Pinned advertised trust policy from %s (%d identities)\n", host, len(reg.TrustedIdentities))
	}
	return nil
}

// advertisedIdentities converts the wire-shape identities from an advertised
// trust document into the per-registry config's identity type. The two shapes
// are field-identical; the copy keeps the config package unaware of the
// registrytrust wire type.
func advertisedIdentities(ids []registrytrust.AdvertisedIdentity) []workspaceconfig.TrustedIdentity {
	if len(ids) == 0 {
		return nil
	}
	out := make([]workspaceconfig.TrustedIdentity, 0, len(ids))
	for _, id := range ids {
		out = append(out, workspaceconfig.TrustedIdentity{
			Identity:       id.Identity,
			IdentityRegexp: id.IdentityRegexp,
			Issuer:         id.Issuer,
		})
	}
	return out
}
