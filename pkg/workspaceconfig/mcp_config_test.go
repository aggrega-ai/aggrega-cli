// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package workspaceconfig

import (
	"errors"
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
	if cfg.MCP.Health.Listen != "unix:/run/aggrega/health.sock" {
		t.Errorf("MCP.Health.Listen: got %q, want %q",
			cfg.MCP.Health.Listen, "unix:/run/aggrega/health.sock")
	}
}

// TestLoadMCPAbsentBlockLeavesDocumentedDefaults pins what a workspace that
// never mentions the block gets: no listener, no certificate, no origin, and
// both caps at the values the schema states.
//
// The empty listen address is the load-bearing one — it is what leaves the
// server speaking over standard input and output — so a change that started
// seeding an address here would be a change of transport for every workspace
// that never asked for one.
func TestLoadMCPAbsentBlockLeavesDocumentedDefaults(t *testing.T) {
	t.Parallel()
	cfg, err := load(writeFixture(t, "default_registry: my-registry.example.com\n"))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.MCP.Listen != "" {
		t.Errorf("MCP.Listen: got %q, want empty (no listener, stdio transport)", cfg.MCP.Listen)
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
	if cfg.MCP.Limits.MaxBodyBytes != 4_194_304 {
		t.Errorf("MCP.Limits.MaxBodyBytes: got %d, want 4194304 (the documented default)", cfg.MCP.Limits.MaxBodyBytes)
	}
	if cfg.MCP.Limits.MaxConcurrentRequests != 16 {
		t.Errorf("MCP.Limits.MaxConcurrentRequests: got %d, want 16 (the documented default)", cfg.MCP.Limits.MaxConcurrentRequests)
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

// TestLoadMCPCapBelowMinimumIsRefused pins that neither cap has an off value.
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
