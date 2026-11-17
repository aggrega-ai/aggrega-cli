// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

//go:build integration

package main

// End-to-end coverage of a policy attached at the render-verdict role over the
// templates the CloudFormation and Azure Resource Manager providers write, with
// each embedded engine, Rego and CEL. The workspace's catalog is local, so the
// builds run offline; each Recipe writes the template its provider reads.

import (
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/analyzer"
)

// renderVerdictCloudWorkspace copies the render-verdict-cloud fixtures into a
// fresh directory, seeds the embedded Plugin entities its catalog needs, and
// returns the directory, the catalog directory and the Release path for the
// named target and fixture Release suffix ("" for the control, "-violation").
func renderVerdictCloudWorkspace(t *testing.T, target, suffix string) (root, catalogDir, releasePath string) {
	t.Helper()
	root = t.TempDir()
	copyTree(t, filepath.Join("testdata", "render-verdict-cloud"), root)
	catalogDir = filepath.Join(root, "catalog")
	seedEmbeddedPlugins(t, filepath.Join(catalogDir, "providers"), "provider/"+target)
	writeRegoPluginEntity(t, catalogDir)
	writeCelPluginEntity(t, catalogDir)
	return root, catalogDir, filepath.Join(root, "release-"+target+suffix+".yaml")
}

// TestRenderVerdictCloudTemplates builds one Release per target twice with a
// control and once with a violation, under each engine.
//
// The control completes with the policy attached and writes the same files, byte
// for byte, as the build with no policy attached, so the verdict changed nothing.
// The violation halts with the policy refusal, matched through its accessor, and
// the refusal's sentence names the file the rule judged. CloudFormation is judged
// on its database instance's storage and Azure Resource Manager on a container's
// image.
func TestRenderVerdictCloudTemplates(t *testing.T) {
	nonInteractive(t)
	const noPolicy = "workspace: {}\noffline: true\n"

	targets := []struct {
		name         string
		wantSentence string
	}{
		{name: "cloudformation", wantSentence: "cloudformation/cloudformation-stack.json: a database instance stores its data unencrypted"},
		{name: "arm", wantSentence: "arm/arm-template.json: a container runs an image with no tag or the tag latest"},
	}
	engines := []struct {
		name       string
		source     string
		attachment func(role, sourceFile, settings string) string
	}{
		{name: "rego", source: "policies/house-rule.rego", attachment: regoAttachment},
		{name: "cel", source: "policies/house-rule.cel", attachment: celAttachment},
	}

	for _, target := range targets {
		for _, engine := range engines {
			t.Run(target.name+" "+engine.name, func(t *testing.T) {
				config := engine.attachment("render-verdict", engine.source, "")

				t.Run("the control is untouched by the verdict", func(t *testing.T) {
					root, catalogDir, release := renderVerdictCloudWorkspace(t, target.name, "")
					if err := pullThen(t, root, release, []string{catalogDir}, noPolicy, "dist-none"); err != nil {
						t.Fatalf("the build with no policy attached must complete: %v", err)
					}
					if err := pullThen(t, root, release, []string{catalogDir}, config, "dist-policy"); err != nil {
						t.Fatalf("the control must complete with the policy attached: %v", err)
					}
					reference := readTree(t, filepath.Join(root, "dist-none"))
					if len(reference) == 0 {
						t.Fatal("the build with no policy wrote no files, so the comparison proves nothing")
					}
					if got := readTree(t, filepath.Join(root, "dist-policy")); !reflect.DeepEqual(got, reference) {
						t.Fatalf("the build with the policy differs from the build with no policy:\nwith policy: %v\nwithout: %v", keysOfTree(got), keysOfTree(reference))
					}
				})

				t.Run("the violation halts the build", func(t *testing.T) {
					root, catalogDir, release := renderVerdictCloudWorkspace(t, target.name, "-violation")
					err := pullThen(t, root, release, []string{catalogDir}, config, "dist")
					if err == nil {
						t.Fatal("the violation must halt the build")
					}
					if !errors.Is(err, analyzer.ErrPolicyViolation()) {
						t.Fatalf("want the policy's refusal, got: %v", err)
					}
					if !strings.Contains(err.Error(), target.wantSentence) {
						t.Fatalf("the refusal must read %q; got: %v", target.wantSentence, err)
					}
				})
			})
		}
	}
}
