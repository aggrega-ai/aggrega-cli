// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package wasm

import (
	"errors"
	"strings"
	"testing"
)

// importerDescriptor is an importer's describe() report declaring detect as its
// recognition signatures, or none where detect is nil.
func importerDescriptor(detect []DetectSignature) PluginDescriptor {
	desc := PluginDescriptor{
		AbiVersion:   ABIVersion,
		PluginType:   string(PluginTypeImporter),
		Capabilities: []string{"import:langflow"},
		Exports:      []PluginExport{{Name: ImportExport.Name()}},
	}
	if detect != nil {
		desc.Importer = &ImporterContract{Detect: detect}
	}
	return desc
}

// langflowSignature is a recognition signature with every field populated.
func langflowSignature() DetectSignature {
	return DetectSignature{
		Syntax: "json",
		Files:  []string{"*.json"},
		Match: []PathMatch{
			{Path: "data.nodes", Is: "array"},
			{Path: "kind", Is: "string", Equals: "Flow"},
		},
	}
}

// TestCrossCheckContract_ImporterDetect holds the load's comparison of an
// entity's recognition record with the importer's describe() report: a record
// equal entry for entry passes, a record that differs in any field, in length or
// in order is refused naming importer.detect, and an entity recording nothing
// makes no claim against an importer that declares signatures.
func TestCrossCheckContract_ImporterDetect(t *testing.T) {
	t.Parallel()
	reported := []DetectSignature{langflowSignature(), {Syntax: "yaml", Match: []PathMatch{{Path: "kind", Is: "present"}}}}
	recorded := func(edit func([]DetectSignature) []DetectSignature) *[]DetectSignature {
		copied := make([]DetectSignature, 0, len(reported))
		for _, sig := range reported {
			sig.Files = append([]string(nil), sig.Files...)
			sig.Match = append([]PathMatch(nil), sig.Match...)
			copied = append(copied, sig)
		}
		copied = edit(copied)
		return &copied
	}
	same := func(s []DetectSignature) []DetectSignature { return s }

	for _, tc := range []struct {
		name    string
		claim   *[]DetectSignature
		guest   []DetectSignature
		refused bool
	}{
		{"an equal record passes", recorded(same), reported, false},
		{"no record makes no claim", nil, reported, false},
		{"an empty record against no signature passes", &[]DetectSignature{}, nil, false},
		{"an empty record against declared signatures is refused", &[]DetectSignature{}, reported, true},
		{"a record against an importer declaring none is refused", recorded(same), nil, true},
		{"a differing syntax is refused", recorded(func(s []DetectSignature) []DetectSignature {
			s[0].Syntax = "yaml"
			return s
		}), reported, true},
		{"a differing file pattern is refused", recorded(func(s []DetectSignature) []DetectSignature {
			s[0].Files = []string{"*.flow.json"}
			return s
		}), reported, true},
		{"a differing is is refused", recorded(func(s []DetectSignature) []DetectSignature {
			s[0].Match[0].Is = "present"
			return s
		}), reported, true},
		{"a differing equals is refused", recorded(func(s []DetectSignature) []DetectSignature {
			s[0].Match[1].Equals = "Graph"
			return s
		}), reported, true},
		{"a predicate turned from equals to prefix is refused", recorded(func(s []DetectSignature) []DetectSignature {
			s[0].Match[1].Prefix, s[0].Match[1].Equals = s[0].Match[1].Equals, ""
			return s
		}), reported, true},
		{"a missing predicate is refused", recorded(func(s []DetectSignature) []DetectSignature {
			s[0].Match = s[0].Match[:1]
			return s
		}), reported, true},
		{"reordered signatures are refused", recorded(func(s []DetectSignature) []DetectSignature {
			s[0], s[1] = s[1], s[0]
			return s
		}), reported, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := CrossCheckContract("importer/import-langflow:1.0.0", EntityContractClaim{Detect: tc.claim},
				importerDescriptor(tc.guest))
			if !tc.refused {
				if err != nil {
					t.Fatalf("the record was refused: %v", err)
				}
				return
			}
			if !errors.Is(err, ErrWasmContractMismatch()) {
				t.Fatalf("a differing record must be refused as a contract mismatch; got %v", err)
			}
			if !strings.Contains(err.Error(), "importer.detect") {
				t.Errorf("the refusal does not name the field: %v", err)
			}
			if !strings.Contains(err.Error(), "importer/import-langflow:1.0.0") {
				t.Errorf("the refusal does not name the entity: %v", err)
			}
		})
	}
}

// TestCrossCheckContract_ImporterDetectNamesBothPredicates holds the refusal of
// a differing predicate to naming the recorded and the reported predicate in the
// words their fields are written in, with the comparison where one is declared.
func TestCrossCheckContract_ImporterDetectNamesBothPredicates(t *testing.T) {
	t.Parallel()
	reported := []DetectSignature{langflowSignature()}
	recorded := []DetectSignature{langflowSignature()}
	recorded[0].Match = []PathMatch{reported[0].Match[0], {Path: "kind", Is: "string", Prefix: "Fl"}}

	err := CrossCheckContract("importer/import-langflow:1.0.0", EntityContractClaim{Detect: &recorded},
		importerDescriptor(reported))
	if !errors.Is(err, ErrWasmContractMismatch()) {
		t.Fatalf("a differing predicate must be refused as a contract mismatch; got %v", err)
	}
	for _, want := range []string{
		`importer.detect[0].match[1]`,
		`path "kind" is "string" prefix "Fl"`,
		`path "kind" is "string" equals "Flow"`,
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not say %s: %v", want, err)
		}
	}
}

