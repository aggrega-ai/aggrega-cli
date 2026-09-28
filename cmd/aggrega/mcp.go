// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"bytes"
	"fmt"
	"net/http"
	"sync"

	"github.com/aggrega-ai/aggrega-cli/internal/clisurface"
	"github.com/aggrega-ai/aggrega-cli/internal/mcp"
	"github.com/aggrega-ai/aggrega-cli/pkg/egress"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"

	"github.com/spf13/cobra"
)

// helpTransport is the transport flag's help text, held beside the wiring that
// reads it for the same reason helpListen is.
//
// The flag's own default is left empty rather than set to stdio, so the help
// shows no default beside it: an untyped flag falls through to the config key,
// and a help line printing stdio as the flag's default would tell a reader
// whose configuration chooses http that the command ignores it.
const helpTransport = "How the server is reached: stdio speaks the protocol over standard input and " +
	"output, which is how a client that launches this command as a subprocess reaches it; http " +
	"serves it over HTTP on the address --listen or mcp.listen names, and over HTTPS where mcp.tls " +
	"names a certificate and key. When set, overrides the mcp.transport config key, whose default " +
	"is stdio."

// helpListen is the listen flag's help text, held beside the wiring that reads
// it so the promise a reader is given and the layer that keeps it sit together.
//
// It names the confinement because that is what this flag's own value can be
// refused for at startup, and a reader who meets that refusal needs the two
// settings that lift it in the same breath as the rule that raised it.
const helpListen = "The address the HTTP transport listens on, in one of two forms: host:port, or " +
	"unix: followed by a filesystem path. It names where to listen and chooses no transport: it is " +
	"read with --transport http, or mcp.transport: http in the workspace configuration, and given " +
	"with the stdio transport it is refused. A listener with no certificate serves plaintext and is " +
	"permitted only on loopback (127.0.0.1, ::1, localhost) or on a Unix socket, which is the shape " +
	"a server behind a terminating proxy takes; any other address needs mcp.tls.cert_file and " +
	"mcp.tls.key_file, which this process then uses to terminate TLS itself. When set, overrides " +
	"the mcp.listen config key."

