// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// registrationKey is a public-key file whose body is one marker, so which file a
// registration read is read back from the entry it wrote.
func registrationKey(marker string) string {
	return "-----BEGIN PUBLIC KEY-----\n" + marker + "\n-----END PUBLIC KEY-----\n"
}

// TestARegistrationReadsARelativeKeyFileFromTheWorkspaceAndHoldsItThere drives
// aggrega/registryAdd over a real workspace with a key file and a trust root named
// each way a request can name them, and reads back the workspace configuration the
// registration wrote.
//
// An absolute value is a file a person picked in a dialog and is read where it
// names, wherever that is. A relative value is a file inside the workspace: it is
// read from the workspace root — never from the directory the daemon happens to run
// in, which is this test binary's own package directory and holds no key — and held
// there, so one climbing out of the root or leading out of it through a link is
// refused with nothing written. A relative trust root is recorded as the file it
// named in the workspace, so the entry names the same file whichever directory a
// later verification runs in. The first row is the control.
func TestARegistrationReadsARelativeKeyFileFromTheWorkspaceAndHoldsItThere(t *testing.T) {
	t.Parallel()

	rows := []struct {
		name string
		// params builds the request's trust-policy fields, given the workspace root
		// and the directory outside it.
		params func(root, outside string) map[string]any
		// want is a fragment the written entry carries, or the refusal's, compared as
		// written: nothing in it is read as a place, so a key's marker stays a marker.
		want string
		// wantPath, set on a row whose entry records a path, composes that fragment
		// from the workspace root and the directory outside it, in place of want.
		wantPath func(root, outside string) string
		refused  bool
	}{
		{
			name: "control: an absolute key file a dialog picked outside the workspace is read where it names",
			params: func(_, outside string) map[string]any {
				return map[string]any{"keyPath": filepath.Join(outside, "picked.pub")}
			},
			want: "PICKEDOUTSIDE",
		},
		{
			name:   "a relative key file is read from the workspace root",
			params: func(string, string) map[string]any { return map[string]any{"keyPath": "keys/workspace.pub"} },
			want:   "INTHEWORKSPACE",
		},
		{
			name:    "a relative key file reached through a link leading out is refused",
			params:  func(string, string) map[string]any { return map[string]any{"keyPath": "keys/linked.pub"} },
			want:    `registry add: keyPath must name a path inside the workspace this session opened`,
			refused: true,
		},
		{
			name:    "a relative key file climbing out of the workspace is refused",
			params:  func(string, string) map[string]any { return map[string]any{"keyPath": "../picked.pub"} },
			want:    `"../picked.pub" is absolute or climbs out of it`,
			refused: true,
		},
		{
			name: "a relative trust root is recorded as the file it names in the workspace",
			params: func(string, string) map[string]any {
				return map[string]any{"trustedRoot": "trust/root.json", "identities": []string{"ci@example.com=https://issuer.example.com"}}
			},
			wantPath: func(root, _ string) string { return "trusted_root: " + filepath.Join(root, "trust", "root.json") },
		},
		{
			name: "a relative trust root reached through a link leading out is refused",
			params: func(string, string) map[string]any {
				return map[string]any{"trustedRoot": "trust-link/root.json", "identities": []string{"ci@example.com=https://issuer.example.com"}}
			},
			want:    `"trust-link/root.json" leads out of it through a link`,
			refused: true,
		},
		{
			name: "an absolute trust root a dialog picked is recorded as picked",
			params: func(_, outside string) map[string]any {
				return map[string]any{"trustedRoot": filepath.Join(outside, "root.json"), "identities": []string{"ci@example.com=https://issuer.example.com"}}
			},
			wantPath: func(_, outside string) string { return "trusted_root: " + filepath.Join(outside, "root.json") },
		},
	}

	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			srv, _, wsPath := registryWriteFixture(t, "workspace: {}\n")
			root := filepath.Dir(wsPath)
			outside := filepath.Join(t.TempDir(), "outside")
			plantFile(t, filepath.Join(outside, "picked.pub"), registrationKey("PICKEDOUTSIDE"))
			plantFile(t, filepath.Join(outside, "secret.pub"), registrationKey("OUTSIDETHROUGHALINK"))
			plantFile(t, filepath.Join(outside, "root.json"), "{}\n")
			plantFile(t, filepath.Join(root, "keys", "workspace.pub"), registrationKey("INTHEWORKSPACE"))
			plantFile(t, filepath.Join(root, "trust", "root.json"), "{}\n")
			plantLink(t, filepath.Join(outside, "secret.pub"), filepath.Join(root, "keys", "linked.pub"))
			plantLink(t, outside, filepath.Join(root, "trust-link"))
			before := treeListing(t, outside)

			params := row.params(root, outside)
			params["host"] = "keys.example.test"
			params["workspace"] = true
			params["fetchTrust"] = false
			resp := callDaemonMethod(t, srv, "aggrega/registryAdd", params)

			if after := treeListing(t, outside); strings.Join(after, "\n") != strings.Join(before, "\n") {
				t.Errorf("the directory outside the workspace changed:\nbefore:\n  %s\nafter:\n  %s",
					strings.Join(before, "\n  "), strings.Join(after, "\n  "))
			}
			written, err := os.ReadFile(wsPath)
			if err != nil {
				t.Fatalf("read the workspace configuration: %v", err)
			}
			if row.refused {
				if resp.Error == nil {
					t.Fatalf("want the registration refused with %q; got result %s", row.want, resp.Result)
				}
				if resp.Error.Code != jsonRPCInvalidParams {
					t.Errorf("Code = %d; want %d (invalid params)", resp.Error.Code, jsonRPCInvalidParams)
				}
				if !strings.Contains(resp.Error.Message, row.want) {
					t.Errorf("the refusal reads %q; want it to carry %q", resp.Error.Message, row.want)
				}
				if strings.Contains(string(written), "keys.example.test") {
					t.Errorf("a refused registration wrote its entry:\n%s", written)
				}
				return
			}
			if resp.Error != nil {
				t.Fatalf("the registration was refused: %s", resp.Error.Message)
			}
			want := row.want
			if row.wantPath != nil {
				want = row.wantPath(root, outside)
			}
			if want == "" {
				t.Fatal("the row names no fragment for the written entry to carry")
			}
			if !strings.Contains(string(written), want) {
				t.Errorf("the entry written does not carry %q:\n%s", want, written)
			}
		})
	}
}
