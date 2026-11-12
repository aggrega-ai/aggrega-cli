// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package workspaceconfig

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

// Six settings are handed to a reader that compares them exactly or parses them,
// and nothing but the configuration schema checks their shape first: an allowed
// origin is compared byte for byte with a browser's Origin header, a trusted-proxy
// header name is looked up on every request, a signing endpoint is handed to the
// signing client, a CLI plugin pin is compared with the digest its bytes hash to,
// and a pinned public key is decoded as PEM. A value in a shape its reader cannot
// use would otherwise load and fail later, somewhere that names no setting.
//
// So these tests are written against the loader, the one path every command reads
// the configuration through, and not against the schema document: a pattern in a
// schema refuses nothing by existing, and the property under test is that the
// file is refused when it is read, naming the file and the key.

// wellFormedPEM is a public-key PEM block of the shape a key file holds.
const wellFormedPEM = "-----BEGIN PUBLIC KEY-----\n" +
	"MFkwEwYHKoZIzj0CAQYIKoZIzj0DAQcDQgAE\n" +
	"-----END PUBLIC KEY-----\n"

// indentBlock indents every line of s by prefix, so a multi-line value sits under
// a YAML block scalar.
func indentBlock(s, prefix string) string {
	lines := strings.Split(strings.TrimSuffix(s, "\n"), "\n")
	for i, l := range lines {
		lines[i] = prefix + l
	}
	return strings.Join(lines, "\n") + "\n"
}

// TestLoadRefusesAServingOrSigningValueItsReaderCannotUse drives one malformed
// value per fixture through the loader and asserts the refusal: that it is the
// configuration gate's (ErrConfigInvalid), that it names the file and the key,
// and that it is the value's shape being refused rather than some other rule.
//
// One fault per fixture is what makes each row attributable: a document carrying
// two would keep failing after one of the patterns was dropped. The acceptance
// table below is this one's positive control — it writes the same keys in the
// shape their readers use, and each of those loads.
//
// A planted defect is visible: drop a key's pattern from the schema and every row
// naming that key loads instead of being refused.
func TestLoadRefusesAServingOrSigningValueItsReaderCannotUse(t *testing.T) {
	t.Parallel()
	hex := strings.Repeat("0123456789abcdef", 4)
	for _, tc := range []struct {
		name    string
		content string
		// field is the key the refusal names, spelled as the loader spells it.
		field string
	}{
		{
			name:    "an origin with a trailing slash",
			content: "mcp:\n  allowed_origins:\n    - https://app.example.com/\n",
			field:   "mcp.allowed_origins[0]",
		},
		{
			name:    "an origin with a path",
			content: "mcp:\n  allowed_origins:\n    - https://app.example.com/mcp\n",
			field:   "mcp.allowed_origins[0]",
		},
		{
			name:    "an origin with no scheme, after one that is well formed",
			content: "mcp:\n  allowed_origins:\n    - https://ok.example.com\n    - app.example.com\n",
			field:   "mcp.allowed_origins[1]",
		},
		{
			name:    "an origin with a capital letter",
			content: "mcp:\n  allowed_origins:\n    - https://App.example.com\n",
			field:   "mcp.allowed_origins[0]",
		},
		{
			name:    "the opaque origin a sandboxed page sends",
			content: "mcp:\n  allowed_origins:\n    - 'null'\n",
			field:   "mcp.allowed_origins[0]",
		},
		{
			name:    "a principal header with a space",
			content: "mcp:\n  authorization:\n    trusted_proxy:\n      principal_header: 'X Forwarded User'\n",
			field:   "mcp.authorization.trusted_proxy.principal_header",
		},
		{
			name:    "a principal header with a colon",
			content: "mcp:\n  authorization:\n    trusted_proxy:\n      principal_header: 'X-Forwarded-User:'\n",
			field:   "mcp.authorization.trusted_proxy.principal_header",
		},
		{
			name: "a scopes header with a space",
			content: "mcp:\n  authorization:\n    trusted_proxy:\n      principal_header: X-Forwarded-User\n" +
				"      scopes_header: 'X Forwarded Scopes'\n",
			field: "mcp.authorization.trusted_proxy.scopes_header",
		},
		{
			name:    "a certificate authority with no scheme",
			content: "signing:\n  fulcio_url: fulcio.example.com\n",
			field:   "signing.fulcio_url",
		},
		{
			name:    "a certificate authority over plain http",
			content: "signing:\n  fulcio_url: http://fulcio.example.com\n",
			field:   "signing.fulcio_url",
		},
		{
			name:    "a transparency log with no scheme",
			content: "signing:\n  rekor_url: rekor.example.com\n",
			field:   "signing.rekor_url",
		},
		{
			name:    "a timestamp authority with no scheme",
			content: "signing:\n  timestamp_url: tsa.example.com\n",
			field:   "signing.timestamp_url",
		},
		{
			name:    "a registry's transparency log with no scheme",
			content: "registries:\n  - host: registry.example.com\n    signing:\n      rekor_url: rekor.example.com\n",
			field:   "registries[0].signing.rekor_url",
		},
		{
			name:    "a CLI plugin pin in bare hexadecimal",
			content: "plugins:\n  cli:\n    - uri: oci://hub.aggrega.ai/plugin/cli/moo:1.0.0\n      digest: " + hex + "\n",
			field:   "plugins.cli[0].digest",
		},
		{
			name: "a CLI plugin pin in capitals",
			content: "plugins:\n  cli:\n    - uri: oci://hub.aggrega.ai/plugin/cli/moo:1.0.0\n      digest: sha256:" +
				strings.ToUpper(hex) + "\n",
			field: "plugins.cli[0].digest",
		},
		{
			name:    "a CLI plugin pin of the wrong length",
			content: "plugins:\n  cli:\n    - uri: oci://hub.aggrega.ai/plugin/cli/moo:1.0.0\n      digest: sha256:abc\n",
			field:   "plugins.cli[0].digest",
		},
		{
			name:    "a public key that is not a PEM block",
			content: "registries:\n  - host: registry.example.com\n    public_key: not-a-pem-block\n",
			field:   "registries[0].public_key",
		},
		{
			name: "a private key where the public key belongs",
			content: "registries:\n  - host: registry.example.com\n    public_key: |\n" +
				indentBlock("-----BEGIN PRIVATE KEY-----\nAAAA\n-----END PRIVATE KEY-----\n", "      "),
			field: "registries[0].public_key",
		},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			path := writeFixture(t, tc.content)
			_, err := load(path)
			if err == nil {
				t.Fatalf("expected the loader to refuse the file; it loaded:\n%s", tc.content)
			}
			if !errors.Is(err, ErrConfigInvalid()) {
				t.Fatalf("the refusal must wrap ErrConfigInvalid; got %v", err)
			}
			msg := err.Error()
			if !strings.Contains(msg, path) {
				t.Errorf("the refusal does not name the file %s: %v", path, err)
			}
			if !strings.Contains(msg, ": "+tc.field+": ") {
				t.Errorf("the refusal does not name the key %s: %v", tc.field, err)
			}
			if !strings.Contains(msg, "pattern: ") {
				t.Errorf("the refusal is not the value's shape being refused: %v", err)
			}
		})
	}
}

