// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package cfnimport

import (
	"slices"
	"testing"

	"github.com/aggrega-ai/aggrega-pdk/go/pkg/aggrega"
)

// TestLinkedChildWithoutAVersionLineIsRead reads, beside the root, every
// template it links by a local path, written with ./, bare or with ../ inside
// the imported directory, through a nested stack's TemplateURL or a serverless
// application's Location, though the linked template declares no
// AWSTemplateFormatVersion; a link by URL, by an application's ID and one
// leaving the directory link nothing the import holds.
func TestLinkedChildWithoutAVersionLineIsRead(t *testing.T) {
	t.Parallel()
	names := []string{"main.yaml", "network.yaml", "services/api.yaml", "shared/queue.yaml"}
	g, diags := readAll("", fixtures(t, "linked", names...)...)
	noDiagnostics(t, diags)
	if !slices.Equal(g.Read, names) || len(g.NotRead) > 0 {
		t.Errorf("the read reads %q and not %q, want it to read %q", g.Read, g.NotRead, names)
	}
	if got, want := nodeIDs(g), []string{"Api", "Network", "Outside", "Published", "Remote"}; !slices.Equal(got, want) {
		t.Errorf("the graph holds %q, want the root's resources %q", got, want)
	}
}

// TestSamBuildOutputBesideTheTemplateNamesBothCandidates halts source-refused
// on a SAM project imported after sam build, its template beside the one the
// build writes, naming both and the root setting; set to one of them, the
// root setting chooses it and names the other not read, with the event and the
// package.json beside them.
func TestSamBuildOutputBesideTheTemplateNamesBothCandidates(t *testing.T) {
	t.Parallel()
	sources := fixtures(t, "sam", ".aws-sam/build/template.yaml", "events/event.json", "package.json", "template.yaml")
	g, diags := readAll("", sources...)
	onlyDiagnostic(t, diags, aggrega.Diagnostic{Severity: aggrega.SeverityHalt, Code: "source-refused",
		Message: "the sources .aws-sam/build/template.yaml, template.yaml are each linked by no other source, and the " +
			"root setting names none of them: set root to the path of the one to start from"})
	if g != nil {
		t.Errorf("a halted read gives a graph")
	}

	g, diags = readAll("template.yaml", sources...)
	noDiagnostics(t, diags)
	if want := []string{"template.yaml"}; !slices.Equal(g.Read, want) {
		t.Errorf("the read reads %q, want %q", g.Read, want)
	}
	if want := []string{".aws-sam/build/template.yaml", "events/event.json", "package.json"}; !slices.Equal(g.NotRead,
		want) {
		t.Errorf("the read names %q not read, want %q", g.NotRead, want)
	}
}

// TestEventAndPackageJsonAreNotRead names a SAM project's test event and its
// package.json among the sources not read, never taking either for a
// parameters file, and names a file the template reader refuses that no
// template links not read, with the reader's reason.
func TestEventAndPackageJsonAreNotRead(t *testing.T) {
	t.Parallel()
	g, diags := readAll("", fixtures(t, "sam", "events/event.json", "events/truncated.json", "package.json",
		"template.yaml")...)
	noDiagnostics(t, diags)
	if want := []string{"template.yaml"}; !slices.Equal(g.Read, want) {
		t.Errorf("the read reads %q, want %q", g.Read, want)
	}
	if want := []string{"events/event.json", "events/truncated.json", "package.json"}; !slices.Equal(g.NotRead, want) {
		t.Errorf("the read names %q not read, want %q", g.NotRead, want)
	}
	want := "events/truncated.json is not read, as the template reader refuses it: invalid YAML template: yaml: " +
		"line 1: did not find expected node content."
	if len(g.Notes) == 0 || g.Notes[0] != want {
		t.Errorf("the notes are %q, want them to open with %q", g.Notes, want)
	}
}

