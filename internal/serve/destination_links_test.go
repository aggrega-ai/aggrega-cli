// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"bytes"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/pluginpath"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// destinationLinksLayout is one workspace root laid beside a directory outside
// it, with a link of each kind a destination can meet: a directory link leading
// out (shared), a directory link that stays inside (inner), a link to a document
// outside (linked-entity.yaml) and one to a document inside (alias-entity.yaml), a
// link to a plugin executable outside (linked-guest.wasm), and a link leading
// nowhere whose far end would sit in the outside directory (dangling). selected
// holds the files a request may name from anywhere — a module, an overlay and an
// import source — so a row refused for its destination has nothing else at fault.
type destinationLinksLayout struct {
	root, outside, selected string
}

func layDestinationLinks(t *testing.T) destinationLinksLayout {
	t.Helper()
	l := destinationLinksLayout{root: t.TempDir(), outside: t.TempDir(), selected: t.TempDir()}

	plantFile(t, filepath.Join(l.outside, "shared", "existing", "kept.txt"), "kept\n")
	plantFile(t, filepath.Join(l.outside, "shared", "doc.yaml"), linkTestComponent("shared-only-component"))
	plantFile(t, filepath.Join(l.outside, "outside-entity.yaml"), linkTestComponent("outside-only-component"))
	plantFile(t, filepath.Join(l.outside, "guest.wasm"), "not a module")

	plantFile(t, filepath.Join(l.root, "entity.yaml"), linkTestComponent("inside-component"))
	plantFile(t, filepath.Join(l.root, "guest.wasm"), "not a module")
	plantDir(t, filepath.Join(l.root, "real"))
	writeRelease(t, l.root, "plain-release.yaml", "release/plain")

	plantLink(t, filepath.Join(l.outside, "shared"), filepath.Join(l.root, "shared"))
	plantLink(t, filepath.Join(l.root, "real"), filepath.Join(l.root, "inner"))
	plantLink(t, filepath.Join(l.outside, "outside-entity.yaml"), filepath.Join(l.root, "linked-entity.yaml"))
	plantLink(t, filepath.Join(l.root, "entity.yaml"), filepath.Join(l.root, "alias-entity.yaml"))
	plantLink(t, filepath.Join(l.outside, "guest.wasm"), filepath.Join(l.root, "linked-guest.wasm"))
	plantLink(t, filepath.Join(l.outside, "missing"), filepath.Join(l.root, "dangling"))

	plantFile(t, filepath.Join(l.selected, "built.wasm"), "not a module")
	plantFile(t, filepath.Join(l.selected, "overlay.yaml"), "metadata: {}\n")
	plantFile(t, filepath.Join(l.selected, "flow.json"), "{\"nodes\": []}\n")
	return l
}

// callPastPushes drives one request through the production dispatch and returns
// its answer, reading past any diagnostics a request publishes before it answers
// — a build refused at the lockfile gate withdraws that file's drift finding
// first.
func callPastPushes(t *testing.T, srv *Server, method string, params any) jsonRPCResponse {
	t.Helper()
	var in, out bytes.Buffer
	in.Write(framedRequest(1, method, params))
	resp, _ := runOneRequestPastPushes(t, srv, &in, &out)
	return resp
}

// treeListing lists every entry under dir, links not followed, with what each is
// and how large, so two listings taken before and after a request differ exactly
// where something was written.
func treeListing(t *testing.T, dir string) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, ierr := d.Info()
		if ierr != nil {
			return ierr
		}
		rel, _ := filepath.Rel(dir, path)
		out = append(out, fmt.Sprintf("%s %s %d", rel, info.Mode().Type(), info.Size()))
		return nil
	})
	if err != nil {
		t.Fatalf("list %s: %v", dir, err)
	}
	return out
}

