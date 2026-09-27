// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"context"
	"errors"

	"github.com/aggrega-ai/aggrega-cli/internal/authoredcontent"
	"github.com/aggrega-ai/aggrega-cli/internal/configops"
	"github.com/aggrega-ai/aggrega-cli/internal/registryops"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// configInitParams is what aggrega/configInit takes.
//
// The spellings are the ones the command line uses, so a caller that learned the
// options on one surface knows them on the other. `global` and `workspace` are
// the write-target overrides, and sending both is refused exactly as the command
// line refuses it.
//
// registry is optional and an absent value seeds the official hub, which is the
// same default the command line's flag carries. A request that says nothing about
// the registry gets that default rather than a refusal, for the reason an absent
// trust-fetch key takes the command line's default: the two surfaces expose one
// capability, and a default that held on one and not the other would make the
// same request mean different things. The answer names the host it seeded and the
// file it wrote, so the default is legible in the response rather than assumed.
//
// dryRun does everything the plain request does except write: it answers with the
// seeded configuration as content instead of putting it on disk. Which of the two
// a caller wants is a property of the caller rather than of this surface — a
// client that keeps its own document model puts the content where the author can
// read it before it is saved — so it is a request field. It defaults to writing,
// because the operation's product is a configuration file and a surface that
// produces a document owes the document. It changes nothing else: an existing
// configuration file is refused exactly as a write refuses it, and force alongside
// it answers with the merge an overwriting write would produce.
type configInitParams struct {
	Registry  string `json:"registry"`
	Force     bool   `json:"force"`
	Global    bool   `json:"global"`
	Workspace bool   `json:"workspace"`
	DryRun    bool   `json:"dryRun"`
}

// configInitResult is the aggrega/configInit response.
//
// Written is what a client branches on: under a write it names a file now on disk
// that the client can open, and under a dry run it is false and Content carries
// the document instead. The two are never both meaningful — the file Content is
// for is Scope.ConfigFile, so nothing here carries a second copy of the path.
//
// Trust reports what the advertised-trust fetch found, as the three distinguished
// outcomes rather than as a line of prose. A caller deciding whether the
// configuration it just seeded is usable needs to know which, and a registry that
// advertises nothing is not a failure — the seeding proceeds and says so.
type configInitResult struct {
	Host    string                        `json:"host"`
	Scope   configWriteScope              `json:"scope"`
	Trust   registryops.TrustFetchOutcome `json:"trust"`
	Written bool                          `json:"written"`
	Content string                        `json:"content,omitempty"`
}

