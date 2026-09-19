// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"bytes"
	"net/http"
	"sync"

	"github.com/aggrega-ai/aggrega-cli/internal/clisurface"
	"github.com/aggrega-ai/aggrega-cli/internal/mcp"
	"github.com/aggrega-ai/aggrega-cli/pkg/egress"

	"github.com/spf13/cobra"
)

// helpListen is the listen flag's help text, held beside the wiring that reads
// it so the promise a reader is given and the layer that keeps it sit together.
//
// It names the confinement because that is what this flag's own value can be
// refused for at startup, and a reader who meets that refusal needs the two
// settings that lift it in the same breath as the rule that raised it.
const helpListen = "Serve the protocol over HTTP on this address instead of standard input and " +
	"output, in one of two forms: host:port, or unix: followed by a filesystem path. Without " +
	"it, and with no mcp.listen in the workspace configuration, the server speaks over standard " +
	"input and output. A listener with no certificate serves plaintext and is permitted only on " +
	"loopback (127.0.0.1, ::1, localhost) or on a Unix socket, which is the shape a deployment " +
	"behind a terminating proxy takes; any other address needs mcp.tls.cert_file and " +
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

Two transports reach the same server, and the invocation chooses between
them. With no listen address the server speaks newline-delimited JSON-RPC
2.0 over the process's standard input and output, which is how an
MCP-capable client runs it: spawned as a subprocess, not typed by hand.
Given --listen, or mcp.listen in the workspace configuration, it serves
the protocol over HTTP instead — one long-running process on a host,
reached by callers who are not at its keyboard.

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

The verification keys are read from the issuer itself, by asking it where
it publishes them and fetching them from the address it names, and read
again once when a token arrives signed by a key identifier the held set
does not carry. A workspace that is offline makes no such request, so
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
workspace.catalog_dirs setting, then its catalog directory. Over
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
	c.Flags().String("listen", "", helpListen)
	return c
}

// runMCP starts the server on the transport the invocation selects.
//
// The two transports differ in how a client reaches the server and in nothing
// else: the same configuration, the same workspace and the same tool surface go
// to both, and the branch below is the whole of the difference.
//
// The listen address is read from the flag when the reader set it and from the
// workspace configuration otherwise. Reading whether the flag CHANGED rather
// than what it holds is what makes an unset flag fall through to the config key
// instead of forcing an empty value over it — the same ladder the editor
// backend's own launch dial takes.
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

	listen := settings.Listen
	if cmd.Flags().Changed("listen") {
		listen, _ = cmd.Flags().GetString("listen")
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
		// Supplied from here for the same reason, and over the same tree: the
		// help a cli:// read serves is the help this tree prints.
		RenderHelp: commandHelp(cmd.Root()),
	}

	if listen == "" {
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
			Address:       listen,
			CertFile:      settings.TLS.CertFile,
			KeyFile:       settings.TLS.KeyFile,
			HealthAddress: settings.Health.Listen,
		})
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