// TestLoadAcceptsTheShapeEachServingAndSigningReaderUses loads each of the six
// settings in the shape its reader uses and reads the value back.
//
// Reading back is what makes a row more than a load: a value that loaded while
// landing nowhere would pass a load alone. The rows include the forms a reader
// accepts that a narrower pattern would refuse — a loopback origin with a port,
// an IPv6 origin, a host name with an underscore, an extension origin, a header
// name in lower case, a PEM block with a line of text above it — because a
// pattern that refused any of them would refuse a value its reader can use.
func TestLoadAcceptsTheShapeEachServingAndSigningReaderUses(t *testing.T) {
	t.Parallel()
	pin := "sha256:" + strings.Repeat("0123456789abcdef", 4)
	for _, tc := range []struct {
		name    string
		content string
		read    func(Config) string
		want    string
	}{
		{"an https origin", "mcp:\n  allowed_origins:\n    - https://app.example.com\n",
			func(c Config) string { return c.MCP.AllowedOrigins[0] }, "https://app.example.com"},
		{"a loopback origin with a port", "mcp:\n  allowed_origins:\n    - http://localhost:3000\n",
			func(c Config) string { return c.MCP.AllowedOrigins[0] }, "http://localhost:3000"},
		{"an IPv4 origin", "mcp:\n  allowed_origins:\n    - http://127.0.0.1:8080\n",
			func(c Config) string { return c.MCP.AllowedOrigins[0] }, "http://127.0.0.1:8080"},
		{"an IPv6 origin", "mcp:\n  allowed_origins:\n    - 'http://[::1]:8080'\n",
			func(c Config) string { return c.MCP.AllowedOrigins[0] }, "http://[::1]:8080"},
		{"an origin whose host carries an underscore", "mcp:\n  allowed_origins:\n    - https://build_agent.internal\n",
			func(c Config) string { return c.MCP.AllowedOrigins[0] }, "https://build_agent.internal"},
		{"an extension origin", "mcp:\n  allowed_origins:\n    - chrome-extension://abcdefghijklmnop\n",
			func(c Config) string { return c.MCP.AllowedOrigins[0] }, "chrome-extension://abcdefghijklmnop"},
		{"a principal header", "mcp:\n  authorization:\n    trusted_proxy:\n      principal_header: X-Forwarded-User\n",
			func(c Config) string { return c.MCP.Authorization.TrustedProxy.PrincipalHeader }, "X-Forwarded-User"},
		{"a principal header in lower case", "mcp:\n  authorization:\n    trusted_proxy:\n      principal_header: x-auth-request-user\n",
			func(c Config) string { return c.MCP.Authorization.TrustedProxy.PrincipalHeader }, "x-auth-request-user"},
		{"a principal header carrying punctuation a header name admits",
			"mcp:\n  authorization:\n    trusted_proxy:\n      principal_header: X-User.Id_1\n",
			func(c Config) string { return c.MCP.Authorization.TrustedProxy.PrincipalHeader }, "X-User.Id_1"},
		{"a scopes header",
			"mcp:\n  authorization:\n    trusted_proxy:\n      principal_header: X-Forwarded-User\n      scopes_header: X-Forwarded-Scopes\n",
			func(c Config) string { return c.MCP.Authorization.TrustedProxy.ScopesHeader }, "X-Forwarded-Scopes"},
		{"a certificate authority", "signing:\n  fulcio_url: https://fulcio.sigstore.dev\n",
			func(c Config) string { return c.Signing.FulcioURL }, "https://fulcio.sigstore.dev"},
		{"a transparency log with a path", "signing:\n  rekor_url: https://rekor.example.com/base\n",
			func(c Config) string { return c.Signing.RekorURL }, "https://rekor.example.com/base"},
		{"a timestamp authority with a port and a path", "signing:\n  timestamp_url: https://tsa.example.com:8443/api/v1/timestamp\n",
			func(c Config) string { return c.Signing.TimestampURL }, "https://tsa.example.com:8443/api/v1/timestamp"},
		{"a registry's certificate authority",
			"registries:\n  - host: registry.example.com\n    signing:\n      fulcio_url: https://fulcio.registry.example.com\n",
			func(c Config) string { return c.Registries[0].Signing.FulcioURL }, "https://fulcio.registry.example.com"},
		{"a CLI plugin pin", "plugins:\n  cli:\n    - uri: oci://hub.aggrega.ai/plugin/cli/moo:1.0.0\n      digest: " + pin + "\n",
			func(c Config) string { return c.Plugins.CLI[0].Digest }, pin},
		{"a public key", "registries:\n  - host: registry.example.com\n    public_key: |\n" + indentBlock(wellFormedPEM, "      "),
			func(c Config) string { return c.Registries[0].PublicKey }, wellFormedPEM},
		{"an RSA public key",
			"registries:\n  - host: registry.example.com\n    public_key: |\n" +
				indentBlock(strings.ReplaceAll(wellFormedPEM, "PUBLIC KEY", "RSA PUBLIC KEY"), "      "),
			func(c Config) string { return c.Registries[0].PublicKey },
			strings.ReplaceAll(wellFormedPEM, "PUBLIC KEY", "RSA PUBLIC KEY")},
		{"a public key with a line of text above it",
			"registries:\n  - host: registry.example.com\n    public_key: |\n" +
				indentBlock("pinned from the registry owner\n"+wellFormedPEM, "      "),
			func(c Config) string { return c.Registries[0].PublicKey }, "pinned from the registry owner\n" + wellFormedPEM},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cfg, err := load(writeFixture(t, tc.content))
			if err != nil {
				t.Fatalf("the loader refused a value its reader uses: %v", err)
			}
			if got := tc.read(cfg); got != tc.want {
				t.Errorf("read back %q, want %q", got, tc.want)
			}
		})
	}
}