func newMCPCmd() *cobra.Command {
	c := &cobra.Command{
		Use: "mcp",
		Short: "Run a Model Context Protocol server over standard input and output, or over HTTP, " +
			"for AI-assistant integration",
		Long: `Run Aggrega as a Model Context Protocol (MCP) server, so an AI
assistant can discover and drive Aggrega operations through a structured
protocol instead of scraping command output.

Two transports reach the same server, and --transport chooses between
them, with mcp.transport in the workspace configuration as the key it
overrides. The default, stdio, speaks newline-delimited JSON-RPC 2.0 over
the process's standard input and output, which is how an MCP-capable
client runs it: spawned as a subprocess, not typed by hand. http serves
the protocol over HTTP instead — one long-running process on a host,
reached by callers who are not at its keyboard — on the address --listen
names, or mcp.listen when the flag is absent.

An address chooses no transport. --listen given with the stdio transport
is refused, whether stdio was typed, configured or left as the default,
because it names where to listen for a transport that does not listen.
An address mcp.listen names is left unused by a server on standard input
and output, which says so in one line on stderr, so a client can launch
this command in a directory whose configuration is written for a
listener. And the http transport with no address from either is refused
before anything binds.

It speaks two MCP protocol revisions on both transports, 2026-07-28 and
2025-06-18, so a client of either connects. In 2026-07-28 nothing is
settled for the connection as a whole: every request states the protocol
version it speaks and the capabilities its sender offers, and the server
reads both from the request it is answering rather than holding anything
between messages. In 2025-06-18 a client settles those same facts once,
by opening the connection with an initialize handshake.

Both revisions reach the same server, and a client learns the same
things about it either way. A client on 2026-07-28 calls server/discover
for the revisions the server speaks, the capabilities it advertises —
tools, resources, and prompts — and the guidance it offers a model
before the first call. A client on 2025-06-18 receives those same
capabilities, and that same guidance, in the reply to its handshake.

From there the client discovers the server's surface through the list
methods (tools, resources, resource templates, and prompts) and invokes
it through tools/call and resources/read. The server exposes Aggrega
operations — compiling and inspecting a Release, reading catalog
entities — as tools and resources an assistant can call directly.

On standard input and output, stream discipline is strict: stdout carries
only protocol messages, one JSON object per line, so the client can parse
the stream without heuristics. Every log, warning, and diagnostic is
written to stderr instead. The server reads one message per line and
exits cleanly when stdin reaches end of stream, so a client that closes
the connection shuts the server down. A client ends one call by naming
its id, and every other call carries on.

Over HTTP there is one endpoint, /mcp, accepting POST. Every message is
its own request and no session of any kind is kept, so nothing a client
sends first decides how a later request is answered. Closing the
connection ends the call it carried. A request carrying an Origin header
is checked against mcp.allowed_origins, and mcp.limits bounds how large
one request body may be, how many requests are answered at once, and how
many one caller may send in a minute. A caller sending faster than
mcp.limits.requests_per_minute is answered 429, counted against that
caller alone.

Every request to /mcp carries a credential, and which one depends on
whether a proxy in front has already checked it. By default it is a
bearer token the server verifies itself, against the keys
mcp.authorization.issuer publishes: it must come from that issuer, name
mcp.authorization.resource among its audiences, be inside its validity
window, and be signed with an asymmetric algorithm. A request carrying no
token is answered 401 with a challenge, and so is one whose token fails
any of those. A token is never accepted from the address, never forwarded
anywhere, never exchanged, and never written to any log; the subject it
names is recorded and compared to nothing.

Set mcp.authorization.trusted_proxy instead and the caller is read from a
header the proxy sets rather than from a token, with its scopes from a
second header; no token is read at all in that mode, and a request
without the principal header is answered 401. Because that header is then
the whole credential, it is trustworthy only where nothing else can set
it: the process is refused before it binds unless mcp.listen is loopback
or a Unix socket.

The challenge names where to obtain a token. A listener publishes an
OAuth protected-resource metadata document, with no credential asked for,
naming the authorization server, this deployment's own address and the
scopes that exist; it is served at /.well-known/oauth-protected-resource
and at that path followed by the endpoint path, which are the two places
a client looks. A client reads it before it can hold a token, which is
why it is the one answer given without one. What that document publishes
comes from mcp.authorization.issuer and mcp.authorization.resource, and a
listener is refused before it binds unless both are set.

The issuer is an https address unless the authorization server runs on
this machine (localhost, 127.0.0.1 or ::1), and that holds in every
mode: with the keys read from the issuer, with a key file, and with a
proxy in front. Every client is sent to the issuer to obtain its token,
so a plain http issuer on another host would carry each client's
authorization code and token across the network unprotected, and a
listener naming one is refused before it binds.

The verification keys are read from the issuer itself, by asking it where
it publishes them and fetching them from the address it names, and read
again once when a token arrives signed by a key identifier the held set
does not carry. That address is held to the same rule as the issuer, and
so is every redirect either read is answered with: a plain http address
on another host is never asked, and a token needing those keys is
answered 503 until the authorization server publishes its metadata and
keys over https. A workspace that is offline makes no such request, so
there mcp.authorization.jwks_file names a file holding those keys and a
listener is refused before it binds without it.

Liveness and readiness are answered on a second address, and only there.
Set mcp.health.listen and a GET of /healthz reports that the process is
running while a GET of /readyz reports whether it can answer a call yet,
each a status code with an empty body and no credential asked for. The
address opens before the workspace is resolved, so a supervisor sees the
process alive while it is still deciding whether it can serve at all.
Leave the key unset and no second listener exists. The endpoint itself
answers both paths the same not-found it gives every path it does not
serve, with a token and without one, so reaching this process over the
network is never a way to probe it.

The workspace this process serves is fixed when it starts, on either
transport: --workspace-root names it, and AGGREGA_WORKSPACE_ROOT supplies
it when the flag is absent. With neither, a server on standard input and
output resolves the workspace from the directory it was started in, while
a listener is refused before it binds unless a workspace resolves there:
one reached over HTTP serves a workspace an operator chose rather than
whichever directory it happened to be started in.

For the same reason a call arriving over HTTP cannot name a workspace of
its own. Sending workspace_root or catalog_dirs is refused, naming the
argument, and the directories searched for catalog entities beside the
served workspace are the ones that workspace itself resolves: its own
workspace.catalog_dirs setting — or, for a Release it gives directories
of its own under workspace.releases, that Release's — then its catalog
directory. Over
standard input and output both arguments are honoured: the client that
launched the server is running on this machine.

Asking the process itself to stop — an interrupt from the terminal, or a
termination request from a supervisor or container runtime — shuts the
server down on the first request, whether or not a client is sending
anything: it stops accepting messages, ends the work a call in progress
is doing, and exits cleanly. A listener stops accepting and lets a call
already being answered finish, within a bounded budget, before the
process exits. A second request ends the process at once.`,
		Args: cobra.NoArgs,
		RunE: runMCP,
	}
	c.Flags().String("transport", "", helpTransport)
	c.Flags().String("listen", "", helpListen)
	return c
}

