// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"
	"oras.land/oras-go/v2/registry/remote/auth"
	"oras.land/oras-go/v2/registry/remote/credentials"

	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

func newLoginCmd() *cobra.Command {
	var username string
	var passwordStdin bool
	var skipVerify bool
	c := &cobra.Command{
		Use:   "login <registry>",
		Short: "Authenticate to an OCI registry and store the credential",
		Long: "Store a username/password credential for a registry so subsequent push, pull, and " +
			"lint --pre-publish authenticate transparently. The credential is written to the shared " +
			"OCI credential store (the same store those commands read), honoring a configured " +
			"credential helper or OS keychain when present. No external login tool is required.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			rw, err := workspaceFromContext(cmd.Context())
			if err != nil {
				return err
			}
			return loginRun(cmd.Context(), loginConfig{
				Registry:      args[0],
				Username:      username,
				PasswordStdin: passwordStdin,
				SkipVerify:    skipVerify,
				ws:            rw,
			})
		},
	}
	c.Flags().StringVarP(&username, "username", "u", "", "Registry username (prompted if omitted)")
	c.Flags().BoolVar(&passwordStdin, "password-stdin", false, "Read the password from stdin (requires --username; for non-interactive use)")
	c.Flags().BoolVar(&skipVerify, "skip-verify", false, "Store the credential without first verifying it against the registry")
	return c
}

// loginConfig is the input contract for the login workflow, populated by the
// cobra command from its flags. Test bodies set Store/In/Out to inject an
// isolated credential store and I/O; nil means production (the shared Docker
// credential store and os.Stdin/os.Stdout).
type loginConfig struct {
	Registry      string
	Username      string
	PasswordStdin bool
	SkipVerify    bool

	// ws is the resolved workspace, threaded in by the command's RunE. It sources
	// the network-egress client for the credential-verification ping; an offline
	// workspace refuses the ping loudly (use --skip-verify to store a credential
	// without contacting the registry). Nil only when a test invokes loginRun
	// directly with SkipVerify set.
	ws *workspaceconfig.ResolvedWorkspace

	Store credentials.Store // nil → NewStoreFromDocker (AllowPlaintextPut)
	In    io.Reader         // nil → os.Stdin (username prompt / --password-stdin)
	Out   io.Writer         // nil → os.Stdout (prompts and messages)

	// EnvLookup reads the environment for the password secret. Nil means
	// production (os.Getenv); tests inject a frozen lookup so they stay
	// race-free under t.Parallel.
	EnvLookup func(string) string
}

// loginRun resolves the username and password (from flags, stdin, or
// interactive prompts), optionally verifies the credential against the
// registry, then stores it in the shared OCI credential store.
//
// AllowPlaintextPut is enabled so login works on a machine without a configured
// native credential helper, matching the behaviour of a standard OCI login: the
// credential is written to the standard Docker config file. When a credential
// helper or OS keychain IS configured, the store delegates to it. For at-rest
// security, configuring a helper is recommended.
//
// ctx belongs to the caller and governs both steps that leave this process: the
// verification round trip to the registry, and the write to the credential
// store. Ending it ends the round trip in flight rather than waiting for it, and
// stops the write from happening at all.
//
// The two steps carry the stop differently, and the difference is worth stating
// because it decides where the check sits. Verification is an ordinary request
// and ends with the context. The store honours a context only when it delegates
// to a credential helper; the plaintext configuration-file path it falls back to
// accepts a context and ignores it, so the stop is carried to that path by the
// check this function makes immediately before handing the credential over.
//
// Stopping cannot leave a partial credential behind, and the ordering is what
// guarantees that rather than any cleanup step. Verification happens before
// anything is written, so a run stopped during it stores nothing at all. The
// write that follows is a single handoff — to a credential helper as one
// exchange, or to the configuration file as a write to a temporary file that is
// then renamed over the old one — so the stored state afterwards is either
// exactly what was there before or exactly the new credential, never a readable
// mixture of the two. A run stopped before the write therefore reports that it
// did not store rather than leaving a credential that looks good and is not.
func loginRun(ctx context.Context, cfg loginConfig) error {
	if cfg.Registry == "" {
		return fmt.Errorf("aggrega login: registry argument required")
	}
	in := cfg.In
	if in == nil {
		in = os.Stdin
	}
	out := cfg.Out
	if out == nil {
		out = os.Stdout
	}

	username := cfg.Username
	if username == "" {
		if cfg.PasswordStdin {
			return fmt.Errorf("aggrega login: --username is required with --password-stdin")
		}
		fmt.Fprint(out, "Username: ")
		s := bufio.NewScanner(in)
		if s.Scan() {
			username = strings.TrimSpace(s.Text())
		}
		if username == "" {
			return fmt.Errorf("aggrega login: username required")
		}
	}

	// Password precedence: --password-stdin (most secure) > the environment
	// variable (a non-interactive CI convenience, readable by child processes,
	// so stdin is preferred) > the interactive prompt.
	var password string
	switch {
	case cfg.PasswordStdin:
		data, err := io.ReadAll(in)
		if err != nil {
			return fmt.Errorf("aggrega login: read password from stdin: %w", err)
		}
		// Accept a single trailing newline (echo "..." | ...) without making it
		// part of the secret.
		password = strings.TrimRight(string(data), "\r\n")
	default:
		if envPassword := envLookupOrGetenv(cfg.EnvLookup)(envRegistryPassword); envPassword != "" {
			password = envPassword
			break
		}
		fmt.Fprint(out, "Password: ")
		b, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Fprintln(out)
		if err != nil {
			return fmt.Errorf("aggrega login: read password: %w", err)
		}
		password = string(b)
	}
	if password == "" {
		return fmt.Errorf("aggrega login: password required")
	}

	cred := auth.Credential{Username: username, Password: password}

	if !cfg.SkipVerify {
		rw, werr := workspaceOrResolve(cfg.ws)
		if werr != nil {
			return fmt.Errorf("aggrega login: %w", werr)
		}
		httpClient, err := workspaceEgressClient(rw.Config())
		if err != nil {
			return fmt.Errorf("aggrega login: %s: %w (use --skip-verify to store the credential without contacting the registry)", cfg.Registry, err)
		}
		if err := oci.VerifyCredential(ctx, cfg.Registry, cred, httpClient); err != nil {
			return fmt.Errorf("aggrega login: %s: %w", cfg.Registry, err)
		}
	}

	store := cfg.Store
	if store == nil {
		s, err := credentials.NewStoreFromDocker(credentials.StoreOptions{AllowPlaintextPut: true})
		if err != nil {
			return fmt.Errorf("aggrega login: credential store: %w", err)
		}
		store = s
	}
	// Checked immediately before the write rather than only on entry, because
	// everything above it — the prompts, the workspace resolution, the round trip
	// to the registry — is time in which the operator can give up, and the write
	// below is the part that outlives the command.
	//
	// It is load-bearing rather than an economy. The shared store honours a
	// context only when it delegates to a credential helper; the plaintext
	// configuration-file path it falls back to takes a context and ignores it, so
	// on a machine with no helper configured the write would go ahead after the
	// operator had already stopped the command. This check is what carries the
	// stop to that path.
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("aggrega login: %s: %w", cfg.Registry, err)
	}
	if err := store.Put(ctx, cfg.Registry, cred); err != nil {
		return fmt.Errorf("aggrega login: store credential for %s: %w", cfg.Registry, err)
	}

	fmt.Fprintf(out, "Login succeeded for %s\n", cfg.Registry)
	return nil
}
