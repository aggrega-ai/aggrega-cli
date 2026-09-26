// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package workspaceconfig

import (
	"errors"
	"strings"
	"testing"
)

// The assertions below are written against the loader rather than against the
// schema document. "The schema declares additionalProperties: false" is a
// statement about an artifact, and an artifact rejects nothing by existing; the
// property under test is that a config file carrying an unknown key under `mcp`
// is REFUSED when it is loaded. So every row here writes a real
// `.aggrega-config.yaml` and drives it through load, which is the one path
// every command reaches the configuration through.
//
// Each accepting row is also the schema-and-type lockstep in both directions at
// once, and that is why the fields are read back rather than merely loaded. The
// `mcp` object and every object nested in it forbid additional properties, so a
// Go field whose key the schema does not declare would make the document a load
// failure — the schema-to-type direction. A key the schema declares that no Go
// field binds would load cleanly and leave the field at its zero value, which
// is the silent-drop direction, and only reading the value back catches it.

// mcpFullConfig sets every key the `mcp` block declares. It is written once and
// shared, so a key added to the block without a row here leaves this fixture
// visibly incomplete rather than silently uncovered.
const mcpFullConfig = `
mcp:
  transport: http
  listen: 127.0.0.1:7777
  tls:
    cert_file: /etc/aggrega/server.pem
    key_file: /etc/aggrega/server.key
  allowed_origins:
    - https://app.example.com
    - https://studio.example.com
  limits:
    max_body_bytes: 1048576
    max_concurrent_requests: 4
    requests_per_minute: 30
  authorization:
    issuer: https://login.example.com
    resource: https://mcp.example.com/mcp
    jwks_file: /etc/aggrega/keys.json
    trusted_proxy:
      principal_header: X-Forwarded-User
      scopes_header: X-Forwarded-Scopes
  deny_reach:
    - registry
    - machine
  health:
    listen: unix:/run/aggrega/health.sock
`