// TestSourcesLinkCycleNamesEveryTemplateAndRoot halts source-refused where
// each template is linked by another, so none is a candidate, naming every
// template and the root setting; set, the root setting reads the cycle once.
func TestSourcesLinkCycleNamesEveryTemplateAndRoot(t *testing.T) {
	t.Parallel()
	sources := fixtures(t, "cycle", "a.yaml", "b.yaml")
	_, diags := readAll("", sources...)
	onlyDiagnostic(t, diags, aggrega.Diagnostic{Severity: aggrega.SeverityHalt, Code: "source-refused",
		Message: "every source the import holds is linked by another, so none is where it starts: set root to the " +
			"path of the one to start from, one of a.yaml, b.yaml"})

	g, diags := readAll("a.yaml", sources...)
	noDiagnostics(t, diags)
	if want := []string{"a.yaml", "b.yaml"}; !slices.Equal(g.Read, want) {
		t.Errorf("the read reads %q, want %q", g.Read, want)
	}
	if got, want := nodeIDs(g), []string{"B"}; !slices.Equal(got, want) {
		t.Errorf("the graph holds %q, want the root's resources %q", got, want)
	}
}

// TestSourcesRecogniseATemplateByWhatItHolds reads a source as a template
// where it holds an AWSTemplateFormatVersion written as text, or Resources and
// a Transform, and halts source-refused where the import holds no template.
func TestSourcesRecogniseATemplateByWhatItHolds(t *testing.T) {
	t.Parallel()
	const resources = "Resources:\n  Queue:\n    Type: AWS::SQS::Queue\n"
	refused := aggrega.Diagnostic{Severity: aggrega.SeverityHalt, Code: "source-refused",
		Message: "the import holds no CloudFormation template: a template is a YAML or JSON file holding an " +
			"AWSTemplateFormatVersion written as text, or holding Resources and a Transform, or one that a template " +
			"the import reads links by a local path; add AWSTemplateFormatVersion: \"2010-09-09\" to the template, or " +
			"import the directory holding the template that links it"}
	cases := []struct {
		name, body string
		template   bool
	}{
		{"a version written as text", "AWSTemplateFormatVersion: 2010-09-09\n" + resources, true},
		{"resources and a transform", "Transform: AWS::Serverless-2016-10-31\n" + resources, true},
		{"resources alone", resources, false},
		{"a version that is no text", "AWSTemplateFormatVersion: [2010-09-09]\n" + resources, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			g, diags := readAll("", src("template.yaml", []byte(c.body)),
				src("package.json", fixture(t, "sam/package.json")))
			if !c.template {
				onlyDiagnostic(t, diags, refused)
				return
			}
			noDiagnostics(t, diags)
			if got, want := nodeIDs(g), []string{"Queue"}; !slices.Equal(got, want) {
				t.Errorf("the graph holds %q, want %q", got, want)
			}
		})
	}
}

// TestSourcesSeveralParametersFilesAreNoneRead reads none of several
// parameters files, names each not read and warns parameter-files-not-read
// naming them.
func TestSourcesSeveralParametersFilesAreNoneRead(t *testing.T) {
	t.Parallel()
	g, diags := readAll("", fixtures(t, "params", "list.json", "pairs.json", "template.yaml")...)
	onlyDiagnostic(t, diags, aggrega.Diagnostic{Severity: aggrega.SeverityWarn, Code: "parameter-files-not-read",
		Message: "the import holds several parameters files, list.json, pairs.json, and reads none of them, as one " +
			"import drafts one Release for one environment: import the template with the one parameters file whose " +
			"values the Release takes"})
	if want := []string{"template.yaml"}; !slices.Equal(g.Read, want) {
		t.Errorf("the read reads %q, want %q", g.Read, want)
	}
	if want := []string{"list.json", "pairs.json"}; !slices.Equal(g.NotRead, want) {
		t.Errorf("the read names %q not read, want %q", g.NotRead, want)
	}
	for _, p := range g.Params {
		if p.Supplied != nil {
			t.Errorf("the parameter %s is supplied %#v, want no value", p.Name, p.Supplied)
		}
	}
}

