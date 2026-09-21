// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/editops"
)

// editableAggregate is the document every assertion below is driven against.
//
// It deliberately carries what a full re-serialize destroys — a leading comment
// block, a hand-aligned trailing comment, blank lines inside a block, a literal
// block scalar indented deeper than its key, a single-quoted scalar beside an
// unquoted one, and a template expression that only survives as text — so a
// method that round-tripped the document through an encoder would fail the
// preservation assertions rather than pass them.
const editableAggregate = `# The storefront application.
#
# The ordering below is deliberate: the web tier reads first.
apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: storefront        # the module's own name
  version: 1.4.2
spec:
  parameters:
    db_size:
      type: string
      default: '10Gi'

  components:

    web:
      type: core/api-server:1.0.0
      description: |
        The public web tier.
          This line is indented inside a literal block.
      values:
        replicas: 3
      uses:
        database:
          primary:
            target: db

    # The data tier is intentionally last.
    db:
      type: core/postgres:2.1.0
      values:
        size: '{{ .parameters.db_size }}'
`

// editableRelease is the Release-side document the invariant refusal is
// driven against. The authoring schema gives a Release's component surface
// operator concerns only, so a request asking it to carry a developer value
// composes a document the schema refuses.
const editableRelease = `# The production deployment.
apiVersion: aggrega.ai/v1alpha1
kind: Release
metadata:
  name: storefront-prod
  environment: production
  version: 1.4.2
spec:
  aggregate: storefront
  manifest: prod-manifest
  components:
    web:
      infra:
        replicas: 5
`

// documentEditFixture stands a Server over a scratch workspace carrying one
// document, and returns the server and the document's workspace-relative name.
//
// The workspace is the session's own temporary directory, so every write these
// tests drive lands inside the test's own tree and nothing reaches the
// machine's configuration or caches.
func documentEditFixture(t *testing.T, name, content string) (*Server, string) {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o644); err != nil {
		t.Fatalf("write the fixture document: %v", err)
	}
	return newTestServer(t, root, nil, nil), name
}

// documentOnDisk reads the fixture document back from the session's workspace.
func documentOnDisk(t *testing.T, srv *Server, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(srv.workspaceRoot, name))
	if err != nil {
		t.Fatalf("read the document back: %v", err)
	}
	return string(b)
}

// TestEditDocumentAppliesEachEditOverTheDaemonAndTheResultRevalidates is the
// invocation-level proof for this method: each edit is dispatched over the
// production request path, the assertion is made against the bytes on disk
// rather than against a returned value, and the edited document is then run
// through the daemon's own validation.
//
// The re-validation is a second request rather than something folded into the
// edit, and driving it here is what makes the sequence a client performs the
// thing under test: apply, then ask the same session about the document it just
// changed. An edit that landed in a document the product would refuse to read
// would pass an assertion made on the write alone.
func TestEditDocumentAppliesEachEditOverTheDaemonAndTheResultRevalidates(t *testing.T) {
	for _, tc := range []struct {
		name   string
		params map[string]any
		lands  string
	}{
		{
			name: "a component instance is added",
			params: map[string]any{
				"edit":  "add-component",
				"node":  []string{"spec", "components"},
				"name":  "worker",
				"value": "core/api-server:1.0.0",
			},
			lands: "    worker:\n      type: core/api-server:1.0.0\n",
		},
		{
			name: "a feature is attached to a node",
			params: map[string]any{
				"edit":  "attach-feature",
				"node":  []string{"spec", "components", "web"},
				"name":  "tls",
				"value": "core/networking/tls:1.0.0",
			},
			lands: "          type: core/networking/tls:1.0.0\n",
		},
		{
			name: "an edge is wired inside a socket",
			params: map[string]any{
				"edit":   "wire-edge",
				"node":   []string{"spec", "components", "web"},
				"socket": "database",
				"name":   "replica",
				"value":  "db",
			},
			lands: "            target: db\n",
		},
		{
			name: "a value is set on a node",
			params: map[string]any{
				"edit":  "set-value",
				"node":  []string{"spec", "components", "db"},
				"name":  "shared_buffers",
				"value": "256MB",
			},
			lands: "        shared_buffers: 256MB\n",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv, doc := documentEditFixture(t, "storefront.aggregate.yaml", editableAggregate)
			params := map[string]any{"file": doc}
			for k, v := range tc.params {
				params[k] = v
			}

			resp := callDaemonMethod(t, srv, "aggrega/editDocument", params)
			var got documentEditResult
			decodeDaemonResult(t, resp, &got)

			if len(got.Written) != 1 || got.Written[0] != doc {
				t.Fatalf("written = %v; want the one document %q", got.Written, doc)
			}
			if got.Edit != tc.params["edit"] {
				t.Errorf("edit = %q; want the request's own value %q", got.Edit, tc.params["edit"])
			}
			if got.File != doc {
				t.Errorf("file = %q; want the request's own value %q", got.File, doc)
			}
			if len(got.Container) == 0 {
				t.Error("the answer names no container, so a caller cannot locate what it just added")
			}

			on := documentOnDisk(t, srv, doc)
			if !strings.Contains(on, tc.lands) {
				t.Fatalf("the entry did not land; want %q in\n--- document ---\n%s", tc.lands, on)
			}
			if on != got.Content {
				t.Error("content does not match the bytes on disk, so a caller reading one and the " +
					"other would be told two different things about the same edit")
			}

			// Every line the author wrote survives. A method that re-serialized
			// the document would lose the comments, the blank lines and the
			// literal block, and every assertion above would still pass.
			for _, line := range []string{
				"# The storefront application.",
				"  name: storefront        # the module's own name",
				"    # The data tier is intentionally last.",
				"          This line is indented inside a literal block.",
				"        size: '{{ .parameters.db_size }}'",
			} {
				if !strings.Contains(on, line) {
					t.Errorf("the edit did not preserve a line the author wrote: %q", line)
				}
			}

			// The re-validation, over the same session, against the document the
			// edit just wrote.
			vresp := callDaemonMethod(t, srv, "aggrega/validate", map[string]any{"file": doc})
			var v validateResult
			decodeDaemonResult(t, vresp, &v)
			if len(v.Diagnostics) != 0 {
				t.Fatalf("the edited document does not re-validate: %+v", v.Diagnostics)
			}
			if v.Checked != checkedDocument {
				t.Errorf("checked = %q; want %q, this being the answer a request naming no Release gets",
					v.Checked, checkedDocument)
			}
		})
	}
}