// TestValidate_ImporterContract holds the describe() shape gate over the
// recognition contract: a well-formed one is admitted, and each malformation is
// refused at load naming what is wrong, one fault per row.
func TestValidate_ImporterContract(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		desc   func() PluginDescriptor
		reason string
	}{
		{"a well-formed contract is admitted", func() PluginDescriptor {
			return importerDescriptor([]DetectSignature{langflowSignature()})
		}, ""},
		{"a contract on a plugin that is not an importer is refused", func() PluginDescriptor {
			d := importerDescriptor([]DetectSignature{langflowSignature()})
			d.PluginType = string(PluginTypeParser)
			d.Capabilities = []string{"parse:demo"}
			d.Exports = []PluginExport{{Name: ParseExport.Name()}}
			return d
		}, "only an importer declares"},
		{"an empty syntax is refused", func() PluginDescriptor {
			sig := langflowSignature()
			sig.Syntax = ""
			return importerDescriptor([]DetectSignature{sig})
		}, "empty syntax"},
		{"a file pattern that is not well formed is refused", func() PluginDescriptor {
			sig := langflowSignature()
			sig.Files = []string{"[.json"}
			return importerDescriptor([]DetectSignature{sig})
		}, "not well formed"},
		{"an empty path is refused", func() PluginDescriptor {
			sig := langflowSignature()
			sig.Match[0].Path = ""
			return importerDescriptor([]DetectSignature{sig})
		}, "empty path"},
		{"an is outside the declared kinds is refused", func() PluginDescriptor {
			sig := langflowSignature()
			sig.Match[0].Is = "list"
			return importerDescriptor([]DetectSignature{sig})
		}, `is "list"`},
		{"equals and prefix together are refused", func() PluginDescriptor {
			sig := langflowSignature()
			sig.Match[1].Prefix = "Fl"
			return importerDescriptor([]DetectSignature{sig})
		}, "both equals and prefix"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := tc.desc().Validate("importer/probe")
			if tc.reason == "" {
				if err != nil {
					t.Fatalf("a well-formed contract was refused: %v", err)
				}
				return
			}
			if !errors.Is(err, ErrWasmManifestRejected()) {
				t.Fatalf("the descriptor must be refused as rejected; got %v", err)
			}
			if !strings.Contains(err.Error(), tc.reason) {
				t.Errorf("the refusal does not say %q: %v", tc.reason, err)
			}
		})
	}
}

// TestUnderDeclaredNotices_ImporterDetect holds the publication advisory to
// telling an author whose entity records no recognition signatures that the
// importer declares some, so a source is recognised by none of them until the
// entity records them — and to saying nothing once it does.
func TestUnderDeclaredNotices_ImporterDetect(t *testing.T) {
	t.Parallel()
	desc := importerDescriptor([]DetectSignature{langflowSignature()})
	surface := EntityCuratedSurface{ExportNames: []string{ImportExport.Name()}, Capabilities: desc.Capabilities}

	notices := UnderDeclaredNotices(surface, desc)
	if len(notices) != 1 || !strings.Contains(notices[0], "recognition signatures") {
		t.Fatalf("notices = %q; want one naming the unrecorded recognition signatures", notices)
	}
	surface.HasDetect = true
	if notices := UnderDeclaredNotices(surface, desc); len(notices) != 0 {
		t.Fatalf("an entity recording the signatures drew notices: %q", notices)
	}
}

// importerDescriptorTargeting is an importer's describe() report declaring
// target as its default target, or none where target is empty.
func importerDescriptorTargeting(target string) PluginDescriptor {
	desc := importerDescriptor(nil)
	if target != "" {
		desc.Importer = &ImporterContract{DefaultTarget: target}
	}
	return desc
}

// TestImporterDefaultTargetMismatchRefused holds the load's comparison of the
// default target an entity records with the importer's describe() report: a
// differing record is refused as a contract mismatch naming importer.defaultTarget
// and both values.
func TestImporterDefaultTargetMismatchRefused(t *testing.T) {
	t.Parallel()
	err := CrossCheckContract("importer/import-arm:1.0.0", EntityContractClaim{DefaultTarget: "compose"},
		importerDescriptorTargeting("arm"))
	if !errors.Is(err, ErrWasmContractMismatch()) {
		t.Fatalf("a differing default target must be refused as a contract mismatch; got %v", err)
	}
	for _, want := range []string{"importer.defaultTarget", `"compose"`, `"arm"`, "importer/import-arm:1.0.0"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not contain %s: %v", want, err)
		}
	}
	if err := CrossCheckContract("importer/import-arm:1.0.0", EntityContractClaim{DefaultTarget: "arm"},
		importerDescriptorTargeting("arm")); err != nil {
		t.Errorf("an equal record was refused: %v", err)
	}
}

// TestImporterDefaultTargetAbsentNotCompared holds an entity recording no
// default target to making no claim against an importer that declares one.
func TestImporterDefaultTargetAbsentNotCompared(t *testing.T) {
	t.Parallel()
	if err := CrossCheckContract("importer/import-arm:1.0.0", EntityContractClaim{Detect: &[]DetectSignature{}},
		importerDescriptorTargeting("arm")); err != nil {
		t.Fatalf("an entity recording no default target was refused: %v", err)
	}
}