// TestLoadMCPEveryDeclaredKey confirms a config setting every key the `mcp`
// block declares loads, and that each value reaches its field.
//
// A planted defect is visible: drop a property from the schema and the load
// fails outright, because the block forbids additional properties; misspell a
// yaml tag on the Go side and the load still succeeds while that one field
// reads back as its zero value, which the per-field assertions below catch.
func TestLoadMCPEveryDeclaredKey(t *testing.T) {
	t.Parallel()
	cfg, err := load(writeFixture(t, mcpFullConfig))
	if err != nil {
		t.Fatalf("load a config setting every declared mcp key: %v", err)
	}
	// The fixture writes the value that is NOT the default, so a transport that
	// failed to reach its field reads back as the default and is caught here.
	if cfg.MCP.Transport != MCPTransportHTTP {
		t.Errorf("MCP.Transport: got %q, want %q", cfg.MCP.Transport, MCPTransportHTTP)
	}
	if cfg.MCP.Listen != "127.0.0.1:7777" {
		t.Errorf("MCP.Listen: got %q, want %q", cfg.MCP.Listen, "127.0.0.1:7777")
	}
	if cfg.MCP.TLS.CertFile != "/etc/aggrega/server.pem" {
		t.Errorf("MCP.TLS.CertFile: got %q, want %q", cfg.MCP.TLS.CertFile, "/etc/aggrega/server.pem")
	}
	if cfg.MCP.TLS.KeyFile != "/etc/aggrega/server.key" {
		t.Errorf("MCP.TLS.KeyFile: got %q, want %q", cfg.MCP.TLS.KeyFile, "/etc/aggrega/server.key")
	}
	if len(cfg.MCP.AllowedOrigins) != 2 ||
		cfg.MCP.AllowedOrigins[0] != "https://app.example.com" ||
		cfg.MCP.AllowedOrigins[1] != "https://studio.example.com" {
		t.Errorf("MCP.AllowedOrigins: got %+v, want the two listed origins in author order", cfg.MCP.AllowedOrigins)
	}
	if cfg.MCP.Limits.MaxBodyBytes != 1_048_576 {
		t.Errorf("MCP.Limits.MaxBodyBytes: got %d, want 1048576", cfg.MCP.Limits.MaxBodyBytes)
	}
	if cfg.MCP.Limits.MaxConcurrentRequests != 4 {
		t.Errorf("MCP.Limits.MaxConcurrentRequests: got %d, want 4", cfg.MCP.Limits.MaxConcurrentRequests)
	}
	if cfg.MCP.Limits.RequestsPerMinute != 30 {
		t.Errorf("MCP.Limits.RequestsPerMinute: got %d, want 30", cfg.MCP.Limits.RequestsPerMinute)
	}
	if cfg.MCP.Authorization.Issuer != "https://login.example.com" {
		t.Errorf("MCP.Authorization.Issuer: got %q, want %q",
			cfg.MCP.Authorization.Issuer, "https://login.example.com")
	}
	if cfg.MCP.Authorization.Resource != "https://mcp.example.com/mcp" {
		t.Errorf("MCP.Authorization.Resource: got %q, want %q",
			cfg.MCP.Authorization.Resource, "https://mcp.example.com/mcp")
	}
	if cfg.MCP.Authorization.JWKSFile != "/etc/aggrega/keys.json" {
		t.Errorf("MCP.Authorization.JWKSFile: got %q, want %q",
			cfg.MCP.Authorization.JWKSFile, "/etc/aggrega/keys.json")
	}
	if cfg.MCP.Authorization.TrustedProxy.PrincipalHeader != "X-Forwarded-User" {
		t.Errorf("MCP.Authorization.TrustedProxy.PrincipalHeader: got %q, want %q",
			cfg.MCP.Authorization.TrustedProxy.PrincipalHeader, "X-Forwarded-User")
	}
	if cfg.MCP.Authorization.TrustedProxy.ScopesHeader != "X-Forwarded-Scopes" {
		t.Errorf("MCP.Authorization.TrustedProxy.ScopesHeader: got %q, want %q",
			cfg.MCP.Authorization.TrustedProxy.ScopesHeader, "X-Forwarded-Scopes")
	}
	if len(cfg.MCP.DenyReach) != 2 ||
		cfg.MCP.DenyReach[0] != "registry" ||
		cfg.MCP.DenyReach[1] != "machine" {
		t.Errorf("MCP.DenyReach: got %+v, want the two kinds in author order", cfg.MCP.DenyReach)
	}
	if cfg.MCP.Health.Listen != "unix:/run/aggrega/health.sock" {
		t.Errorf("MCP.Health.Listen: got %q, want %q",
			cfg.MCP.Health.Listen, "unix:/run/aggrega/health.sock")
	}
}

