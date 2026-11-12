// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package serve

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
)

// moduleReferenceFixture stands the cross-file workspace up in a tempdir — a
// Release whose Aggregate imports one module from the workspace's catalog — with
// the module's component type replaced by the row's, and returns the server, the
// Aggregate's path and the module's path.
func moduleReferenceFixture(t *testing.T, moduleComponentType string) (srv *Server, aggregatePath, modulePath string) {
	t.Helper()
	tmp := t.TempDir()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	fixtureDir := filepath.Join(cwd, "testdata", "tier4", "cross-file-analyzer-violations")
	for _, rel := range []string{
		"release.yaml", "manifest.yaml", "aggregate.yaml", ".aggrega-config.yaml",
		filepath.Join("catalog", "components", "web.yaml"),
		filepath.Join("catalog", "features", "premium.yaml"),
		filepath.Join("catalog", "aggregates", "sub-with-violation.yaml"),
	} {
		data, rerr := os.ReadFile(filepath.Join(fixtureDir, rel))
		if rerr != nil {
			t.Fatalf("read fixture %s: %v", rel, rerr)
		}
		dst := filepath.Join(tmp, rel)
		if merr := os.MkdirAll(filepath.Dir(dst), 0o755); merr != nil {
			t.Fatalf("mkdir %s: %v", filepath.Dir(dst), merr)
		}
		if werr := os.WriteFile(dst, data, 0o644); werr != nil {
			t.Fatalf("write fixture %s: %v", rel, werr)
		}
	}
	modulePath = filepath.Join(tmp, "catalog", "aggregates", "sub-with-violation.yaml")
	module, err := os.ReadFile(modulePath)
	if err != nil {
		t.Fatalf("read module: %v", err)
	}
	const authored = "      type: example/web\n"
	if strings.Count(string(module), authored) != 1 {
		t.Fatalf("the module no longer carries exactly one %q, so the row's type cannot be placed", authored)
	}
	module = []byte(strings.Replace(string(module), authored, "      type: "+moduleComponentType+"\n", 1))
	if err := os.WriteFile(modulePath, module, 0o644); err != nil {
		t.Fatalf("write module: %v", err)
	}

	srv = newTestServer(t, tmp, nil, nil)
	srv.wsConfig.Workspace.CatalogDirs = []string{filepath.Join(tmp, "catalog")}
	// The lazy catalog captures its directories at construction, so it is rebuilt
	// over the fixture's catalog now that the configuration names it.
	srv.lazyCatalog = catalog.NewLazyLookup(tmp, "", []string{filepath.Join(tmp, "catalog")}, nil)
	return srv, filepath.Join(tmp, "aggregate.yaml"), modulePath
}

// TestValidateReportsAModuleReferenceNothingResolvesAsTheAuthorsFault drives the
// daemon's validate over a Release whose imported module names a component type
// no catalog holds, and reads the condition codes off the answer.
//
// The answer carries the unresolvable-reference condition — the author's fault,
// the same condition the graph request and a build report for it — and never the
// disagreement between the compiler's own views of the catalog. It is asserted on
// both documents a reader would validate against that Release: the Aggregate that
// imports the module, and the module's own file.
//
// The healthy rows are the positive control: the same workspace with the module's
// type resolvable carries neither condition, so the fault in the other rows is
// the missing type's and not the fixture's.
func TestValidateReportsAModuleReferenceNothingResolvesAsTheAuthorsFault(t *testing.T) {
	t.Parallel()
	unresolvable, _ := cerrors.CodeOf(closure.ErrUnresolvableURI())
	disagreement, _ := cerrors.CodeOf(resolver.ErrGuardEntityViewInconsistency())
	for _, tc := range []struct {
		name       string
		moduleType string
		module     bool
		want       bool
	}{
		{"the Aggregate, with every module reference resolvable", "example/web", false, false},
		{"the module's own file, with every module reference resolvable", "example/web", true, false},
		{"the Aggregate, with the module naming a type no catalog holds", "example/absent", false, true},
		{"the module's own file, with it naming a type no catalog holds", "example/absent", true, true},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			srv, aggregatePath, modulePath := moduleReferenceFixture(t, tc.moduleType)
			file := aggregatePath
			if tc.module {
				file = modulePath
			}
			content, err := os.ReadFile(file)
			if err != nil {
				t.Fatalf("read %s: %v", file, err)
			}
			resp := requestOverTheWire(t, srv, "aggrega/validate", validateParams{
				File: file, Content: string(content), Release: "release.yaml",
			})
			if resp.Error != nil {
				t.Fatalf("validate %q: unexpected envelope %+v", file, resp.Error)
			}
			var result validateResult
			if err := json.Unmarshal(resp.Result, &result); err != nil {
				t.Fatalf("unmarshal validate result: %v", err)
			}
			var sawUnresolvable bool
			for _, d := range result.Diagnostics {
				if d.Code == disagreement {
					t.Errorf("reported as a disagreement between the compiler's own views: %q", d.Message)
				}
				if d.Code == unresolvable {
					sawUnresolvable = true
				}
			}
			if sawUnresolvable != tc.want {
				t.Errorf("the answer carries the %s condition: %v; want %v\n diagnostics: %q",
					unresolvable, sawUnresolvable, tc.want, diagMessages(result.Diagnostics))
			}
		})
	}
}