// TestADestinationARequestNamesIsJudgedByItsDeepestExistingPart drives every
// request that writes where its request names — the catalog generation's output
// directory, the import's, the build's output parent, the entity a pin writes its
// lock beside, the plugin alignment's entity and module, and the structured edit's
// document — over one real root, and reads back whether each was refused for its
// destination, and what the directory outside the root holds afterwards.
//
// Each row carries one destination at fault, or none. A request admitted past the
// destination check reaches its next stage, which is how admission is read: the
// generation and the alignment reach the module's bytes and refuse them, the
// build reaches the lockfile gate, the import reaches format selection, the pin
// answers that a Component is not pinnable, and the edit refuses the edit it was
// named. None of those writes anything, so the outside directory is listed before
// and after every row and must not have changed. The first row is the control.
func TestADestinationARequestNamesIsJudgedByItsDeepestExistingPart(t *testing.T) {
	t.Parallel()

	const (
		admittedToTheModule  = "wasm binary magic mismatch"
		admittedToTheFormat  = "was not recognised from its shape"
		admittedToTheEdit    = "the edit named is not one this capability applies"
		admittedToTheBuild   = "admitted, and refused by the lockfile gate"
		admittedToThePin     = "admitted, and answered as not pinnable"
		leadsOutOfTheSession = "leads out of it through a link"
		editLeadsOut         = "the document named leads outside the workspace through a link"
	)
	generation := func(outputDir string) func(destinationLinksLayout) map[string]any {
		return func(l destinationLinksLayout) map[string]any {
			return map[string]any{
				"plugin":    filepath.Join(l.selected, "built.wasm"),
				"overlay":   filepath.Join(l.selected, "overlay.yaml"),
				"outputDir": outputDir,
			}
		}
	}
	fixed := func(params map[string]any) func(destinationLinksLayout) map[string]any {
		return func(destinationLinksLayout) map[string]any { return params }
	}

	rows := []struct {
		name   string
		method string
		params func(destinationLinksLayout) map[string]any
		// want is an admission marker above, or a fragment the refusal carries.
		want string
		// opening is how the refusal opens, where the row pins it.
		opening string
	}{
		{
			name:   "a generation into a directory not made yet inside the root is admitted",
			method: "aggrega/pluginGenCatalog",
			params: generation("generated"),
			want:   admittedToTheModule,
		},
		{
			name:    "a generation into a linked directory leading out is refused, quoting the value sent",
			method:  "aggrega/pluginGenCatalog",
			params:  generation("shared"),
			want:    `"shared" ` + leadsOutOfTheSession,
			opening: `plugin gen-catalog: outputDir must name a path inside the workspace this session opened`,
		},
		{
			name:   "a generation into an existing directory inside a linked directory leading out is refused",
			method: "aggrega/pluginGenCatalog",
			params: generation("shared/existing"),
			want:   `"shared/existing" ` + leadsOutOfTheSession,
		},
		{
			name:   "a generation into a directory not made yet beneath a linked directory leading out is refused",
			method: "aggrega/pluginGenCatalog",
			params: generation("shared/new"),
			want:   `"shared/new" ` + leadsOutOfTheSession,
		},
		{
			name:   "a generation two directories not made yet beneath a linked directory leading out is refused",
			method: "aggrega/pluginGenCatalog",
			params: generation("shared/new/deeper"),
			want:   `"shared/new/deeper" ` + leadsOutOfTheSession,
		},
		{
			name:   "a generation into a directory not made yet beneath a linked directory inside the root is admitted",
			method: "aggrega/pluginGenCatalog",
			params: generation("inner/new"),
			want:   admittedToTheModule,
		},
		{
			name:   "a generation beneath a link leading nowhere is judged by the directory holding the link",
			method: "aggrega/pluginGenCatalog",
			params: generation("dangling/new"),
			want:   admittedToTheModule,
		},
		{
			name:   "an import into a directory not made yet inside the root is admitted",
			method: "aggrega/import",
			params: func(l destinationLinksLayout) map[string]any {
				return map[string]any{"source": filepath.Join(l.selected, "flow.json"), "outputDir": "drafts"}
			},
			want: admittedToTheFormat,
		},
		{
			name:   "an import into a directory not made yet beneath a linked directory leading out is refused",
			method: "aggrega/import",
			params: func(l destinationLinksLayout) map[string]any {
				return map[string]any{"source": filepath.Join(l.selected, "flow.json"), "outputDir": "shared/drafts"}
			},
			want:    `"shared/drafts" ` + leadsOutOfTheSession,
			opening: `import: outputDir must name a path inside the workspace this session opened`,
		},
		{
			name:   "a build whose output parent is a linked directory inside the root is admitted",
			method: "aggrega/build",
			params: fixed(map[string]any{"release": "plain-release.yaml", "outputParent": "inner"}),
			want:   admittedToTheBuild,
		},
		{
			name:    "a build whose output parent is a linked directory leading out is refused",
			method:  "aggrega/build",
			params:  fixed(map[string]any{"release": "plain-release.yaml", "outputParent": "shared"}),
			want:    `"shared" ` + leadsOutOfTheSession,
			opening: `build: outputParent must name a path inside the workspace this session opened`,
		},
		{
			name:   "a build whose output parent is not made yet beneath a linked directory leading out is refused",
			method: "aggrega/build",
			params: fixed(map[string]any{"release": "plain-release.yaml", "outputParent": "shared/site"}),
			want:   `"shared/site" ` + leadsOutOfTheSession,
		},
		{
			name:   "a pin of an entity inside the root is admitted",
			method: "aggrega/pin",
			params: fixed(map[string]any{"entity": "entity.yaml"}),
			want:   admittedToThePin,
		},
		{
			name:   "a pin of an entity reached through a link inside the root is admitted",
			method: "aggrega/pin",
			params: fixed(map[string]any{"entity": "alias-entity.yaml"}),
			want:   admittedToThePin,
		},
		{
			name:    "a pin of an entity reached through a link leading out is refused",
			method:  "aggrega/pin",
			params:  fixed(map[string]any{"entity": "linked-entity.yaml"}),
			want:    `"linked-entity.yaml" ` + leadsOutOfTheSession,
			opening: `pin: entity must name a path inside the workspace this session opened`,
		},
		{
			name:   "an alignment of an entity and a module inside the root is admitted",
			method: "aggrega/pluginSync",
			params: fixed(map[string]any{"entity": "entity.yaml", "plugin": "guest.wasm"}),
			want:   admittedToTheModule,
		},
		{
			name:    "an alignment of an entity reached through a link leading out is refused",
			method:  "aggrega/pluginSync",
			params:  fixed(map[string]any{"entity": "linked-entity.yaml", "plugin": "guest.wasm"}),
			want:    `"linked-entity.yaml" ` + leadsOutOfTheSession,
			opening: `plugin sync: entity must name a path inside the workspace this session opened`,
		},
		{
			name:    "an alignment reading a module reached through a link leading out is refused",
			method:  "aggrega/pluginSync",
			params:  fixed(map[string]any{"entity": "entity.yaml", "plugin": "linked-guest.wasm"}),
			want:    `"linked-guest.wasm" ` + leadsOutOfTheSession,
			opening: `plugin sync: plugin must name a path inside the workspace this session opened`,
		},
		{
			name:   "an edit of a document inside the root is admitted",
			method: "aggrega/editDocument",
			params: fixed(map[string]any{"file": "entity.yaml", "edit": "no-such-edit"}),
			want:   admittedToTheEdit,
		},
		{
			name:   "an edit of a document reached through a link inside the root is admitted",
			method: "aggrega/editDocument",
			params: fixed(map[string]any{"file": "alias-entity.yaml", "edit": "no-such-edit"}),
			want:   admittedToTheEdit,
		},
		{
			name:   "an edit of a document reached through a link leading out is refused, quoting the value sent",
			method: "aggrega/editDocument",
			params: fixed(map[string]any{"file": "linked-entity.yaml", "edit": "no-such-edit"}),
			want:   `editDocument: ` + editLeadsOut + `, and an edit is only applied to a document the workspace carries: "linked-entity.yaml"`,
		},
		{
			name:   "an edit of a document inside a linked directory leading out is refused",
			method: "aggrega/editDocument",
			params: fixed(map[string]any{"file": "shared/doc.yaml", "edit": "no-such-edit"}),
			want:   editLeadsOut,
		},
		{
			name:   "an edit naming by its absolute path a link leading out is refused",
			method: "aggrega/editDocument",
			params: func(l destinationLinksLayout) map[string]any {
				return map[string]any{"file": filepath.Join(l.root, "linked-entity.yaml"), "edit": "no-such-edit"}
			},
			want: editLeadsOut,
		},
	}

	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			l := layDestinationLinks(t)
			srv := linkedFilesServer(t, l.root, "workspace: {}\n")
			srv.resolveImportPlugin = func(*workspaceconfig.ResolvedWorkspace, string, map[string]pluginpath.ColocatedDir) (pluginpath.ResolvedPlugin, error) {
				return pluginpath.ResolvedPlugin{}, fmt.Errorf("this fixture resolves no importer module")
			}
			before := treeListing(t, l.outside)

			resp := callPastPushes(t, srv, row.method, row.params(l))

			if after := treeListing(t, l.outside); strings.Join(after, "\n") != strings.Join(before, "\n") {
				t.Errorf("the directory outside the root changed:\nbefore:\n  %s\nafter:\n  %s",
					strings.Join(before, "\n  "), strings.Join(after, "\n  "))
			}
			switch row.want {
			case admittedToTheBuild:
				if resp.Error == nil || resp.Error.Code != errLockfileAbsent {
					t.Fatalf("want the build admitted and refused by the lockfile gate; got %+v (result %s)", resp.Error, resp.Result)
				}
				return
			case admittedToThePin:
				var got pinResult
				decodeDaemonResult(t, resp, &got)
				if got.Pinnable || got.Kind != "Component" {
					t.Fatalf("want the pin admitted and answered as a Component that is not pinnable; got %+v", got)
				}
				return
			}
			if resp.Error == nil {
				t.Fatalf("want an answer carrying %q; got result %s", row.want, resp.Result)
			}
			if !strings.Contains(resp.Error.Message, row.want) {
				t.Errorf("the answer reads %q; want it to carry %q", resp.Error.Message, row.want)
			}
			if row.opening != "" && !strings.Contains(resp.Error.Message, row.opening) {
				t.Errorf("the refusal reads %q; want it to open %q", resp.Error.Message, row.opening)
			}
			if strings.Contains(resp.Error.Message, l.outside) {
				t.Errorf("the answer names %s, the place a link leads to: %q", l.outside, resp.Error.Message)
			}
			admitted := row.want == admittedToTheModule || row.want == admittedToTheFormat || row.want == admittedToTheEdit
			if !admitted && resp.Error.Code != jsonRPCInvalidParams {
				t.Errorf("Code = %d; want %d (invalid params) — a destination a request may not name is the request's fault",
					resp.Error.Code, jsonRPCInvalidParams)
			}
		})
	}
}