// TestEditDocumentRefusesAResultTheAuthoringSchemaWouldNotAdmit is the invariant
// proof on this surface: the refusal carries the condition a reader quotes, and
// the document is left byte-for-byte as it was.
//
// The byte comparison is the load-bearing half. A method that wrote the
// composed document and then reported a failure would satisfy an assertion made
// on the error alone, and the author's file would be left carrying something the
// product refuses to read.
func TestEditDocumentRefusesAResultTheAuthoringSchemaWouldNotAdmit(t *testing.T) {
	t.Parallel()
	srv, doc := documentEditFixture(t, "prod.release.yaml", editableRelease)

	resp := callDaemonMethod(t, srv, "aggrega/editDocument", map[string]any{
		"file":  doc,
		"edit":  "set-value",
		"node":  []string{"spec", "components", "web"},
		"name":  "replicas",
		"value": 9,
	})
	if resp.Error == nil {
		t.Fatal("a Release document was allowed to declare a developer value")
	}
	if !strings.Contains(resp.Error.Message, "E0275") {
		t.Errorf("the refusal does not carry the condition's short code, which is what a reader "+
			"quotes and looks up: %q", resp.Error.Message)
	}
	if !strings.Contains(resp.Error.Message, "nothing was written") {
		t.Errorf("the refusal does not say the document was left alone: %q", resp.Error.Message)
	}
	if !strings.Contains(resp.Error.Message, `unexpected property: "values"`) {
		t.Errorf("the refusal does not name what the schema refused, so a caller is not told what "+
			"to change: %q", resp.Error.Message)
	}
	if on := documentOnDisk(t, srv, doc); on != editableRelease {
		t.Errorf("a refused edit changed the document on disk\n--- document ---\n%s", on)
	}
}