// dispatchConfigInit seeds a working configuration by registering a registry and
// its signature trust policy.
//
// 🔴 It WRITES, and by default the file it writes is the global one every
// workspace on this machine resolves through — so a seeding a session performs
// changes what resolves for projects this session has never heard of. The response
// says so on every answer rather than only where the caller asked for the global
// file, because the default is the machine-wide one and a caller that sent no
// scope flag is exactly the caller most likely to assume otherwise.
//
// It is carried here on the same rule every other writing method runs under: a
// caller that sent this has asked for the configuration to be seeded, exactly as
// a caller sending a build has asked for artifacts. What stays out of bounds is a
// write performed while validating or inspecting, where nobody asked, and this is
// not that.
//
// It refuses rather than overwrite an existing configuration file, so a session
// cannot replace a configuration somebody is working from by accident; a caller
// that means to sends force. Where it takes the registry's advertised trust
// policy it makes one unauthenticated request to that registry's own origin, and
// nothing is prompted for: a registry that advertises nothing is reported as
// advertising nothing and the seeding proceeds — on the built-in baseline for the
// official hub, on no policy for any other registry. Offline, seeding a registry
// other than the hub is refused, because this method takes no identities or key
// to pin by hand; the refusal is the registration's own sentence with its short
// code ahead of it.
func (s *Server) dispatchConfigInit(ctx context.Context, req *jsonRPCRequest) error {
	var params configInitParams
	if len(req.Params) > 0 {
		if err := decodeParams(req, &params); err != nil {
			return s.writeError(ctx, req.ID, jsonRPCInvalidParams, "invalid params: "+err.Error())
		}
	}
	if s.resolvedWorkspace == nil {
		return s.writeError(ctx, req.ID, jsonRPCInternalError,
			"the daemon holds no resolved workspace to resolve a configuration write target against")
	}

	// The two overrides are refused here, on this surface's own spellings, rather
	// than left to the shared write-target policy. That policy names the command
	// line's flags in its refusal, and a refusal naming a flag a caller never sent
	// tells them to fix something they cannot reach from here.
	if params.Global && params.Workspace {
		return s.writeError(ctx, req.ID, jsonRPCInvalidParams,
			"config init: global and workspace cannot be combined; send at most one, "+
				"or neither to write the configuration shared across every workspace on this machine")
	}

	// An absent registry takes the command line's own default rather than the zero
	// value, which the registration would refuse for naming no host.
	registry := params.Registry
	if registry == "" {
		registry = workspaceconfig.HubBaselineHost
	}

	delivery := authoredcontent.DeliveryFor(params.DryRun)
	res, err := configops.Init(ctx, s.resolvedWorkspace, configops.InitOptions{
		Registry:  registry,
		Force:     params.Force,
		Global:    params.Global,
		Workspace: params.Workspace,
		Delivery:  delivery,
	})
	if ctx.Err() != nil {
		return nil
	}
	if err != nil {
		// The refuse-if-exists condition is re-worded here, and only that one. The
		// shared layer's sentence names the command line's flag, which is the right
		// remedy at a terminal and unreachable from a request — so the condition is
		// recognised through its own type rather than by reading the sentence, and
		// the field a caller here can actually send is what the refusal names. The
		// file comes off that same value, so this names the file the guard protected
		// rather than resolving a write target a second time.
		if existing := (*configops.ConfigFileExistsError)(nil); errors.As(err, &existing) {
			return s.writeError(ctx, req.ID, jsonRPCInvalidParams,
				"config init: "+existing.Path()+" already exists and was left untouched; "+
					"send force to proceed anyway, which merges the registry entry into what is already there")
		}
		return s.writeError(ctx, req.ID, jsonRPCInvalidParams, registrationRefusal(err))
	}
	return s.writeResult(ctx, req.ID, configInitResult{
		Host:    res.Registration.Host,
		Scope:   s.scopeOfWriteTarget(res.Registration.Target),
		Trust:   res.Registration.Trust,
		Written: !delivery.Reports(),
		Content: string(res.Registration.Content),
	})
}

// configCaptureParams is what aggrega/configCapture takes.
//
// allPlugins widens the frozen socket set from the sockets that determine the
// rendered output — the provider bindings and the label-transform governance
// attachments — to every plugin socket, adding the verdict governance and linter
// gates and the import socket. It defaults to the narrower set, because only the
// output-determining sockets must travel with a project for its build to produce
// the same bytes elsewhere; the rest are meant to vary by environment.
//
// There is no write-target override, and its absence is the operation rather than
// an omission: freezing exists to make a workspace self-contained, so it always
// writes that workspace's own configuration file.
type configCaptureParams struct {
	AllPlugins bool `json:"allPlugins"`
}

// configCapturedAttachment is one frozen plugin attachment on the wire.
//
// 🔴 It is a projection rather than the configuration type itself, and
// deliberately so: that type is declared for the configuration document's own
// form and carries no field spellings for this protocol, so serializing it
// directly would answer with capitalised names where every other field on this
// surface is lowercase.
//
// It names the attachment and not the whole of it: an attachment's optional
// attachment-time inputs are frozen into the file verbatim and are not repeated
// here, on the same ground the command line's summary does not print them. The
// file the answer names is the authoritative copy, and a client wanting the full
// form opens it.
type configCapturedAttachment struct {
	URI        string `json:"uri,omitempty"`
	Capability string `json:"capability,omitempty"`
	Function   string `json:"function,omitempty"`
	Role       string `json:"role,omitempty"`
}

