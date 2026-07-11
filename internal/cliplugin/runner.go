// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package cliplugin runs an installed CLI plugin as a terminal command: it
// resolves the plugin's bytes through the one resolution cascade, loads them
// under the runtime CLI instantiation profile (with the streaming host functions
// bound), invokes the guest's cli_run export with the invocation's argument
// vector, streams the guest's stdout and stderr straight to the terminal, and
// returns the guest's exit code as the command's status.
//
// The runner is a generic dispatcher: it holds zero command-specific logic. It
// does not know what any particular CLI plugin does, does not branch on the
// plugin's name or argument content, and does not interpret the guest's output.
// Every command's behaviour lives in its Wasm guest; the host only routes the
// argument vector in and the streamed output plus exit code out.
package cliplugin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"

	abi "github.com/aggrega-ai/aggrega-abi-go/pkg/abi"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
	"github.com/aggrega-ai/aggrega-cli/pkg/pluginpath"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
)

// ErrPluginNotCLI is returned when a resolved plugin is loaded to be run as a
// command but its describe() reports a non-CLI plugin type — for example a
// provider or policy-validator plugin, which the build pipeline dispatches but
// which has no terminal command form. The error names the plugin and its actual
// type.
var ErrPluginNotCLI = errors.New("cliplugin: plugin is not a CLI plugin")

// ErrNoResolver is returned when Run is invoked on a Runner with no Resolve seam
// set, so there is no way to turn the plugin URI into bytes.
var ErrNoResolver = errors.New("cliplugin: no plugin resolver configured")

// Runner dispatches an installed CLI plugin. It composes plugin-URI resolution
// with a runtime-profile Wasm load and invoke; it carries no per-command state,
// so one Runner serves every command invocation.
type Runner struct {
	// Resolve turns a plugin URI into its resolved bytes through the resolution
	// cascade (typically a build-configured pluginpath.Resolver's Resolve method
	// value). Required — a nil Resolve makes Run return ErrNoResolver.
	Resolve pluginpath.Lookup

	// Loader loads the resolved bytes under the runtime CLI profile. A nil
	// Loader defaults to one built through the shared caching constructor, so it
	// participates in the process-wide compilation cache.
	Loader *wasm.Loader

	// Stdout and Stderr receive the guest's streamed output. A nil writer
	// defaults to the process's os.Stdout / os.Stderr; tests inject buffers.
	Stdout io.Writer
	Stderr io.Writer
}

// Run resolves the plugin URI, loads it under the runtime CLI profile, invokes
// its cli_run export with argv, streams the guest's output to the Runner's
// stdout/stderr, and returns the guest's exit code.
//
// The returned exit code is the command's status, exactly as the guest reported
// it: a non-zero code is an ordinary command result (the shell's $?), NOT a Go
// error. Run returns a non-nil error only for a host-side failure — the URI does
// not resolve, the bytes are not a loadable CLI plugin, the resolved plugin is
// not of CLI type (ErrPluginNotCLI), or the guest traps or panics mid-run. In
// every error case the returned exit code is zero and the caller distinguishes
// "the command ran and exited non-zero" (err == nil, code != 0) from "the
// command could not be run" (err != nil).
func (r Runner) Run(ctx context.Context, uri string, argv []string) (int, error) {
	if r.Resolve == nil {
		return 0, ErrNoResolver
	}
	stdout := r.Stdout
	if stdout == nil {
		stdout = os.Stdout
	}
	stderr := r.Stderr
	if stderr == nil {
		stderr = os.Stderr
	}
	loader := r.Loader
	if loader == nil {
		// Build the default loader through the sanctioned constructor so it
		// shares the process-wide compilation cache. A CLI run needs no
		// external-input service (a guest fetch receives the not-available
		// envelope), so nil is passed for it.
		loader = wasm.NewCachingLoaderFromContext(ctx, nil)
	}

	resolved, err := r.Resolve(uri)
	if err != nil {
		return 0, err
	}
	spec := resolver.PluginSpec(uri, resolved)

	plugin, err := loader.LoadCLI(ctx, spec, stdout, stderr)
	if err != nil {
		return 0, err
	}

	// DescribeAndValidate Closes the plugin itself on any rejection, so the
	// deferred Close is registered only after it succeeds and leaves the plugin
	// live. Close is safe to call once; a single deferred Close covers every
	// exit path below.
	desc, err := loader.DescribeAndValidate(ctx, plugin, spec)
	if err != nil {
		return 0, err
	}
	defer func() { _ = plugin.Close(ctx) }()

	if desc.PluginType != wasm.PluginTypeCLI {
		return 0, fmt.Errorf("cliplugin: plugin %q reports type %q, not %q: %w",
			uri, desc.PluginType, wasm.PluginTypeCLI, ErrPluginNotCLI)
	}

	payload, err := json.Marshal(abi.CLIRunInput{Args: argv})
	if err != nil {
		return 0, fmt.Errorf("cliplugin: encode cli_run input for plugin %q: %w", uri, err)
	}

	// The guest streams its stdout/stderr through the host functions bound at
	// LoadCLI, so by the time cli_run returns the output has already reached the
	// injected writers; the host reads only the exit code here.
	exitCode, _, err := wasm.InvokeExportStatus(ctx, plugin, desc, wasm.CLIRunExportName, payload)
	if err != nil {
		return 0, err
	}
	return int(exitCode), nil
}