// TestEditDocumentIsDeterministic drives the same request against two identical
// documents and requires the same bytes out.
//
// Two workspaces rather than one document edited twice: a second edit of the
// same document is a no-op the shared layer recognises, which would pass this
// assertion without the composition having run again. Each run here composes
// from the same starting bytes, which is the comparison the determinism claim
// is about.
func TestEditDocumentIsDeterministic(t *testing.T) {
	t.Parallel()

	request := func(doc string) map[string]any {
		return map[string]any{
			"file":  doc,
			"edit":  "add-component",
			"node":  []string{"spec", "components"},
			"name":  "worker",
			"value": "core/api-server:1.0.0",
		}
	}

	srvA, docA := documentEditFixture(t, "storefront.aggregate.yaml", editableAggregate)
	decodeDaemonResult(t, callDaemonMethod(t, srvA, "aggrega/editDocument", request(docA)),
		&documentEditResult{})
	first := documentOnDisk(t, srvA, docA)

	srvB, docB := documentEditFixture(t, "storefront.aggregate.yaml", editableAggregate)
	decodeDaemonResult(t, callDaemonMethod(t, srvB, "aggrega/editDocument", request(docB)),
		&documentEditResult{})
	second := documentOnDisk(t, srvB, docB)

	if first != second {
		t.Errorf("the same edit produced different bytes\n--- first ---\n%s\n--- second ---\n%s",
			first, second)
	}
	if first == editableAggregate {
		t.Fatal("neither run changed the document, so the comparison above compared nothing")
	}
}

// TestEditDocumentUnderReportWritesNothing pins the reporting delivery: the
// content a write would have produced comes back, the file is untouched, and
// written is an empty list rather than an absent field.
//
// It also pins the default, which is the half most easily got wrong. A caller
// that states no delivery gets the write, because the operation's product is an
// edited document and a surface that produces one owes it — so a method that
// defaulted to reporting would leave every caller that had not thought about
// delivery with nothing on disk.
func TestEditDocumentUnderReportWritesNothing(t *testing.T) {
	t.Parallel()

	base := map[string]any{
		"edit":  "add-component",
		"node":  []string{"spec", "components"},
		"name":  "worker",
		"value": "core/api-server:1.0.0",
	}

	for _, tc := range []struct {
		name   string
		dryRun any
		writes bool
	}{
		{"a dry run writes nothing", true, false},
		{"an explicit false writes", false, true},
		{"an absent delivery writes", nil, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv, doc := documentEditFixture(t, "storefront.aggregate.yaml", editableAggregate)
			params := map[string]any{"file": doc}
			for k, v := range base {
				params[k] = v
			}
			if tc.dryRun != nil {
				params["dryRun"] = tc.dryRun
			}

			var got documentEditResult
			decodeDaemonResult(t, callDaemonMethod(t, srv, "aggrega/editDocument", params), &got)

			if !strings.Contains(got.Content, "    worker:\n") {
				t.Errorf("content does not carry the document the edit produced:\n%s", got.Content)
			}
			if got.Written == nil {
				t.Error("written is absent rather than an empty array")
			}

			on := documentOnDisk(t, srv, doc)
			switch {
			case tc.writes && on == editableAggregate:
				t.Error("the document on disk is unchanged, so nothing was written")
			case tc.writes && len(got.Written) != 1:
				t.Errorf("written = %v; want the one document", got.Written)
			case !tc.writes && on != editableAggregate:
				t.Errorf("a reporting delivery changed the document on disk\n--- document ---\n%s", on)
			case !tc.writes && len(got.Written) != 0:
				t.Errorf("a reporting delivery reported writing %v", got.Written)
			}
		})
	}
}

// TestEditDocumentRefusesARequestThatNamesNoEdit drives this surface's own
// required-field refusals, on this surface's spellings.
//
// The refusals name request fields and never a command-line flag: a caller here
// cannot act on a flag it has no way to send. The row naming an edit that is not
// one is the bound made visible — the set is closed, so an unrecognised value is
// refused rather than interpreted, and the refusal lists what may be named.
func TestEditDocumentRefusesARequestThatNamesNoEdit(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name   string
		params map[string]any
		want   string
	}{
		{"no file", map[string]any{"edit": "add-component"}, "file is required"},
		{"no edit", map[string]any{"file": "storefront.aggregate.yaml"}, "edit is required"},
		{"neither", map[string]any{}, "file is required"},
		{
			"an edit that is not one of the four",
			map[string]any{
				"file": "storefront.aggregate.yaml",
				"edit": "delete-component",
				"node": []string{"spec", "components"},
				"name": "web",
			},
			"not one this capability applies",
		},
		{
			"no node path",
			map[string]any{
				"file":  "storefront.aggregate.yaml",
				"edit":  "add-component",
				"name":  "worker",
				"value": "core/api-server:1.0.0",
			},
			"no node was named",
		},
		{
			"a document outside the workspace",
			map[string]any{
				"file":  "../escape.aggregate.yaml",
				"edit":  "add-component",
				"node":  []string{"spec", "components"},
				"name":  "worker",
				"value": "core/api-server:1.0.0",
			},
			"not inside the workspace",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv, _ := documentEditFixture(t, "storefront.aggregate.yaml", editableAggregate)

			resp := callDaemonMethod(t, srv, "aggrega/editDocument", tc.params)
			if resp.Error == nil {
				t.Fatal("aggrega/editDocument accepted a request that addresses no edit")
			}
			if resp.Error.Code != jsonRPCInvalidParams {
				t.Errorf("refusal code = %d; want %d, the invalid-params code every request-shape "+
					"refusal here answers with", resp.Error.Code, jsonRPCInvalidParams)
			}
			if !strings.Contains(resp.Error.Message, tc.want) {
				t.Errorf("refusal = %q; want it to contain %q", resp.Error.Message, tc.want)
			}
			if strings.Contains(resp.Error.Message, "--") {
				t.Errorf("the refusal names a command-line flag a request cannot send: %q",
					resp.Error.Message)
			}
			if on := documentOnDisk(t, srv, "storefront.aggregate.yaml"); on != editableAggregate {
				t.Errorf("a refused request changed the document on disk\n--- document ---\n%s", on)
			}
		})
	}
}

