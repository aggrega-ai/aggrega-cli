// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/internal/pluginwire"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
	"github.com/aggrega-ai/aggrega-cli/pkg/plugintype"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/validator/envelope"
)

// newPluginRunLocalCmd builds the `plugin run-local` subcommand, which loads a
// .wasm guest from a filesystem path, calls one of its declared exports, and
// writes the guest's response bytes to stdout. It is the direct-invocation path
// for iterating on a plugin during development: the guest is addressed by its
// --plugin path, not resolved through the workspace plugin cascade.
//
// It has two forms. The artifact form takes no positional argument: the guest
// is loaded bare, and an optional --type imposes a type expectation the guest's
// describe() must satisfy. The entity form takes a Plugin entity file as its one
// positional argument: the entity is schema-validated, and its declared type and
// export contract are cross-checked against the guest's describe() before the
// guest runs. The guest bytes always come from --plugin in both forms; the
// entity carries no artifact path and drives no registry or identity resolution.
// --type is rejected in the entity form because the entity supplies the type.
//
// The guest's clean exit code becomes the command's exit status — a non-zero
// exit is an ordinary command result, not a host error — so a caller can read
// $? exactly as it would for any other program. A Wasm trap or a recovered
// guest panic surfaces as a normal command error instead.
func newPluginRunLocalCmd() *cobra.Command {
	var (
		pluginPath string
		export     string
		with       string
		typeFlag   string
	)
	c := &cobra.Command{
		Use:   "run-local [plugin.yaml]",
		Short: "Load a local .wasm guest and call one of its exports",
		Long: `Load a .wasm guest from a filesystem path, call one of its declared
exports with an optional payload, and write the guest's response bytes to
stdout.

The guest is addressed directly by --plugin path; it is not resolved through
the workspace plugin cascade. The export named by --export must be one the
guest advertises in its describe() self-report, or the call is rejected before
the guest runs.

The command has two forms. The artifact form takes no positional argument and
loads the guest bare. The entity form takes a Plugin entity file as its one
positional argument: the entity is schema-validated, and its declared
metadata.pluginType and export contract are cross-checked against the guest's
describe() before the guest runs. In both forms the guest bytes come from
--plugin; the entity carries no artifact path.

The payload is supplied with --with, and it is always written as JSON. A value
beginning with @ reads the payload bytes from a file (--with @input.json); any
other value is taken as a literal inline JSON payload (--with '{"k":1}'). When
--with is omitted, the export receives the minimal empty JSON object {}. The
resolved payload is validated as JSON before the guest runs, so malformed input
is reported up front.

Some exports read their payload in a binary encoding rather than as JSON. Which
encoding an export reads is fixed by the socket that export serves, so no flag
selects it: the resolved payload is converted into whatever the named export
reads before the guest is called. The authored payload stays JSON either way.

In the artifact form, --type optionally names a plugin type the guest's
describe() self-report must agree with, or the load is rejected before the
guest runs; when omitted, no type expectation is imposed. In the entity form
--type is rejected, because the entity's metadata.pluginType supplies the type.

The guest's clean exit code becomes this command's exit status, so a failure
inside the plugin is distinguishable from a failure of aggrega itself.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()

			payload, err := resolveWithPayload(with)
			if err != nil {
				return err
			}
			// The authoring surface is text; the wire is whatever the named export
			// carries. A text-wire export gets the author's bytes unchanged; a
			// binary-wire export gets them read into that export's payload shape and
			// re-emitted in the encoding the guest reads. Nobody hand-writes those
			// bytes, so the conversion belongs here rather than on the operator.
			payload, err = pluginwire.EncodeForExport(export, payload)
			if err != nil {
				return err
			}

			spec := wasm.PluginSpec{
				Path: pluginPath,
				Name: filepath.Base(pluginPath),
			}

			if len(args) == 1 {
				// Entity form: the positional argument is a Plugin entity whose
				// declared type and export contract gate the guest at load. --type
				// would be a second, conflicting source of the type, so reject it.
				if cmd.Flags().Changed("type") {
					return fmt.Errorf("--type cannot be combined with a plugin.yaml argument; the entity's metadata.pluginType supplies the type")
				}
				if err := applyPluginEntitySpec(&spec, args[0]); err != nil {
					return err
				}
			} else if typeFlag != "" {
				if _, ok := plugintype.Lookup(plugintype.Type(typeFlag)); !ok {
					return fmt.Errorf("--type %q is not a known plugin type", typeFlag)
				}
				spec.PluginType = plugintype.Type(typeFlag)
			}

			loader := wasm.NewCachingLoaderFromContext(ctx, nil)
			// Real-execution load: run-local actually invokes a guest export via
			// InvokeExportStatus below, so it needs the runnable plugin and real
			// host functions that LoadAndDescribe binds — not the describe-only
			// introspection path.
			plugin, desc, err := loader.LoadAndDescribe(ctx, spec)
			if err != nil {
				return err
			}
			defer plugin.Close(ctx)

			if err := ensureExportDeclared(desc, export); err != nil {
				return err
			}

			exitCode, out, err := wasm.InvokeExportStatus(ctx, plugin, desc, export, payload)
			if err != nil {
				return err
			}

			if _, err := cmd.OutOrStdout().Write(out); err != nil {
				return err
			}

			if exitCode != 0 {
				return exitCodeError{code: int(exitCode)}
			}
			return nil
		},
	}
	c.Flags().StringVar(&pluginPath, "plugin", "", "filesystem path to the .wasm artifact")
	c.Flags().StringVar(&export, "export", "", "the declared export to call")
	c.Flags().StringVar(&with, "with", "", "payload for the export: inline JSON, or @<path> to read from a file; defaults to {} when omitted")
	c.Flags().StringVar(&typeFlag, "type", "", "the plugin type the guest's describe() self-report must match")
	_ = c.MarkFlagRequired("plugin")
	_ = c.MarkFlagRequired("export")
	return c
}

// applyPluginEntitySpec populates the load spec from a Plugin entity file for
// the entity form. It reads the file, validates it against the Plugin schema,
// parses it, and sets the load gate's two entity-driven checks: the declared
// metadata.pluginType (which the guest's describe() type must equal) and the
// authored export contract (which the guest's describe() must satisfy —
// entity-claim ⊆ describe(); a nil claim leaves the cross-check a no-op). The
// guest bytes still come from --plugin (spec.Path is left untouched); the entity
// supplies only its declared identity for diagnostics and its contract to gate
// against. A schema violation or parse failure is returned unwrapped.
func applyPluginEntitySpec(spec *wasm.PluginSpec, entityPath string) error {
	data, err := os.ReadFile(entityPath)
	if err != nil {
		return fmt.Errorf("reading plugin entity: %w", err)
	}
	if _, err := envelope.ValidateBytes(envelope.KindPlugin, data, filepath.Base(entityPath)); err != nil {
		return err
	}
	var plugin types.Plugin
	if err := yaml.Unmarshal(data, &plugin); err != nil {
		return fmt.Errorf("parsing plugin entity: %w", err)
	}
	spec.PluginType = plugintype.Type(plugin.Metadata.PluginType)
	spec.Contract = resolver.WasmContractClaim(resolver.PluginContractClaim(plugin))
	// The entity's declared name is the more identifying diagnostic label than the
	// artifact filename, since the entity is what is being gated against the guest.
	spec.Name = plugin.Metadata.Name
	return nil
}

// ensureExportDeclared reports whether the descriptor advertises the requested
// export before the guest is invoked. When it does not, the returned error names
// the requested export and enumerates the exports the guest does declare, in the
// descriptor's own order, so the caller sees which names are actually callable.
func ensureExportDeclared(desc wasm.PluginDescriptor, export string) error {
	names := make([]string, len(desc.Exports))
	for i, e := range desc.Exports {
		names[i] = e.Name
		if e.Name == export {
			return nil
		}
	}
	return fmt.Errorf("the guest does not declare export %q; it declares: %s", export, strings.Join(names, ", "))
}

// resolveWithPayload resolves the --with value to the authored JSON payload
// bytes. What finally reaches the guest is those bytes converted into the
// encoding the named export reads, which is a separate step: this one is about
// the authoring surface, which is JSON for every export.
//
// It has three forms. A value beginning with @ is a file reference: the leading @
// is stripped and the remaining path is read from disk. Any other non-empty value
// is taken literally as an inline JSON payload. An omitted value (the empty
// string) defaults to the minimal empty JSON object {}, a convenience for
// smoke-testing an export with no input.
//
// The empty default is deliberately socket-blind: it is the literal bytes {},
// independent of any plugin type and of any request envelope shape. It
// fabricates no intermediate representation and infers no per-socket semantics.
// The socket enters one step later, where the resolved bytes are converted into
// the encoding the export reads — so what a guest finally receives for the empty
// default is an empty payload in its own encoding, and a guest that cannot read
// an empty payload surfaces its own error normally.
//
// The resolved payload — inline, from file, or the default — is validated as
// well-formed JSON before it is returned, so malformed input is reported up
// front rather than reaching the guest as a decode trap. Validation lives here
// so both command forms resolve and check the payload identically.
func resolveWithPayload(with string) ([]byte, error) {
	var payload []byte
	switch {
	case with == "":
		payload = []byte("{}")
	case with[0] == '@':
		b, err := os.ReadFile(with[1:])
		if err != nil {
			return nil, fmt.Errorf("reading payload file: %w", err)
		}
		payload = b
	default:
		payload = []byte(with)
	}
	if !json.Valid(payload) {
		return nil, fmt.Errorf("--with payload is not valid JSON")
	}
	return payload, nil
}