// TestLoadMCPAbsentBlockLeavesDocumentedDefaults pins what a workspace that
// never mentions the block gets: the standard-input-and-output transport, no
// address, no certificate, no origin, and the caps at the values the schema
// states.
//
// The transport is the load-bearing one — it is what leaves the server speaking
// over standard input and output — so a change that seeded the other transport
// here would start a listener for every workspace that never asked for one.
func TestLoadMCPAbsentBlockLeavesDocumentedDefaults(t *testing.T) {
	t.Parallel()
	cfg, err := load(writeFixture(t, "default_registry: my-registry.example.com\n"))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.MCP.Transport != MCPTransportStdio {
		t.Errorf("MCP.Transport: got %q, want %q (the documented default)", cfg.MCP.Transport, MCPTransportStdio)
	}
	if cfg.MCP.Listen != "" {
		t.Errorf("MCP.Listen: got %q, want empty (no address named)", cfg.MCP.Listen)
	}
	if cfg.MCP.TLS.CertFile != "" || cfg.MCP.TLS.KeyFile != "" {
		t.Errorf("MCP.TLS: got %+v, want both paths empty", cfg.MCP.TLS)
	}
	if len(cfg.MCP.AllowedOrigins) != 0 {
		t.Errorf("MCP.AllowedOrigins: got %+v, want empty", cfg.MCP.AllowedOrigins)
	}
	if cfg.MCP.Limits.MaxBodyBytes != 4_194_304 {
		t.Errorf("MCP.Limits.MaxBodyBytes: got %d, want 4194304 (the documented default)", cfg.MCP.Limits.MaxBodyBytes)
	}
	if cfg.MCP.Limits.MaxConcurrentRequests != 16 {
		t.Errorf("MCP.Limits.MaxConcurrentRequests: got %d, want 16 (the documented default)", cfg.MCP.Limits.MaxConcurrentRequests)
	}
	if cfg.MCP.Limits.RequestsPerMinute != 120 {
		t.Errorf("MCP.Limits.RequestsPerMinute: got %d, want 120 (the documented default)",
			cfg.MCP.Limits.RequestsPerMinute)
	}
	if cfg.MCP.Authorization.TrustedProxy.PrincipalHeader != "" {
		t.Errorf("MCP.Authorization.TrustedProxy.PrincipalHeader: got %q, want empty — a workspace that "+
			"never asked for it validates the tokens its callers present",
			cfg.MCP.Authorization.TrustedProxy.PrincipalHeader)
	}
	if len(cfg.MCP.DenyReach) != 0 {
		t.Errorf("MCP.DenyReach: got %+v, want empty — a workspace that never asked to withdraw a kind "+
			"of reach carries whatever its callers' scopes admit", cfg.MCP.DenyReach)
	}
	if cfg.MCP.Health.Listen != "" {
		t.Errorf("MCP.Health.Listen: got %q, want empty (no second listener, neither check answered)",
			cfg.MCP.Health.Listen)
	}
}

// TestLoadMCPPresentBlockPreservesUnsetDefaults asserts the defaults over a
// document that DOES carry the block and sets one key in it, rather than over
// an absent block.
//
// Asserted this way the row cannot be satisfied by the block being ignored
// wholesale: the listen address proves the block parsed, and the caps then have
// to survive that parse rather than merely never having been touched.
func TestLoadMCPPresentBlockPreservesUnsetDefaults(t *testing.T) {
	t.Parallel()
	cfg, err := load(writeFixture(t, "mcp:\n  listen: unix:/run/aggrega/mcp.sock\n"))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.MCP.Listen != "unix:/run/aggrega/mcp.sock" {
		t.Fatal("precondition: the mcp block did not parse, so the defaults below would prove nothing")
	}
	// An address alone chooses no transport, so a block naming only where to
	// listen leaves the default transport in force.
	if cfg.MCP.Transport != MCPTransportStdio {
		t.Errorf("MCP.Transport: got %q, want %q (the documented default)", cfg.MCP.Transport, MCPTransportStdio)
	}
	if cfg.MCP.Limits.MaxBodyBytes != 4_194_304 {
		t.Errorf("MCP.Limits.MaxBodyBytes: got %d, want 4194304 (the documented default)", cfg.MCP.Limits.MaxBodyBytes)
	}
	if cfg.MCP.Limits.MaxConcurrentRequests != 16 {
		t.Errorf("MCP.Limits.MaxConcurrentRequests: got %d, want 16 (the documented default)", cfg.MCP.Limits.MaxConcurrentRequests)
	}
	if cfg.MCP.Limits.RequestsPerMinute != 120 {
		t.Errorf("MCP.Limits.RequestsPerMinute: got %d, want 120 (the documented default)",
			cfg.MCP.Limits.RequestsPerMinute)
	}
}