// TestAnInsertionPointIsNotReportedForADocumentReachedThroughALinkLeadingOut
// drives aggrega/insertionPoint over one real root, naming each document in each
// of the three spellings a document the client holds may be named by, and reads
// back whether a position was reported or the request refused, and in what words.
//
// The first row is the control, a document inside the root named relative to it.
// A document inside the root named by its absolute path or its file address is
// admitted as it always was; a document reached through a link leading out is
// refused whichever spelling names it, and whether or not the editor holds a copy
// of it, since that copy is still the file outside; a document outside the root as
// written is refused as before, in the words it always was.
func TestAnInsertionPointIsNotReportedForADocumentReachedThroughALinkLeadingOut(t *testing.T) {
	t.Parallel()

	address := func(path string) string { return (&url.URL{Scheme: "file", Path: filepath.ToSlash(path)}).String() }
	rows := []struct {
		name string
		file func(destinationLinksLayout) string
		// held is the content the editor holds for the row's file, validated
		// before the request; empty where it holds none.
		held string
		// want is the workspace-relative file a position is reported for, or the
		// refusal the request carries.
		want     string
		admitted bool
	}{
		{
			name:     "a document inside the root named relative to it is answered",
			file:     func(destinationLinksLayout) string { return "entity.yaml" },
			want:     "entity.yaml",
			admitted: true,
		},
		{
			name:     "a document inside the root named by its absolute path is answered",
			file:     func(l destinationLinksLayout) string { return filepath.Join(l.root, "entity.yaml") },
			want:     "entity.yaml",
			admitted: true,
		},
		{
			name:     "a document inside the root named by its file address is answered",
			file:     func(l destinationLinksLayout) string { return address(filepath.Join(l.root, "entity.yaml")) },
			want:     "entity.yaml",
			admitted: true,
		},
		{
			name:     "a document reached through a link inside the root is answered",
			file:     func(destinationLinksLayout) string { return "alias-entity.yaml" },
			want:     "alias-entity.yaml",
			admitted: true,
		},
		{
			name: "a document reached through a link leading out is refused",
			file: func(destinationLinksLayout) string { return "linked-entity.yaml" },
			want: errInsertionFileLeadsOut.Error(),
		},
		{
			name: "a document reached through a link leading out and named by its absolute path is refused",
			file: func(l destinationLinksLayout) string { return filepath.Join(l.root, "linked-entity.yaml") },
			want: errInsertionFileLeadsOut.Error(),
		},
		{
			name: "a document reached through a link leading out and named by its file address is refused",
			file: func(l destinationLinksLayout) string { return address(filepath.Join(l.root, "linked-entity.yaml")) },
			want: errInsertionFileLeadsOut.Error(),
		},
		{
			name: "a document inside a linked directory leading out is refused",
			file: func(destinationLinksLayout) string { return "shared/doc.yaml" },
			want: errInsertionFileLeadsOut.Error(),
		},
		{
			name: "a document reached through a link leading out is refused while the editor holds a copy",
			file: func(destinationLinksLayout) string { return "linked-entity.yaml" },
			held: linkTestComponent("held-copy"),
			want: errInsertionFileLeadsOut.Error(),
		},
		{
			name: "a document outside the root as written is refused as before",
			file: func(l destinationLinksLayout) string { return filepath.Join(l.outside, "outside-entity.yaml") },
			want: errInsertionFileOutsideWorkspace.Error(),
		},
	}

	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			l := layDestinationLinks(t)
			srv := linkedFilesServer(t, l.root, "workspace: {}\n")
			file := row.file(l)
			if row.held != "" {
				if held := callPastPushes(t, srv, "aggrega/validate", map[string]any{"file": file, "content": row.held}); held.Error != nil {
					t.Fatalf("hold the editor's copy of %s: %+v", file, held.Error)
				}
			}

			resp := callPastPushes(t, srv, "aggrega/insertionPoint", map[string]any{
				"file":      file,
				"container": []string{"metadata"},
			})

			if row.admitted {
				var got InsertionPointResult
				decodeDaemonResult(t, resp, &got)
				if got.File != row.want {
					t.Errorf("a position was reported for %q; want %q", got.File, row.want)
				}
				return
			}
			if resp.Error == nil {
				t.Fatalf("a position was reported; want the request refused with %q: %s", row.want, resp.Result)
			}
			if resp.Error.Code != jsonRPCInvalidParams {
				t.Errorf("Code = %d; want %d (invalid params)", resp.Error.Code, jsonRPCInvalidParams)
			}
			if resp.Error.Message != row.want {
				t.Errorf("the refusal reads %q; want %q", resp.Error.Message, row.want)
			}
		})
	}
}

