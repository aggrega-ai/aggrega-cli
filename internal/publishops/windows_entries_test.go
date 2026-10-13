// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package publishops

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"oras.land/oras-go/v2/content/memory"

	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
)

// windowsNamesComponent is a Component whose two schema pillars are imported from
// the files named, or which imports nothing where both are empty. A pillar import
// is the smallest declaration that makes an entity carry a file, and two of them
// let one entity carry two names of different classes.
func windowsNamesComponent(valuesImport, infraImport string) string {
	body := "apiVersion: aggrega.ai/v1alpha1\nkind: Component\nmetadata:\n  name: core/windows-names\n" +
		"  version: 1.0.0\n  labels:\n    component.aggrega.ai/type: server\nspec:\n  schema:\n"
	if valuesImport == "" && infraImport == "" {
		return body + "    values:\n      greeting: { type: string, default: hello }\n"
	}
	if valuesImport != "" {
		body += fmt.Sprintf("    values:\n      $import: %q\n", valuesImport)
	}
	if infraImport != "" {
		body += fmt.Sprintf("    infra:\n      $import: %q\n", infraImport)
	}
	return body
}

// pillarFile is a pillar body an import above may name.
const pillarFile = "greeting:\n  type: string\n  default: hello\n"

// TestAPublicationWarnsAboutEachEntryWindowsCannotHoldAndRefusesNothing publishes
// one entity per row — as a dry run, and for real into a store this test holds —
// and reads three things: the entries the result carries, the warning lines on
// the advisory output, and that the publication went ahead.
//
// The rows are the shapes the rule tells apart: a device name as a directory and a
// name ending in a full stop; a device name with an extension in upper case; the
// document's own file name, which the archive holds under that name and is judged
// with what it carries; a document carrying nothing, which publishes alone under a
// name the product chooses and warns about nothing whatever it is called; and an
// archive whose every name Windows holds. A name ending in a space has no row,
// because a schema import's path is read with its surrounding space removed, and
// two places prove it instead: TestWindowsCannotHold in pathsegment, the table of
// the predicate the warning reads, and, on this path, the name ending in a full
// stop, judged by the one rule that decides both endings. Each warning line is
// read for the condition's own sentence, read off its registration rather than
// spelled here, and for the entry and segment it names.
func TestAPublicationWarnsAboutEachEntryWindowsCannotHoldAndRefusesNothing(t *testing.T) {
	sentence, ok := cerrors.MessageOf(ErrWindowsUnusableEntry())
	if !ok || sentence == "" {
		t.Fatal("the advisory registers no sentence, so no line can be read for it")
	}
	rows := []struct {
		name         string
		document     string
		body         string
		files        []string
		wantEntries  []string
		wantSegments []string
	}{
		{
			name:         "a device name as a directory, and a name ending in a full stop",
			document:     "component.yaml",
			body:         windowsNamesComponent("./aux/x.yaml", "./report."),
			files:        []string{"aux/x.yaml", "report."},
			wantEntries:  []string{"aux/x.yaml", "report."},
			wantSegments: []string{"aux", "report."},
		},
		{
			name:         "a device name with an extension in upper case",
			document:     "component.yaml",
			body:         windowsNamesComponent("./COM1.yaml", ""),
			files:        []string{"COM1.yaml"},
			wantEntries:  []string{"COM1.yaml"},
			wantSegments: []string{"COM1.yaml"},
		},
		{
			name:         "the document's own file name is judged with what it carries",
			document:     "con.yaml",
			body:         windowsNamesComponent("./schema.yaml", ""),
			files:        []string{"schema.yaml"},
			wantEntries:  []string{"con.yaml"},
			wantSegments: []string{"con.yaml"},
		},
		{
			name:     "a document carrying nothing publishes alone and warns about nothing",
			document: "nul.yaml",
			body:     windowsNamesComponent("", ""),
		},
		{
			name:     "an archive whose every name Windows holds",
			document: "component.yaml",
			body:     windowsNamesComponent("./schema/values.yaml", "./schema/infra.yaml"),
			files:    []string{"schema/values.yaml", "schema/infra.yaml"},
		},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "1.0.0")
			writeWindowsNamesFixture(t, filepath.Join(dir, row.document), row.body)
			for _, rel := range row.files {
				writeWindowsNamesFixture(t, filepath.Join(dir, filepath.FromSlash(rel)), pillarFile)
			}
			entityPath := filepath.Join(dir, row.document)

			for _, dryRun := range []bool{true, false} {
				var status, advisories bytes.Buffer
				store := memory.New()
				no := false
				res, err := Push(context.Background(), PushConfig{
					EntityPath: entityPath, Registry: "example.com", DryRun: dryRun, Sign: &no,
					Workspace: resolveIn(t, "workspace: {}\n", nil), PushTarget: store,
					Out: &status, Advisories: &advisories,
				})
				moment := fmt.Sprintf("dry run %v", dryRun)
				if err != nil {
					t.Fatalf("%s: the publication was refused; a warning refuses nothing: %v", moment, err)
				}
				if !dryRun {
					if _, exists, rerr := oci.Resolve(context.Background(), store, "1.0.0"); rerr != nil || !exists {
						t.Fatalf("%s: the publication landed nothing (exists %v, %v)", moment, exists, rerr)
					}
				}
				// DeepEqual tells a nil list from an empty one, so a row expecting none
				// also holds the result to carrying none rather than an empty list.
				if !reflect.DeepEqual(res.WindowsUnusableEntries, row.wantEntries) {
					t.Errorf("%s: the result carries %#v, want %#v", moment, res.WindowsUnusableEntries, row.wantEntries)
				}

				var lines []string
				for _, line := range strings.Split(advisories.String(), "\n") {
					if line != "" {
						lines = append(lines, line)
					}
				}
				if len(lines) != len(row.wantEntries) {
					t.Fatalf("%s: %d advisory line(s), want %d:\n%s", moment, len(lines), len(row.wantEntries), advisories.String())
				}
				for i, line := range lines {
					if !strings.HasPrefix(line, "warning: "+sentence+" ") {
						t.Errorf("%s: line %d does not open with the condition's sentence: %q", moment, i, line)
					}
					for _, want := range []string{
						fmt.Sprintf("the entry %q", row.wantEntries[i]),
						fmt.Sprintf("its segment %q", row.wantSegments[i]),
					} {
						if !strings.Contains(line, want) {
							t.Errorf("%s: line %d does not name %s: %q", moment, i, want, line)
						}
					}
				}
				if strings.Contains(status.String(), sentence) {
					t.Errorf("%s: the warning reached the status output a script reads:\n%s", moment, status.String())
				}
			}
		})
	}
}

// writeWindowsNamesFixture writes content at path, creating its directory.
func writeWindowsNamesFixture(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
