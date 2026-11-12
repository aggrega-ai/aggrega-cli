// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package serve

import (
	"bytes"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/yamlpos"
)

// TestAFileAddressIsReadAsThePathItNames pins which values are read as a file
// address and what each one names, in both path spellings the host may use.
//
// The rows that are NOT addresses matter as much as the rows that are: a value
// that is not an address must reach the path resolution exactly as it arrived,
// so the address form changes the meaning of no plain path — the single-slash
// form and a relative path whose first segment merely resembles the scheme are
// both plain paths here.
func TestAFileAddressIsReadAsThePathItNames(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name         string
		value        string
		driveLetters bool
		want         string
		wantOK       bool
	}{
		{"an address with an empty authority", "file:///ws/proj/aggregate.yaml", false, "/ws/proj/aggregate.yaml", true},
		{"an address naming this machine by name", "file://localhost/ws/proj/aggregate.yaml", false, "/ws/proj/aggregate.yaml", true},
		{"percent-escapes are decoded", "file:///ws/a%20proj/%23one%25.yaml", false, "/ws/a proj/#one%.yaml", true},
		{"the scheme is matched without regard to case", "FILE:///ws/proj/aggregate.yaml", false, "/ws/proj/aggregate.yaml", true},
		{"a drive letter opens the path on a drive-letter system", "file:///c%3A/ws/aggregate.yaml", true, "c:/ws/aggregate.yaml", true},
		{"an unescaped drive letter opens it too", "file:///D:/ws/aggregate.yaml", true, "D:/ws/aggregate.yaml", true},
		{"another machine is its network share on a drive-letter system", "file://server/share/aggregate.yaml", true, "//server/share/aggregate.yaml", true},
		{"another machine names nothing on any other system", "file://server/share/aggregate.yaml", false, "", false},
		{"a query is no part of a document's address", "file:///ws/a.yaml?x=1", false, "", false},
		{"a fragment is no part of a document's address", "file:///ws/a.yaml#top", false, "", false},
		{"credentials are no part of a document's address", "file://someone@localhost/ws/a.yaml", false, "", false},
		{"a port is no part of a document's address", "file://localhost:8080/ws/a.yaml", false, "", false},
		{"an address naming no path names nothing", "file://localhost", false, "", false},
		{"a malformed escape is not an address", "file:///ws/%zz.yaml", false, "", false},
		{"an absolute path is not an address", "/ws/proj/aggregate.yaml", false, "", false},
		{"a relative path is not an address", "aggregate.yaml", false, "", false},
		{"the single-slash form is a relative path here", "file:/ws/a.yaml", false, "", false},
		{"another scheme is not a file address", "https://example.invalid/aggregate.yaml", false, "", false},
		{"the empty string is not an address", "", false, "", false},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, ok := addressPath(tc.value, tc.driveLetters)
			if got != tc.want || ok != tc.wantOK {
				t.Errorf("addressPath(%q, driveLetters=%v) = (%q, %v); want (%q, %v)",
					tc.value, tc.driveLetters, got, ok, tc.want, tc.wantOK)
			}
		})
	}
}

// TestCanonicalFileKeysAnAddressAsThePathItNames pins the key a file address
// reaches the per-file stores under: the key its path reaches them under. Every
// row compares against the key canonicalFile gives the plain path, so a document
// validated under one spelling is found under the other.
//
// The plain-path rows are the negative control. Each is a value that is not an
// address, and each must be keyed exactly as the path resolution alone keys it —
// the relative path onto the root, the climb left as it stands, and a relative
// name that merely opens with the scheme joined onto the root like any other.
func TestCanonicalFileKeysAnAddressAsThePathItNames(t *testing.T) {
	t.Parallel()
	root := filepath.Join(t.TempDir(), "a workspace")
	abs := filepath.Join(root, "aggregate.yaml")
	address := fileAddressOf(abs)
	for _, tc := range []struct {
		name  string
		value string
		want  string
	}{
		{"the address of a document inside the workspace", address, abs},
		{"the address naming this machine by name", "file://localhost" + (&url.URL{Path: filepath.ToSlash(abs)}).EscapedPath(), abs},
		{"the address of a document outside the workspace", fileAddressOf(filepath.Join(filepath.Dir(root), "outside.yaml")), filepath.Join(filepath.Dir(root), "outside.yaml")},
		{"the absolute path the address names", abs, abs},
		{"a workspace-relative path still resolves onto the root", "aggregate.yaml", abs},
		{"a relative path climbing out is still left as it stands", "../outside.yaml", "../outside.yaml"},
		{"a relative name shaped like the scheme is still a relative path", "file:/aggregate.yaml", filepath.Join(root, "file:", "aggregate.yaml")},
		{"an address naming another machine is still read as the relative path it spells", "file://elsewhere/aggregate.yaml", filepath.Join(root, "file:", "elsewhere", "aggregate.yaml")},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s := &Server{workspaceRoot: root}
			if got := s.canonicalFile(tc.value); got != tc.want {
				t.Errorf("canonicalFile(%q) = %q; want %q", tc.value, got, tc.want)
			}
		})
	}
}