// TestLoadTrustedProxyNamesItsPrincipalHeaderOrIsLeftOut pins what an empty
// principal header means now: the trusted_proxy block exists only to switch the
// proxy mode on, so a block whose principal header is empty is a block written by
// mistake, and it is refused at load naming the key. Leaving the mode off means
// leaving the block out, and that loads.
//
// The empty scopes header is the other side of the same rule: it is the header's
// documented default, asserting no scopes, and it loads.
func TestLoadTrustedProxyNamesItsPrincipalHeaderOrIsLeftOut(t *testing.T) {
	t.Parallel()
	const principal = "mcp.authorization.trusted_proxy.principal_header"

	t.Run("the block left out loads, and the server checks tokens itself", func(t *testing.T) {
		t.Parallel()
		cfg, err := load(writeFixture(t, "mcp:\n  authorization:\n    issuer: https://login.example.com\n"))
		if err != nil {
			t.Fatalf("a configuration leaving trusted_proxy out was refused: %v", err)
		}
		if cfg.MCP.Authorization.Issuer != "https://login.example.com" {
			t.Fatal("precondition: the authorization block did not parse")
		}
		if got := cfg.MCP.Authorization.TrustedProxy.PrincipalHeader; got != "" {
			t.Errorf("PrincipalHeader: got %q, want empty", got)
		}
	})

	t.Run("an empty scopes header beside a principal header loads", func(t *testing.T) {
		t.Parallel()
		cfg, err := load(writeFixture(t, "mcp:\n  authorization:\n    trusted_proxy:\n"+
			"      principal_header: X-Forwarded-User\n      scopes_header: ''\n"))
		if err != nil {
			t.Fatalf("an empty scopes header was refused: %v", err)
		}
		if got := cfg.MCP.Authorization.TrustedProxy.ScopesHeader; got != "" {
			t.Errorf("ScopesHeader: got %q, want empty", got)
		}
	})

	for _, tc := range []struct {
		name    string
		content string
	}{
		{"an empty principal header", "mcp:\n  authorization:\n    trusted_proxy:\n      principal_header: ''\n"},
		{"an empty principal header beside a scopes header",
			"mcp:\n  authorization:\n    trusted_proxy:\n      principal_header: ''\n      scopes_header: X-Forwarded-Scopes\n"},
	} {
		tc := tc
		t.Run(tc.name+" is refused", func(t *testing.T) {
			t.Parallel()
			path := writeFixture(t, tc.content)
			_, err := load(path)
			if err == nil {
				t.Fatalf("expected the loader to refuse %s; it loaded", tc.name)
			}
			if !errors.Is(err, ErrConfigInvalid()) {
				t.Fatalf("the refusal must wrap ErrConfigInvalid; got %v", err)
			}
			if msg := err.Error(); !strings.Contains(msg, path) || !strings.Contains(msg, ": "+principal+": ") {
				t.Errorf("the refusal does not name the file and %s: %v", principal, err)
			}
		})
	}
}

