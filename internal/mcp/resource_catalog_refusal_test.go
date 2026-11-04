// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package mcp

import (
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compilerops"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/validator/envelope"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// refusedAdminerComponent is a Component the catalog load refuses: its inject
// list is written as a scalar, which its kind's schema does not accept. Its
// header still declares its identity, so a reference to that identity can be
// matched to it.
const refusedAdminerComponent = "apiVersion: aggrega.ai/v1alpha1\n" +
	"kind: Component\n" +
	"metadata:\n" +
	"  name: core/devtool/adminer\n" +
	"  version: \"1.0.0\"\n" +
	"  labels:\n" +
	"    component.aggrega.ai/type: \"server\"\n" +
	"spec:\n" +
	"  inject: env\n" +
	"  schema:\n" +
	"    infra:\n" +
	"      port: { type: integer, default: 8080 }\n"

// TestServeCatalogEntityNamesTheRefusedFileDeclaringIt drives the catalog
// resource over a workspace whose catalog holds one file the load refuses. A read
// of the identity that file declares is still a resource-not-found, and its
// message names the refused file, the condition it was refused under, why that
// leaves the identity resolving to nothing, and the URI asked for — rather than
// only the URI. A read of an identity no file declares keeps the plain
// not-found message, so the refusal is reported only where it explains the miss.
func TestServeCatalogEntityNamesTheRefusedFileDeclaringIt(t *testing.T) {
	t.Parallel()
	ws := t.TempDir()
	catalogDir := filepath.Join(ws, workspaceconfig.ConventionalCatalogDir)
	if err := os.MkdirAll(catalogDir, 0o755); err != nil {
		t.Fatalf("mkdir the conventional catalog directory: %v", err)
	}
	const refusedFile = "adminer.yaml"
	if err := os.WriteFile(filepath.Join(catalogDir, refusedFile), []byte(refusedAdminerComponent), 0o644); err != nil {
		t.Fatalf("write the refused component: %v", err)
	}
	s := &server{out: io.Discard, logw: io.Discard, launchRoot: ws}

	const uri = "catalog://component/core/devtool/adminer"
	_, rerr := readResource(t, s, uri)
	if rerr == nil {
		t.Fatalf("resources/read %s served a refused entity", uri)
	}
	if rerr.Code != codeResourceNotFound {
		t.Errorf("resources/read %s code = %d, want %d", uri, rerr.Code, codeResourceNotFound)
	}
	if strings.HasPrefix(rerr.Message, "resource not found") {
		t.Errorf("resources/read %s answers the plain not-found rather than the refusal:\n%s", uri, rerr.Message)
	}
	want := []string{refusedFile, compilerops.RefusedDocumentNote, strconv.Quote(uri), "inject"}
	if code, ok := cerrors.ShortCodeOf(envelope.ErrEntityEnvelope()); ok && code != "" {
		want = append(want, code)
	}
	for _, w := range want {
		if !strings.Contains(rerr.Message, w) {
			t.Errorf("resources/read %s message does not carry %q:\n%s", uri, w, rerr.Message)
		}
	}

	const absent = "catalog://component/core/devtool/absent"
	if _, rerr := readResource(t, s, absent); rerr == nil || rerr.Code != codeResourceNotFound ||
		rerr.Message != "resource not found: "+absent {
		t.Errorf("resources/read %s error = %+v, want the plain not-found", absent, rerr)
	}
}