// runMCP starts the server on the transport the invocation selects.
//
// The two transports differ in how a client reaches the server and in nothing
// else: the same configuration, the same workspace and the same tool surface go
// to both, and the branch below is the whole of the difference. Which of the two
// this invocation speaks, and where a listener binds, is resolveMCPLaunch's to
// decide, before either server starts.
func runMCP(cmd *cobra.Command, args []string) error {
	flagRoot, _ := cmd.Flags().GetString("workspace-root")
	root, err := validateWorkspaceRootFlag(workspaceRootFromFlagOrEnv(cmd, flagRoot, nil))
	if err != nil {
		return err
	}

	// The workspace is read through the resolution every command reads, so the
	// mcp block this launch honours is the one the cascade composed rather than
	// one loaded a second time here.
	ws, err := workspaceFromContext(cmd.Context())
	if err != nil {
		return err
	}
	settings := ws.Config().MCP

	launch, err := resolveMCPLaunch(cmd, ws)
	if err != nil {
		return err
	}

	cfg := mcp.Config{
		Stdin:         cmd.InOrStdin(),
		Stdout:        cmd.OutOrStdout(),
		Stderr:        cmd.ErrOrStderr(),
		ServerVersion: provenanceCompilerVersion(),
		WorkspaceRoot: root,
		// Supplied for the same reason the command surface below is: the value
		// belongs to this package and the server lives under it. Both halves of the
		// stamp are read through the derivations this package already owns, so a
		// lockfile a tool call writes records the same compiler and the same core
		// plugin versions a lockfile written at a terminal records for this binary.
		Toolchain: toolchainStamp(),
		Commands:  commandSurface(cmd.Root()),
		// Read from the same resolved block the listener settings below are, and
		// handed to both transports rather than to the listener alone: which tools
		// a client is offered is a property of the server, and the client a
		// narrowing exists for is usually the one that launched this process.
		AnnounceTools: settings.AnnounceTools,
		// Supplied from here for the same reason, and over the same tree: the
		// help a cli:// read serves is the help this tree prints.
		RenderHelp: commandHelp(cmd.Root()),
		// Supplied from here because the cascade it binds is assembled in this
		// package: it is shared with the install path, the plugin listing and the
		// runtime plugin runner, so a plugin reference a tool call resolves is
		// resolved by the identical cascade the command line resolves it by.
		ResolveImportPlugin: importPluginBytes,
		// Supplied from here for the same reason and off the same cascade: the
		// enumeration walks the workspace's sockets and resolves each binding
		// through the resolver this package assembles, so the plugins a tool call
		// reports are the plugins `aggrega plugin list` reports — one walk, not two
		// that agree.
		ListPlugins: pluginListing,
	}

	if launch.transport == workspaceconfig.MCPTransportStdio {
		if launch.notice != "" {
			_, _ = fmt.Fprintln(cmd.ErrOrStderr(), launch.notice)
		}
		return mcp.Run(cmd.Context(), cfg)
	}
	// cmd.Context() is the run's context, which ends when the process is first
	// asked to stop. The listener reads it as the signal to stop accepting and
	// drain, so a termination request from a supervisor ends it on the same terms
	// the standard-input transport ends on.
	return mcp.ListenAndServe(cmd.Context(), cfg,
		mcp.HTTPConfig{
			AllowedOrigins:        settings.AllowedOrigins,
			MaxBodyBytes:          settings.Limits.MaxBodyBytes,
			MaxConcurrentRequests: settings.Limits.MaxConcurrentRequests,
			RequestsPerMinute:     settings.Limits.RequestsPerMinute,
			Authorization: mcp.AuthorizationConfig{
				Issuer:   settings.Authorization.Issuer,
				Resource: settings.Authorization.Resource,
				JWKSFile: settings.Authorization.JWKSFile,
				TrustedProxy: mcp.TrustedProxyConfig{
					PrincipalHeader: settings.Authorization.TrustedProxy.PrincipalHeader,
					ScopesHeader:    settings.Authorization.TrustedProxy.ScopesHeader,
				},
			},
			DenyReach: settings.DenyReach,
			// The gate is handed over rather than the posture: whether this
			// workspace makes an outbound request at all is the egress gate's
			// decision, and reading the offline field a second time inside the
			// server would be a second copy of it.
			Egress: func() (*http.Client, error) { return egress.HTTPClient(ws.Config()) },
		},
		mcp.ListenConfig{
			Address:       launch.address,
			CertFile:      settings.TLS.CertFile,
			KeyFile:       settings.TLS.KeyFile,
			HealthAddress: settings.Health.Listen,
		})
}