// TestASigningEndpointFromTheEnvironmentIsNotHeldToTheFileShape pins the bound
// the schema descriptions and the configuration page state: the configuration
// file is validated before the environment is laid over it, so a signing endpoint
// supplied by AGGREGA_SIGNING_FULCIO_URL is not judged by the pattern the file's
// value is. The same scheme-less value is refused in the file and taken from the
// environment.
//
// It pins a limit rather than a feature, so that closing the limit is a change
// that also has to correct the sentences stating it, rather than one that leaves
// them describing a bound that no longer exists.
func TestASigningEndpointFromTheEnvironmentIsNotHeldToTheFileShape(t *testing.T) {
	t.Parallel()
	const schemeLess = "fulcio.example.com"

	t.Run("in the file it is refused", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		writeFile(t, filepath.Join(root, ".aggrega-config.yaml"), "workspace: {}\nsigning:\n  fulcio_url: "+schemeLess+"\n")
		_, err := Resolve(root, "", fakeEnv(nil))
		if err == nil {
			t.Fatal("expected a scheme-less endpoint in the file to be refused; it resolved")
		}
		if !errors.Is(err, ErrConfigInvalid()) || !strings.Contains(err.Error(), ": signing.fulcio_url: ") {
			t.Errorf("the refusal must wrap ErrConfigInvalid and name signing.fulcio_url; got %v", err)
		}
	})

	t.Run("from the environment it is taken as written", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		writeFile(t, filepath.Join(root, ".aggrega-config.yaml"), "workspace: {}\n")
		rw, err := Resolve(root, "", fakeEnv(map[string]string{"AGGREGA_SIGNING_FULCIO_URL": schemeLess}))
		if err != nil {
			t.Fatalf("resolve with the endpoint in the environment: %v", err)
		}
		if got := rw.Config().Signing.FulcioURL; got != schemeLess {
			t.Errorf("Signing.FulcioURL: got %q, want %q", got, schemeLess)
		}
	})
}