// TestADestinationIsJudgedByTheDeepestPartOfItThatExists holds the judgement every
// path a request names is held to — read or written — to its rule over real paths,
// one shape per row: the deepest part of the path that exists, with its links
// resolved, against the root resolved the same way. The first row is the control,
// the root itself.
func TestADestinationIsJudgedByTheDeepestPartOfItThatExists(t *testing.T) {
	t.Parallel()

	l := layDestinationLinks(t)
	// A root reached through a link of its own still holds what lies under it.
	linkedRoot := filepath.Join(t.TempDir(), "root-link")
	plantLink(t, l.root, linkedRoot)

	rows := []struct {
		name, root, path string
		outside          bool
	}{
		{"the root itself is inside", l.root, l.root, false},
		{"a directory not made yet inside the root is inside", l.root, filepath.Join(l.root, "new", "deeper"), false},
		{"a document inside the root is inside", l.root, filepath.Join(l.root, "entity.yaml"), false},
		{"a link to a document inside the root is inside", l.root, filepath.Join(l.root, "alias-entity.yaml"), false},
		{"a linked directory inside the root is inside", l.root, filepath.Join(l.root, "inner"), false},
		{"a directory not made yet beneath a linked directory inside is inside", l.root, filepath.Join(l.root, "inner", "new"), false},
		{"a linked directory leading out is outside", l.root, filepath.Join(l.root, "shared"), true},
		{"an existing directory inside a linked directory leading out is outside", l.root, filepath.Join(l.root, "shared", "existing"), true},
		{"a directory not made yet beneath a linked directory leading out is outside", l.root, filepath.Join(l.root, "shared", "new"), true},
		{"two directories not made yet beneath a linked directory leading out are outside", l.root, filepath.Join(l.root, "shared", "new", "deeper"), true},
		{"a link to a document outside is outside", l.root, filepath.Join(l.root, "linked-entity.yaml"), true},
		{"a link leading nowhere is passed over for the directory holding it", l.root, filepath.Join(l.root, "dangling", "new"), false},
		{"a root reached through a link of its own holds a directory not made yet", linkedRoot, filepath.Join(linkedRoot, "new"), false},
		{"a root reached through a link of its own does not hold a link leading out", linkedRoot, filepath.Join(linkedRoot, "shared", "new"), true},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			if got := leadsOutOfWorkspace(row.root, row.path, nil); got != row.outside {
				t.Errorf("leadsOutOfWorkspace(%s) = %t; want %t", row.path, got, row.outside)
			}
		})
	}
	if _, err := os.Lstat(filepath.Join(l.outside, "missing")); err == nil {
		t.Errorf("judging a path beneath a link leading nowhere created the place it leads to")
	}
}