// mcpLaunch is what one invocation of `aggrega mcp` resolved to.
type mcpLaunch struct {
	// transport is workspaceconfig.MCPTransportStdio or
	// workspaceconfig.MCPTransportHTTP.
	transport string
	// address is where the listener binds on the HTTP transport, and empty on
	// standard input and output.
	address string
	// notice is the one line a server on standard input and output writes to say
	// that a configured address is not used, and empty where no address was
	// configured.
	notice string
}

// resolveMCPLaunch decides which transport this invocation speaks and, on HTTP,
// where it listens. It is the one place that decision is made, and runMCP calls
// it before either server starts, so every refusal it makes is made before
// anything binds.
//
// Each flag is read by whether it was TYPED rather than by what it holds, which
// is what lets an untyped flag fall through to its config key instead of
// forcing an empty value over it: a typed --transport wins over mcp.transport,
// which wins over the default, stdio; a typed --listen wins over mcp.listen.
//
// The address chooses nothing. So the HTTP transport with no address is
// refused; a typed --listen under stdio is refused, however stdio was chosen,
// because it names where to listen for a transport that does not listen; and an
// address that only the configuration names is left unused under stdio, with
// one line saying so. That last case is refused nowhere on purpose: it is the
// launch a client makes when it spawns this command in a directory whose
// configuration is written for a listener, and a listener's configuration that
// forgets the transport already fails where it is seen, with no listener and a
// process that ends at the end of its input.
//
// A typed --listen carrying an empty value is refused before any of that,
// whatever the transport, rather than falling through to mcp.listen: an empty
// address names nowhere, and treating it as unset would make the flag's absence
// and its presence mean the same thing.
func resolveMCPLaunch(cmd *cobra.Command, ws *workspaceconfig.ResolvedWorkspace) (mcpLaunch, error) {
	settings := ws.Config().MCP
	flags := cmd.Flags()

	transport := settings.Transport
	transportTyped := flags.Changed("transport")
	if transportTyped {
		transport, _ = flags.GetString("transport")
	}

	// chosenBy names what chose the transport, the way a refusal has to name it
	// for a reader to know what to change, and reports whether nothing did. An
	// untyped transport's layer is read from the provenance the configuration
	// reading reports rather than worked out again here, and only a refusal asks
	// for it, so a launch that is refused nothing reads nothing more.
	chosenBy := func() (phrase string, byDefault bool, err error) {
		if transportTyped {
			return "--transport " + transport, false, nil
		}
		origin, err := mcpSettingOrigin(ws, "mcp.transport")
		if err != nil {
			return "", false, err
		}
		if kind, _ := origin.Layer(); kind == workspaceconfig.OriginBuiltInDefault {
			return "the default", true, nil
		}
		return fmt.Sprintf("mcp.transport: %s in %s", transport, origin.Source), false, nil
	}

	if transport != workspaceconfig.MCPTransportStdio && transport != workspaceconfig.MCPTransportHTTP {
		// A configuration file is held to the same two words by the schema when
		// it loads, so this is met by a typed value; the setting is named either
		// way so the refusal stays true of whichever was read.
		setting := "mcp.transport"
		if transportTyped {
			setting = "--transport"
		}
		return mcpLaunch{}, fmt.Errorf("%s was given %q, which is no transport this command speaks: "+
			"the two it speaks are %s and %s",
			setting, transport, workspaceconfig.MCPTransportStdio, workspaceconfig.MCPTransportHTTP)
	}

	listenTyped := flags.Changed("listen")
	listen := settings.Listen
	if listenTyped {
		listen, _ = flags.GetString("listen")
		if listen == "" {
			return mcpLaunch{}, fmt.Errorf("--listen was given an empty address, which names nowhere to listen: "+
				"give it host:port, or unix: followed by a filesystem path, with --transport %s, or leave it out "+
				"to speak over standard input and output", workspaceconfig.MCPTransportHTTP)
		}
	}

	if transport == workspaceconfig.MCPTransportHTTP {
		if listen == "" {
			by, _, err := chosenBy()
			if err != nil {
				return mcpLaunch{}, err
			}
			return mcpLaunch{}, fmt.Errorf("the HTTP transport, chosen by %s, needs an address to listen on, and "+
				"neither --listen nor mcp.listen names one: pass --listen host:port, or --listen unix: followed by "+
				"a filesystem path, or set mcp.listen in the workspace configuration", by)
		}
		return mcpLaunch{transport: transport, address: listen}, nil
	}

	if listenTyped {
		by, byDefault, err := chosenBy()
		if err != nil {
			return mcpLaunch{}, err
		}
		if byDefault {
			return mcpLaunch{}, fmt.Errorf("--listen %s names an address for the HTTP transport, and no transport "+
				"was chosen, so this server would speak over standard input and output, its default: pass "+
				"--transport %s to serve over HTTP on that address, or leave --listen out",
				listen, workspaceconfig.MCPTransportHTTP)
		}
		return mcpLaunch{}, fmt.Errorf("--listen %s cannot be used with the stdio transport, chosen by %s: "+
			"--listen names where the HTTP transport listens, and stdio speaks over standard input and output. "+
			"Choose --transport %s to serve over HTTP on that address, or leave --listen out",
			listen, by, workspaceconfig.MCPTransportHTTP)
	}

	launch := mcpLaunch{transport: transport}
	if settings.Listen != "" {
		origin, err := mcpSettingOrigin(ws, "mcp.listen")
		if err != nil {
			return mcpLaunch{}, err
		}
		launch.notice = fmt.Sprintf("mcp: speaking over standard input and output, so the address %s that "+
			"mcp.listen names, from %s, is not used; set mcp.transport: %s to serve over HTTP on it",
			settings.Listen, origin.Source, workspaceconfig.MCPTransportHTTP)
	}
	return launch, nil
}