// TestLoadMCPUnknownKeyIsRefused drives one unknown key per fixture through the
// loader and asserts the refusal, once for the block itself and once for each
// nested object.
//
// One fault per fixture is what makes each row attributable: a fixture carrying
// two unknown keys would keep passing after one of the three objects stopped
// forbidding additional properties, because the other refusal would still fire.
// The accepting rows above are this table's positive control — they share its
// vocabulary and load cleanly, so a row here fails on the key it plants rather
// than on the shape of the document around it.
func TestLoadMCPUnknownKeyIsRefused(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		content string
	}{
		{
			name:    "unknown key on the block",
			content: "mcp:\n  listen: 127.0.0.1:7777\n  no_such_key: 1\n",
		},
		{
			name:    "unknown key under tls",
			content: "mcp:\n  tls:\n    cert_file: /etc/aggrega/server.pem\n    no_such_key: 1\n",
		},
		{
			name:    "unknown key under limits",
			content: "mcp:\n  limits:\n    max_body_bytes: 1048576\n    no_such_key: 1\n",
		},
		{
			name:    "unknown key under health",
			content: "mcp:\n  health:\n    listen: 127.0.0.1:7788\n    no_such_key: 1\n",
		},
		{
			name:    "unknown key under authorization",
			content: "mcp:\n  authorization:\n    issuer: https://login.example.com\n    no_such_key: 1\n",
		},
		{
			name: "unknown key under trusted_proxy",
			content: "mcp:\n  authorization:\n    trusted_proxy:\n" +
				"      principal_header: X-Forwarded-User\n      no_such_key: 1\n",
		},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if _, err := load(writeFixture(t, tc.content)); err == nil {
				t.Fatal("expected the loader to refuse the unknown key; got nil")
			} else if !errors.Is(err, ErrConfigInvalid()) {
				t.Errorf("the refusal must wrap ErrConfigInvalid; got %v", err)
			}
		})
	}
}

// TestLoadMCPTrustedProxyWithoutItsPrincipalHeaderIsRefused pins the one key
// the schema requires inside a nested `mcp` object.
//
// The header is the whole credential in that mode, so a block declaring only
// where the scopes come from names a deployment with no way to tell who is
// calling. The refusal is the schema's, made at load, rather than a check the
// serving process makes later.
func TestLoadMCPTrustedProxyWithoutItsPrincipalHeaderIsRefused(t *testing.T) {
	t.Parallel()
	// Control: the same block WITH the required key loads, so the refusal below
	// is about the absent key rather than about the block existing at all.
	accepted := "mcp:\n  authorization:\n    trusted_proxy:\n      principal_header: X-Forwarded-User\n"
	if _, err := load(writeFixture(t, accepted)); err != nil {
		t.Fatalf("a trusted_proxy block naming its principal header was refused: %v", err)
	}

	refused := "mcp:\n  authorization:\n    trusted_proxy:\n      scopes_header: X-Forwarded-Scopes\n"
	if _, err := load(writeFixture(t, refused)); err == nil {
		t.Fatal("expected the loader to refuse a trusted_proxy block naming no principal header; got nil")
	} else if !errors.Is(err, ErrConfigInvalid()) {
		t.Errorf("the refusal must wrap ErrConfigInvalid; got %v", err)
	}
}

// TestLoadMCPDenyReachOutsideTheClosedSetIsRefused drives the refusal a
// misspelled kind of reach earns, at the moment the configuration loads.
//
// The direction is what matters. An operator writing this setting has asked for
// a bound, and a value that is silently ignored leaves them served without one
// while nothing about the running server says so — so the value is held to the
// closed set here, before anything binds, rather than being read for whatever it
// happens to match later.
//
// Each accepting row is the control, and there are two of them for two different
// reasons: the empty list is what every workspace that never mentions the setting
// carries, and a list naming real kinds is what proves the refusal below is about
// the value rather than about the key existing at all.
func TestLoadMCPDenyReachOutsideTheClosedSetIsRefused(t *testing.T) {
	t.Parallel()

	for _, accepted := range []struct {
		name    string
		content string
	}{
		{"an empty list", "mcp:\n  deny_reach: []\n"},
		{"every kind the vocabulary holds", "mcp:\n  deny_reach: [none, workspace, registry, machine]\n"},
	} {
		accepted := accepted
		t.Run(accepted.name, func(t *testing.T) {
			t.Parallel()
			if _, err := load(writeFixture(t, accepted.content)); err != nil {
				t.Fatalf("the loader refused %s: %v", accepted.name, err)
			}
		})
	}

	for _, tc := range []struct {
		name    string
		content string
	}{
		{"a kind that does not exist", "mcp:\n  deny_reach: [everything]\n"},
		{"a near miss on a real kind", "mcp:\n  deny_reach: [registries]\n"},
		{"a real kind beside one that is not", "mcp:\n  deny_reach: [registry, everything]\n"},
		{"the empty string", "mcp:\n  deny_reach: ['']\n"},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if _, err := load(writeFixture(t, tc.content)); err == nil {
				t.Fatalf("expected the loader to refuse %s; got nil", tc.name)
			} else if !errors.Is(err, ErrConfigInvalid()) {
				t.Errorf("the refusal must wrap ErrConfigInvalid; got %v", err)
			}
		})
	}
}