// configCapturedSocket is one plugin socket a freeze wrote, with the attachments
// it wrote there.
//
// Socket names the socket the attachments are bound at; Field is the key they
// were written under in the file. The two differ for the provider socket alone,
// which is written under a plural key, so both are carried rather than leaving a
// client to hold a mapping of its own.
type configCapturedSocket struct {
	Socket      string                     `json:"socket"`
	Field       string                     `json:"field"`
	Attachments []configCapturedAttachment `json:"attachments"`
}

// configCaptureResult is the aggrega/configCapture response.
//
// ConfigFile is the file that was written, so a client holding a document model
// can open exactly what the freeze produced. It is always this workspace's own
// configuration file, which is why this answer carries no machine-wide scope: the
// operation resolves no write target and reaches nothing outside the workspace
// the session opened.
//
// Captured is what separates a freeze that wrote something from one that found
// nothing build-critical. A workspace whose configuration a build does not
// participate in is a real answer rather than a failure: the file is left as it
// stands, both content fields are empty, and Captured is false.
//
// Sockets and CatalogDirs are always arrays, empty rather than absent, so a
// client reads a workspace with nothing to freeze as an empty answer rather than
// as a missing field.
type configCaptureResult struct {
	ConfigFile    string                 `json:"configFile"`
	WorkspaceRoot string                 `json:"workspaceRoot"`
	Captured      bool                   `json:"captured"`
	Sockets       []configCapturedSocket `json:"sockets"`
	CatalogDirs   []string               `json:"catalogDirs"`
}

// dispatchConfigCapture freezes the effective build-critical configuration into
// the workspace's own configuration file.
//
// 🔴 It WRITES, and what it writes is a file the author owns and goes on editing.
// That is admitted on the same rule the other writing methods run under — a caller
// that sent this has asked for the freeze — and the write is the operation rather
// than a step beside it: the answer names the file so the client can open what was
// written, which is the follow-up, not a substitute for writing it.
//
// Each field is written at its effective value, so a binding that today comes only
// from the machine's global configuration is materialized into the workspace and
// travels with the project. Machine-wide tooling settings are not build-critical
// and are left where they are, so this reaches nothing outside the workspace even
// though it reads a configuration assembled from outside it.
//
// It reaches no registry and prompts for nothing, and re-running it over an
// unchanged configuration rewrites the same bytes — so a client may offer it
// freely without a second call changing anything a reader would notice.
func (s *Server) dispatchConfigCapture(ctx context.Context, req *jsonRPCRequest) error {
	var params configCaptureParams
	if len(req.Params) > 0 {
		if err := decodeParams(req, &params); err != nil {
			return s.writeError(ctx, req.ID, jsonRPCInvalidParams, "invalid params: "+err.Error())
		}
	}
	if s.resolvedWorkspace == nil {
		return s.writeError(ctx, req.ID, jsonRPCInternalError,
			"the daemon holds no resolved workspace to freeze a configuration for")
	}

	res, err := configops.Capture(s.resolvedWorkspace, configops.CaptureOptions{
		AllPlugins: params.AllPlugins,
	})
	if ctx.Err() != nil {
		return nil
	}
	if err != nil {
		return s.writeError(ctx, req.ID, jsonRPCInternalError, err.Error())
	}

	sockets := make([]configCapturedSocket, 0, len(res.Sockets))
	for _, cs := range res.Sockets {
		attachments := make([]configCapturedAttachment, 0, len(cs.Refs))
		for _, ref := range cs.Refs {
			attachments = append(attachments, configCapturedAttachment{
				URI:        ref.URI,
				Capability: ref.Capability,
				Function:   ref.Function,
				Role:       string(ref.Role),
			})
		}
		sockets = append(sockets, configCapturedSocket{
			Socket:      string(cs.Socket),
			Field:       cs.Field,
			Attachments: attachments,
		})
	}
	catalogDirs := res.CatalogDirs
	if catalogDirs == nil {
		catalogDirs = []string{}
	}

	return s.writeResult(ctx, req.ID, configCaptureResult{
		ConfigFile:    res.Path,
		WorkspaceRoot: s.workspaceRoot,
		Captured:      res.Captured(),
		Sockets:       sockets,
		CatalogDirs:   catalogDirs,
	})
}