// mcpSettingOrigin returns the provenance the configuration reading reports for
// one setting of the resolved workspace, so a line naming where a setting came
// from names the layer `aggrega config show --origin` names.
func mcpSettingOrigin(ws *workspaceconfig.ResolvedWorkspace, field string) (workspaceconfig.ValueOrigin, error) {
	origins, err := ws.ValueOrigins()
	if err != nil {
		return workspaceconfig.ValueOrigin{}, err
	}
	for _, origin := range origins {
		if origin.Field == field {
			return origin, nil
		}
	}
	return workspaceconfig.ValueOrigin{}, fmt.Errorf("the configuration reading reports no %s setting", field)
}

// commandSurface projects the live command tree into the surface the MCP server
// derives its long-tail command tools from.
//
// It runs here because this is the only package that can see both the command
// tree and the server. The projection itself is the shared command catalog, so
// no command is restated: a command reaches the tool surface by existing in the
// tree. What this adds is the one fact the catalog deliberately does not record
// — whether a command executes an operation — read from the tree rather than
// inferred from a descriptor, so a grouping command that later gains an
// operation of its own reaches the tool surface with no change here.
func commandSurface(root *cobra.Command) []mcp.CommandSurface {
	if root == nil {
		return nil
	}
	runnable := map[string]bool{}
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		runnable[c.CommandPath()] = c.Runnable()
		for _, sub := range c.Commands() {
			walk(sub)
		}
	}
	walk(root)

	cat := clisurface.Catalog(root)
	out := make([]mcp.CommandSurface, 0, len(cat))
	for _, d := range cat {
		out = append(out, mcp.CommandSurface{Descriptor: d, Runnable: runnable[d.Path]})
	}
	return out
}