// fileAddressOf returns the file address a language-server client sends for the
// document at path: the scheme, an empty authority and the percent-escaped path.
func fileAddressOf(path string) string {
	return (&url.URL{Scheme: "file", Path: filepath.ToSlash(path)}).String()
}

// addressFixture stands the parameter-type-mismatch workspace up under a
// directory whose name carries a character an address must escape, so every
// request below exercises the decoding of percent-escapes as well as of the
// scheme.
func addressFixture(t *testing.T) (srv *Server, path, address string) {
	t.Helper()
	ws := filepath.Join(t.TempDir(), "a workspace")
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatalf("mkdir workspace: %v", err)
	}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	fixtureDir := filepath.Join(cwd, "testdata", "tier4", "parameter-type-mismatch")
	for _, name := range []string{"release.yaml", "manifest.yaml", "aggregate.yaml"} {
		data, rerr := os.ReadFile(filepath.Join(fixtureDir, name))
		if rerr != nil {
			t.Fatalf("read fixture %s: %v", name, rerr)
		}
		if werr := os.WriteFile(filepath.Join(ws, name), data, 0o644); werr != nil {
			t.Fatalf("write fixture %s: %v", name, werr)
		}
	}
	path = filepath.Join(ws, "aggregate.yaml")
	return newTestServer(t, ws, nil, nil), path, fileAddressOf(path)
}

// requestOverTheWire drives one request through the production write path.
func requestOverTheWire(t *testing.T, srv *Server, method string, params any) jsonRPCResponse {
	t.Helper()
	var in, out bytes.Buffer
	in.Write(framedRequest(1, method, params))
	return runOneRequest(t, srv, &in, &out)
}