// TestSourcesRootSettingRoutesPastARefusedSource reads, with the root setting
// set, past a source the template reader refuses that the root does not reach
// — one whose text holds AWSTemplateFormatVersion, one linked by a template
// the import does not read — naming it not read with the reader's reason, and
// halts parse-failed on one a template the root reaches links.
func TestSourcesRootSettingRoutesPastARefusedSource(t *testing.T) {
	t.Parallel()
	t.Run("holding AWSTemplateFormatVersion", func(t *testing.T) {
		t.Parallel()
		g, diags := readAll("template.yaml", fixtures(t, "stray", "docs/notes.yaml", "template.yaml")...)
		noDiagnostics(t, diags)
		if want := []string{"template.yaml"}; !slices.Equal(g.Read, want) {
			t.Errorf("the read reads %q, want %q", g.Read, want)
		}
		if want := []string{"docs/notes.yaml"}; !slices.Equal(g.NotRead, want) {
			t.Errorf("the read names %q not read, want %q", g.NotRead, want)
		}
		want := []string{"docs/notes.yaml is not read, as the template reader refuses it: invalid YAML template: " +
			"yaml: line 3: found character that cannot start any token."}
		if !slices.Equal(g.Notes, want) {
			t.Errorf("the notes are %q, want %q", g.Notes, want)
		}
	})
	t.Run("linked by a template the import does not read", func(t *testing.T) {
		t.Parallel()
		g, diags := readAll("template.yaml", fixtures(t, "stray", "broken.yaml", "other.yaml", "template.yaml")...)
		noDiagnostics(t, diags)
		if want := []string{"template.yaml"}; !slices.Equal(g.Read, want) {
			t.Errorf("the read reads %q, want %q", g.Read, want)
		}
		if want := []string{"broken.yaml", "other.yaml"}; !slices.Equal(g.NotRead, want) {
			t.Errorf("the read names %q not read, want %q", g.NotRead, want)
		}
		want := []string{"broken.yaml is not read, as the template reader refuses it: invalid YAML template: yaml: " +
			"line 4: did not find expected node content."}
		if !slices.Equal(g.Notes, want) {
			t.Errorf("the notes are %q, want %q", g.Notes, want)
		}
	})
	t.Run("linked by a template the root reaches", func(t *testing.T) {
		t.Parallel()
		g, diags := readAll("main.yaml", fixtures(t, "stray", "broken.yaml", "main.yaml", "template.yaml")...)
		onlyDiagnostic(t, diags, aggrega.Diagnostic{Severity: aggrega.SeverityHalt, Code: "parse-failed",
			Message: `could not parse source "broken.yaml" as cloudformation: invalid YAML template: yaml: line 4: ` +
				`did not find expected node content`, SourceRef: "broken.yaml"})
		if g != nil {
			t.Errorf("a halted read gives a graph")
		}
	})
}

// TestSourcesRootSettingNamesATemplateWithoutAVersionLine starts from the
// source the root setting names where the template reader reads it as an
// object holding Resources, though it declares no AWSTemplateFormatVersion and
// nothing links it, and halts source-refused, saying it is no template, where
// the source it names holds no Resources.
func TestSourcesRootSettingNamesATemplateWithoutAVersionLine(t *testing.T) {
	t.Parallel()
	sources := []aggrega.ImportSource{src("network.yaml", fixture(t, "linked/network.yaml")),
		src("package.json", fixture(t, "sam/package.json"))}
	t.Run("holding Resources", func(t *testing.T) {
		t.Parallel()
		g, diags := readAll("network.yaml", sources...)
		noDiagnostics(t, diags)
		if want := []string{"network.yaml"}; !slices.Equal(g.Read, want) {
			t.Errorf("the read reads %q, want %q", g.Read, want)
		}
		if want := []string{"package.json"}; !slices.Equal(g.NotRead, want) {
			t.Errorf("the read names %q not read, want %q", g.NotRead, want)
		}
		if got, want := nodeIDs(g), []string{"Vpc"}; !slices.Equal(got, want) {
			t.Errorf("the graph holds %q, want %q", got, want)
		}
	})
	t.Run("holding no Resources", func(t *testing.T) {
		t.Parallel()
		g, diags := readAll("package.json", sources...)
		onlyDiagnostic(t, diags, aggrega.Diagnostic{Severity: aggrega.SeverityHalt, Code: "source-refused",
			Message: "the root setting names package.json, which holds no Resources object, so it is no template the " +
				"import can start from: set root to the path of a template, one holding Resources"})
		if g != nil {
			t.Errorf("a halted read gives a graph")
		}
	})
}