// helpRenderMu serialises help rendering over one command tree.
//
// None of the steps below is a read, which is the whole reason it is here.
// Resolving a path writes the name each command was reached by onto that
// command; installing the help and version flags merges a command's inherited
// flags into its own set; the writer is installed on the command being rendered;
// and the render itself swaps that writer again while it composes the usage
// block. Requests reach the server on their own goroutines, so two reads
// arriving together would meet every one of those, and the one whose writer was
// swapped out collects only the bytes the other had not already taken.
var helpRenderMu sync.Mutex

// commandHelp renders one command's help from the live tree, for the server's
// cli:// resource family.
//
// It is supplied from here for the same reason the command surface is: this is
// the only package that can see both the command tree and the server. The text
// is the tree's own help rendering, so a command reaches the resource family by
// existing in the tree and its help has one home rather than a restatement here.
//
// The path is the command's own name segments. It resolves against the tree the
// same way an invocation does, and a path that does not resolve to a command all
// the way down is reported as no command rather than as the nearest ancestor:
// resolution leaves whatever it could not match, so anything left over means the
// path named something the tree does not have.
//
// Two calls are what make the rendered text the text the command prints. Running
// a command installs its help and version flags on it immediately before the
// run, so the flag list a reader sees includes them; rendering without those
// calls produces a help block missing the help flag itself.
func commandHelp(root *cobra.Command) mcp.HelpRenderer {
	if root == nil {
		return nil
	}
	return func(path []string) (string, bool) {
		helpRenderMu.Lock()
		defer helpRenderMu.Unlock()

		target, leftover, _ := root.Find(path)
		if target == nil || len(leftover) > 0 {
			return "", false
		}
		target.InitDefaultHelpFlag()
		target.InitDefaultVersionFlag()

		// The writer goes on the command being rendered because the lookup walks
		// up the tree from there, and it is put back before the next reader sees
		// the tree.
		previous := target.OutOrStdout()
		defer target.SetOut(previous)

		var rendered bytes.Buffer
		target.SetOut(&rendered)
		if err := target.Help(); err != nil {
			return "", false
		}
		return rendered.String(), true
	}
}