// TestAPerDocumentRequestFindsTheDocumentUnderItsAddressOrItsPath drives every
// request that reads the parse cache a validation seeds, naming the document by
// one spelling after a validation named it by the other, in both directions.
//
// Each row asserts two things. The request is answered rather than refused with
// the file-not-validated code — which is exactly what an address draws when it
// is not decoded, so that half is the planted defect's tripwire. And the answer
// equals the one the same request gets naming the document the way the
// validation did, so an address reaches the document and not merely some
// document.
//
// resolve's cursor sits on the entity's own name, whose answer is a location in
// the requested document spelled as the request spelled it, so that row compares
// the range the location covers.
func TestAPerDocumentRequestFindsTheDocumentUnderItsAddressOrItsPath(t *testing.T) {
	t.Parallel()
	onName := yamlpos.Position{Line: 3, Character: 9}
	onKind := yamlpos.Position{Line: 1, Character: 6}
	type project func(t *testing.T, result json.RawMessage) any
	whole := func(t *testing.T, result json.RawMessage) any {
		t.Helper()
		var v any
		if err := json.Unmarshal(result, &v); err != nil {
			t.Fatalf("unmarshal result: %v", err)
		}
		return v
	}
	rangeOnly := func(t *testing.T, result json.RawMessage) any {
		t.Helper()
		var r resolveResult
		if err := json.Unmarshal(result, &r); err != nil {
			t.Fatalf("unmarshal resolve result: %v", err)
		}
		if r.Location == nil {
			t.Fatal("resolve on the entity's own name answered no location, so the comparison below would prove nothing")
		}
		return r.Location.Range
	}
	rows := []struct {
		method  string
		params  func(file string) any
		project project
	}{
		{"aggrega/completion", func(f string) any { return completionParams{URI: f, Position: onKind} }, whole},
		{"aggrega/hover", func(f string) any { return hoverParams{URI: f, Position: onName} }, whole},
		{"aggrega/resolve", func(f string) any { return resolveParams{URI: f, Position: onName} }, rangeOnly},
		{"aggrega/findReferences", func(f string) any { return findReferencesParams{URI: f, Position: onName} }, whole},
		{"aggrega/findImplementations", func(f string) any { return findImplementationsParams{URI: f, Position: onName} }, whole},
		{"aggrega/diagnostics", func(f string) any { return diagnosticsParams{File: f} }, whole},
		{"aggrega/insertionPoint", func(f string) any { return insertionPointParams{File: f, Container: []string{"spec", "parameters"}} }, whole},
	}
	for _, row := range rows {
		for _, byAddress := range []bool{true, false} {
			row, byAddress := row, byAddress
			name := row.method + " by the address after a validation by the path"
			if !byAddress {
				name = row.method + " by the path after a validation by the address"
			}
			t.Run(name, func(t *testing.T) {
				t.Parallel()
				srv, path, address := addressFixture(t)
				validatedAs, askedAs := path, address
				if !byAddress {
					validatedAs, askedAs = address, path
				}
				content, err := os.ReadFile(path)
				if err != nil {
					t.Fatalf("read aggregate: %v", err)
				}
				if resp := requestOverTheWire(t, srv, "aggrega/validate", validateParams{
					File: validatedAs, Content: string(content), Release: "release.yaml",
				}); resp.Error != nil {
					t.Fatalf("validate %q: unexpected envelope %+v", validatedAs, resp.Error)
				}

				asked := requestOverTheWire(t, srv, row.method, row.params(askedAs))
				if asked.Error != nil {
					t.Fatalf("%s naming %q was refused with code %d (%s); want an answer, because %q validated the same document",
						row.method, askedAs, asked.Error.Code, asked.Error.Message, validatedAs)
				}
				same := requestOverTheWire(t, srv, row.method, row.params(validatedAs))
				if same.Error != nil {
					t.Fatalf("%s naming %q, the spelling the validation used, was refused: %+v", row.method, validatedAs, same.Error)
				}
				if got, want := row.project(t, asked.Result), row.project(t, same.Result); !reflect.DeepEqual(got, want) {
					t.Errorf("%s answered differently for the two spellings of one document\n %q -> %v\n %q -> %v",
						row.method, askedAs, got, validatedAs, want)
				}
			})
		}
	}
}

// TestAnInvalidationByOneSpellingDropsTheDocumentValidatedByTheOther pins the
// write side of the same identity: an invalidation drops the document it names
// however it spells it, so the next read under the spelling the validation used
// is refused with the file-not-validated code rather than served the dropped
// state.
func TestAnInvalidationByOneSpellingDropsTheDocumentValidatedByTheOther(t *testing.T) {
	t.Parallel()
	for _, byAddress := range []bool{true, false} {
		byAddress := byAddress
		name := "invalidated by the address after a validation by the path"
		if !byAddress {
			name = "invalidated by the path after a validation by the address"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			srv, path, address := addressFixture(t)
			validatedAs, invalidatedAs := path, address
			if !byAddress {
				validatedAs, invalidatedAs = address, path
			}
			content, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read aggregate: %v", err)
			}
			if resp := requestOverTheWire(t, srv, "aggrega/validate", validateParams{
				File: validatedAs, Content: string(content),
			}); resp.Error != nil {
				t.Fatalf("validate %q: unexpected envelope %+v", validatedAs, resp.Error)
			}
			if resp := requestOverTheWire(t, srv, "aggrega/invalidate", invalidateParams{
				File: invalidatedAs, Granularity: "file",
			}); resp.Error != nil {
				t.Fatalf("invalidate %q: unexpected envelope %+v", invalidatedAs, resp.Error)
			}
			resp := requestOverTheWire(t, srv, "aggrega/diagnostics", diagnosticsParams{File: validatedAs})
			if resp.Error == nil || resp.Error.Code != errFileNotValidated {
				t.Errorf("diagnostics for %q after invalidating %q answered %+v; want code %d",
					validatedAs, invalidatedAs, resp.Error, errFileNotValidated)
			}
		})
	}
}
