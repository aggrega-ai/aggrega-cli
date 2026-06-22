// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/aggrega-ai/aggrega-cli/pkg/registryhost"
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

	c := &cobra.Command{
		Use:   "add <host>",
		Short: "Register a registry and its signature trust policy in the workspace config",
		Long: `Register a registry host and its signature trust policy in the workspace config file.

A trust policy is either keyless (one or more --identity entries, each an
"identity=issuer" pair) or keyed (--key pointing at a pinned public-key PEM).
Pass --trusted-root to pin a Sigstore trust-root bundle for air-gapped keyless
verification. Adding the official hub host with no policy flags pins the built-in
official baseline.

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
				ws:                rw,
				Out:               cmd.OutOrStdout(),
			})
		},
	}
	c.Flags().StringArrayVar(&identities, "identity", nil, "Trusted keyless identity as `identity=issuer` (repeatable; OR-set). E.g. dev@example.com=https://accounts.google.com")
	c.Flags().StringVar(&keyPath, "key", "", "Path to a pinned cosign-compatible public-key PEM for keyed verification")
	c.Flags().StringVar(&trustedRoot, "trusted-root", "", "Path to a pinned Sigstore trust-root bundle for air-gapped keyless verification")
	c.Flags().BoolVar(&requireSignatures, "require-signatures", false, "Require a signature on every artifact pulled from (and pushed to) this registry")
	c.Flags().BoolVar(&advisory, "advisory", false, "Downgrade verification failures for this registry to warnings instead of halting")
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

	ws  *workspaceconfig.ResolvedWorkspace
	Out interface{ Write([]byte) (int, error) }
}

// registryAddRun assembles a RegistryConfig from the flags and upserts it
// into the workspace config file at the resolved workspace root.
func registryAddRun(cfg registryAddConfig) error {
	rw, err := workspaceOrResolve(cfg.ws)
	if err != nil {
		return fmt.Errorf("workspaceconfig: %w", err)
	}
	host := registryhost.Of(cfg.Host)
	if host == "" {
		return fmt.Errorf("aggrega registry add: a registry host is required")
	}

	reg := workspaceconfig.RegistryConfig{Host: host}

	// Adding the official hub with no explicit policy pins the embedded
	// baseline (the maintainer + CI OR-set, signatures required).
	if host == workspaceconfig.HubBaselineHost && len(cfg.Identities) == 0 && cfg.KeyPath == "" {
		reg = workspaceconfig.DefaultBaseline()
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