// TestEditDocumentRefusalNamesEveryAdmissibleEdit pins that both refusals about
// which edit was named list the set from the shared layer's own enumeration
// rather than from a literal here.
//
// Without it a surface could advertise a set the layer does not apply — telling
// a caller to name something that would then be refused, or omitting something
// it may name.
//
// A caller that named no edit and a caller that named one outside the set are
// owed the same list, and the second is the one that most needs it: it mistyped,
// so it is the caller with something to correct. They are refused at different
// places — the first by this surface, the second by the shared layer — so both
// are driven rather than one standing in for the other.
func TestEditDocumentRefusalNamesEveryAdmissibleEdit(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name   string
		params map[string]any
	}{
		{"no edit named", map[string]any{"file": "storefront.aggregate.yaml"}},
		{
			"an edit outside the set",
			map[string]any{
				"file": "storefront.aggregate.yaml",
				"edit": "delete-component",
				"node": []string{"spec", "components"},
				"name": "web",
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv, _ := documentEditFixture(t, "storefront.aggregate.yaml", editableAggregate)

			resp := callDaemonMethod(t, srv, "aggrega/editDocument", tc.params)
			if resp.Error == nil {
				t.Fatal("a request that addresses no edit was accepted")
			}
			kinds := editops.Kinds()
			if len(kinds) == 0 {
				t.Fatal("the shared layer enumerates no edits, so the loop below asserts nothing")
			}
			for _, kind := range kinds {
				if !strings.Contains(resp.Error.Message, string(kind)) {
					t.Errorf("the refusal does not name the %q edit: %q", kind, resp.Error.Message)
				}
			}
		})
	}
}

// TestEditDocumentDropsTheSessionsViewOfTheEditedDocument pins the re-read: a
// document the session had already parsed is answered about from the bytes the
// edit produced, not from the version the cache was holding.
//
// The cache is seeded by validating first, which is how a real session comes to
// hold a document at all. Asserting the entry through the session's own parse
// cache is what distinguishes a dropped entry from one that merely happens to
// agree.
func TestEditDocumentDropsTheSessionsViewOfTheEditedDocument(t *testing.T) {
	t.Parallel()
	srv, doc := documentEditFixture(t, "storefront.aggregate.yaml", editableAggregate)

	// Seed the session's view of the document.
	var seeded validateResult
	decodeDaemonResult(t, callDaemonMethod(t, srv, "aggrega/validate",
		map[string]any{"file": doc}), &seeded)
	abs := srv.canonicalFile(doc)
	if _, held := srv.parseCache.Get(abs); !held {
		t.Fatal("the validation did not seed the parse cache, so this test would prove nothing")
	}

	decodeDaemonResult(t, callDaemonMethod(t, srv, "aggrega/editDocument", map[string]any{
		"file":  doc,
		"edit":  "add-component",
		"node":  []string{"spec", "components"},
		"name":  "worker",
		"value": "core/api-server:1.0.0",
	}), &documentEditResult{})

	if entry, held := srv.parseCache.Get(abs); held && !strings.Contains(string(entry.content), "worker:") {
		t.Error("the session is still holding the document as it stood before the edit, so the next " +
			"request about it would be answered from bytes the edit replaced")
	}
}