// TestLoadMCPTransportOutsideTheTwoIsRefused drives the refusal a transport the
// server does not speak earns when the configuration loads, and reads that the
// refusal names the two it does.
//
// A misspelled transport that loaded would leave a server meant to listen on the
// default, speaking over standard input and output with no listener, and nothing about the
// running process would say the value was never read — so it is held to the two
// words here, before anything starts.
//
// The two accepting rows are the control: each loads and reads back as written,
// so a refusal below is about the value rather than about the key existing.
func TestLoadMCPTransportOutsideTheTwoIsRefused(t *testing.T) {
	t.Parallel()

	for _, accepted := range []string{MCPTransportStdio, MCPTransportHTTP} {
		accepted := accepted
		t.Run("accepts "+accepted, func(t *testing.T) {
			t.Parallel()
			cfg, err := load(writeFixture(t, "mcp:\n  transport: "+accepted+"\n"))
			if err != nil {
				t.Fatalf("the loader refused transport %q: %v", accepted, err)
			}
			if cfg.MCP.Transport != accepted {
				t.Errorf("MCP.Transport: got %q, want %q", cfg.MCP.Transport, accepted)
			}
		})
	}

	for _, tc := range []struct {
		name    string
		content string
	}{
		{"a transport that does not exist", "mcp:\n  transport: tcp\n"},
		{"a known transport in the wrong case", "mcp:\n  transport: HTTP\n"},
		{"the empty string", "mcp:\n  transport: ''\n"},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := load(writeFixture(t, tc.content))
			if err == nil {
				t.Fatalf("expected the loader to refuse %s; got nil", tc.name)
			}
			if !errors.Is(err, ErrConfigInvalid()) {
				t.Errorf("the refusal must wrap ErrConfigInvalid; got %v", err)
			}
			for _, word := range []string{MCPTransportStdio, MCPTransportHTTP} {
				if !strings.Contains(err.Error(), word) {
					t.Errorf("the refusal does not name %q, one of the two transports a file may write: %v", word, err)
				}
			}
		})
	}
}

// TestLoadMCPCapBelowMinimumIsRefused pins that no cap has an off value.
//
// Zero is the value a reader reaches for when they want a cap lifted, because
// two of this file's sibling cache settings give it exactly that meaning. These
// caps bound what an unauthenticated network peer may ask for before it is
// refused, so the schema's minimum is 1 and zero is a load failure rather than
// a quiet removal of the bound.
func TestLoadMCPCapBelowMinimumIsRefused(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		content string
	}{
		{"max_body_bytes zero", "mcp:\n  limits:\n    max_body_bytes: 0\n"},
		{"max_concurrent_requests zero", "mcp:\n  limits:\n    max_concurrent_requests: 0\n"},
		{"requests_per_minute zero", "mcp:\n  limits:\n    requests_per_minute: 0\n"},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if _, err := load(writeFixture(t, tc.content)); err == nil {
				t.Fatal("expected the loader to refuse a cap of zero; got nil")
			} else if !errors.Is(err, ErrConfigInvalid()) {
				t.Errorf("the refusal must wrap ErrConfigInvalid; got %v", err)
			}
		})
	}
}
