// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package helmcore_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"text/template"

	"github.com/Masterminds/semver/v3"
	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/rendercore"
	"github.com/aggrega-ai/aggrega-cli/internal/plugincondition"
	"github.com/aggrega-ai/aggrega-cli/plugins/internal/helmcore"
	"helm.sh/helm/v4/pkg/chart/common"
	chart "helm.sh/helm/v4/pkg/chart/v2"
)

// kubeVersionChart is the fixture whose one ConfigMap writes the Kubernetes version
// it is rendered for, the helm version and one API version lookup, and branches on
// the version as a chart choosing its token mechanism does: from 1.21 a projected
// token, below it a first-party one.
const kubeVersionChart = "testdata/kube-version-chart"

// kubeVersionTemplate is the fixture's one template, named as the template engine
// names it in an execution error.
const kubeVersionTemplate = "kube-version/templates/configmap.yaml"

// kubeVersionReadingChart is a chart whose one ConfigMap writes the Kubernetes
// version it is rendered for and compares it with nothing, so what a chart reads is
// visible for a version the fixture's own comparison refuses.
func kubeVersionReadingChart(t *testing.T) *chart.Chart {
	t.Helper()
	return chartFromFiles(t, map[string]string{
		"Chart.yaml": "apiVersion: v2\nname: kube-version-reading\nversion: 0.1.0\n",
		"templates/configmap.yaml": "apiVersion: v1\nkind: ConfigMap\nmetadata:\n" +
			"  name: {{ .Release.Name }}-kube-version\ndata:\n" +
			"  version: {{ .Capabilities.KubeVersion.Version | quote }}\n" +
			"  major: {{ .Capabilities.KubeVersion.Major | quote }}\n" +
			"  minor: {{ .Capabilities.KubeVersion.Minor | quote }}\n",
	})
}

// renderedKubeVersion is what the fixture's ConfigMap reports.
type renderedKubeVersion struct {
	Version     string `yaml:"version"`
	Major       string `yaml:"major"`
	Minor       string `yaml:"minor"`
	Token       string `yaml:"token"`
	HelmVersion string `yaml:"helmVersion"`
	AppsV1      string `yaml:"appsV1"`
}

// renderKubeVersionChart renders the fixture for one node carrying annotations.
func renderKubeVersionChart(t *testing.T, annotations map[string]string) (files map[string][]byte, err error) {
	t.Helper()
	return renderKubeVersionOf(t, loadChart(t, kubeVersionChart), annotations)
}

// renderKubeVersionOf renders ch for one node carrying annotations, reporting a
// panic as a test failure rather than letting a recovered one pass.
func renderKubeVersionOf(t *testing.T, ch *chart.Chart, annotations map[string]string) (files map[string][]byte, err error) {
	t.Helper()
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("the render panicked: %v", r)
		}
	}()
	nc := nodeCtx("api", kubeVersionChart)
	nc.BoundaryPath = "prod"
	nc.Annotations = annotations
	return helmcore.RenderLoadedChart(context.Background(), ch,
		map[string]any{}, nc, helmcore.NewHermeticValuesValidator(forbidFetch(t)))
}

// readKubeVersion decodes the fixture's one ConfigMap out of a render.
func readKubeVersion(t *testing.T, files map[string][]byte) renderedKubeVersion {
	t.Helper()
	if len(files) != 1 {
		t.Fatalf("the fixture renders one ConfigMap; got %d files: %v", len(files), sortedFileKeys(files))
	}
	for _, content := range files {
		var doc struct {
			Data renderedKubeVersion `yaml:"data"`
		}
		if err := yaml.Unmarshal(content, &doc); err != nil {
			t.Fatalf("decode the rendered ConfigMap: %v\n%s", err, content)
		}
		return doc.Data
	}
	panic("unreachable")
}

// TestRenderChart_RendersForTheKubernetesVersionTheAnnotationNames holds what a
// chart reads as .Capabilities.KubeVersion for each form of the annotation: absent
// or empty, the version `helm template` of the SDK's own release renders for; set,
// exactly what the SDK's own `--kube-version` parser makes of it — so "1.20" is
// "v1.20", not a version padded with a patch the author did not write, and text
// after the numbers, or space around them, stays in the version as written. Every
// other capability is the SDK's built-in one in every row.
func TestRenderChart_RendersForTheKubernetesVersionTheAnnotationNames(t *testing.T) {
	t.Parallel()

	def, err := helmcore.DefaultKubeVersion()
	if err != nil {
		t.Fatalf("the default Kubernetes version: %v", err)
	}

	for _, tc := range []struct {
		name string
		// annotations is the node's merged annotation map; nil is a node carrying none.
		annotations map[string]string
		// version, major and minor are what the chart reads; empty version means the
		// default.
		version, major, minor string
		token                 string
		// comparisonRefuses is set where the fixture chart's own semverCompare
		// refuses the version text the parser writes: the render then fails in the
		// fixture's template, as `helm template` given the same flag value does, and
		// what the chart reads is read from a chart that compares nothing.
		comparisonRefuses bool
	}{
		{
			name:  "no annotation renders for the client library's Kubernetes version",
			token: "projected",
		},
		{
			name:        "an empty value is unset, as an empty --kube-version is",
			annotations: map[string]string{rendercore.AnnotationKubernetesVersion: ""},
			token:       "projected",
		},
		{
			name:        "1.20 renders for v1.20 and the chart's branch below 1.21",
			annotations: map[string]string{rendercore.AnnotationKubernetesVersion: "1.20"},
			version:     "v1.20", major: "1", minor: "20",
			token: "first-party",
		},
		{
			name:        "a full version with its v is kept whole",
			annotations: map[string]string{rendercore.AnnotationKubernetesVersion: "v1.31.2"},
			version:     "v1.31.2", major: "1", minor: "31",
			token: "projected",
		},
		{
			name:        "a vendor suffix is kept in the version as the SDK's parser keeps it",
			annotations: map[string]string{rendercore.AnnotationKubernetesVersion: "1.29.3-gke.100"},
			version:     "v1.29.3-gke.100", major: "1", minor: "29",
			token: "projected",
		},
		{
			name:        "surrounding space is kept in the version as the SDK's parser keeps it",
			annotations: map[string]string{rendercore.AnnotationKubernetesVersion: " 1.30 "},
			version:     "v 1.30 ", major: "1", minor: "30",
			comparisonRefuses: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			want := common.KubeVersion{Version: tc.version, Major: tc.major, Minor: tc.minor}
			if tc.version == "" {
				want = def
			} else {
				// One parser: what the chart reads is exactly what the SDK's own flag
				// parser makes of the authored value.
				parsed, err := common.ParseKubeVersion(tc.annotations[rendercore.AnnotationKubernetesVersion])
				if err != nil {
					t.Fatalf("the SDK's parser refuses this row's value: %v", err)
				}
				if parsed.Version != want.Version || parsed.Major != want.Major || parsed.Minor != want.Minor {
					t.Fatalf("this row expects %q (major %q, minor %q), but the SDK's parser yields %q (major %q, minor %q)",
						want.Version, want.Major, want.Minor, parsed.Version, parsed.Major, parsed.Minor)
				}
			}

			if tc.comparisonRefuses {
				files, err := renderKubeVersionOf(t, kubeVersionReadingChart(t), tc.annotations)
				if err != nil {
					t.Fatalf("render the version with no comparison: %s", errorChainText(err))
				}
				if got := readKubeVersion(t, files); got.Version != want.Version || got.Major != want.Major || got.Minor != want.Minor {
					t.Errorf("the chart read %q (major %q, minor %q), want %q (major %q, minor %q)",
						got.Version, got.Major, got.Minor, want.Version, want.Major, want.Minor)
				}

				// The refusal is the chart's own: sprig's semverCompare returns the
				// semver parser's invalid-version error, which the template engine
				// reports as the execution error of the fixture's template. The
				// render's own frame names only the node, so the cause is read from
				// the chain beneath it.
				files, err = renderKubeVersionChart(t, tc.annotations)
				if err == nil {
					t.Fatalf("the chart's own comparison of %q did not refuse it; files %v", want.Version, sortedFileKeys(files))
				}
				if !errors.Is(err, semver.ErrInvalidSemVer) {
					t.Errorf("the refusal does not carry the semver parser's invalid-version error: %s", errorChainText(err))
				}
				var execErr template.ExecError
				if !errors.As(err, &execErr) || execErr.Name != kubeVersionTemplate {
					t.Errorf("the refusal is not an execution error of %s: %s", kubeVersionTemplate, errorChainText(err))
				}
				if !strings.Contains(errorChainText(err), "error calling semverCompare") {
					t.Errorf("the refusal is not the chart's semverCompare call: %s", errorChainText(err))
				}
				if len(files) != 0 {
					t.Errorf("a refused render still emitted files: %v", sortedFileKeys(files))
				}
				return
			}

			files, err := renderKubeVersionChart(t, tc.annotations)
			if err != nil {
				t.Fatalf("render: %v", err)
			}
			got := readKubeVersion(t, files)
			if got.Version != want.Version || got.Major != want.Major || got.Minor != want.Minor {
				t.Errorf("the chart rendered for %q (major %q, minor %q), want %q (major %q, minor %q)",
					got.Version, got.Major, got.Minor, want.Version, want.Major, want.Minor)
			}
			if got.Token != tc.token {
				t.Errorf("the chart took its %q branch at %s, want %q", got.Token, got.Version, tc.token)
			}

			builtIn := common.DefaultCapabilities
			if got.HelmVersion != builtIn.HelmVersion.Version {
				t.Errorf("the chart read helm version %q, want the SDK's own %q", got.HelmVersion, builtIn.HelmVersion.Version)
			}
			if wantApps := strconv.FormatBool(builtIn.APIVersions.Has("apps/v1")); got.AppsV1 != wantApps {
				t.Errorf("the chart read apps/v1 availability %q, want the SDK's own %q", got.AppsV1, wantApps)
			}
		})
	}
}

// TestRenderChart_AKubernetesVersionTheParserRefusesHalts holds the halt: a value
// the SDK's parser refuses stops the render under the value-not-representable
// condition, naming the node, its boundary, the annotation and the value, and is
// never rendered for the default instead.
func TestRenderChart_AKubernetesVersionTheParserRefusesHalts(t *testing.T) {
	t.Parallel()

	for _, value := range []string{"one.two", "1", "latest", " ", "01.20"} {
		t.Run(strconv.Quote(value), func(t *testing.T) {
			t.Parallel()
			files, err := renderKubeVersionChart(t, map[string]string{rendercore.AnnotationKubernetesVersion: value})
			if err == nil {
				t.Fatalf("a chart rendered for the unparsable version %q; files: %v", value, sortedFileKeys(files))
			}
			if !errors.Is(err, plugincondition.ErrPluginValueNotRepresentable()) {
				t.Fatalf("the halt does not carry the value-not-representable condition: %v", err)
			}
			if len(files) != 0 {
				t.Errorf("a refused render still emitted files: %v", sortedFileKeys(files))
			}
			for _, want := range []string{
				`helm: node "api" in boundary "prod": annotation aggrega.ai/kubernetes-version: ` + strconv.Quote(value) +
					` is not a Kubernetes version a chart can be rendered for`,
				`such as "1.31" or "v1.31.2"`,
			} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the halt does not carry %q:\n%v", want, err)
				}
			}
		})
	}
}

// TestRenderChart_TheKubernetesVersionReachesOneRenderOnly holds that a node's
// version is written into that render's capabilities alone: the SDK's built-in
// capabilities are a shared value, and a version written into them would be read
// by every later render, the default ones included.
func TestRenderChart_TheKubernetesVersionReachesOneRenderOnly(t *testing.T) {
	t.Parallel()

	before := common.DefaultCapabilities.KubeVersion
	if _, err := renderKubeVersionChart(t, map[string]string{rendercore.AnnotationKubernetesVersion: "1.24"}); err != nil {
		t.Fatalf("render for 1.24: %v", err)
	}
	if after := common.DefaultCapabilities.KubeVersion; after != before {
		t.Fatalf("a render wrote its version into the SDK's built-in capabilities: %+v, was %+v", after, before)
	}
	files, err := renderKubeVersionChart(t, nil)
	if err != nil {
		t.Fatalf("render with no annotation: %v", err)
	}
	def, err := helmcore.DefaultKubeVersion()
	if err != nil {
		t.Fatalf("the default Kubernetes version: %v", err)
	}
	if got := readKubeVersion(t, files); got.Version != def.Version {
		t.Errorf("a render naming no version rendered for %q after one naming 1.24, want the default %q", got.Version, def.Version)
	}
}

// TestRenderChart_RenderingForANamedVersionIsDeterministic holds byte-identity of
// two renders of one node for one named version.
func TestRenderChart_RenderingForANamedVersionIsDeterministic(t *testing.T) {
	t.Parallel()

	annotations := map[string]string{rendercore.AnnotationKubernetesVersion: "1.20"}
	first, err := renderKubeVersionChart(t, annotations)
	if err != nil {
		t.Fatalf("first render: %v", err)
	}
	second, err := renderKubeVersionChart(t, annotations)
	if err != nil {
		t.Fatalf("second render: %v", err)
	}
	if len(first) != len(second) {
		t.Fatalf("the two renders emitted %v and %v", sortedFileKeys(first), sortedFileKeys(second))
	}
	for name, content := range first {
		if !bytes.Equal(content, second[name]) {
			t.Errorf("%s differs between two renders:\n%s\n---\n%s", name, content, second[name])
		}
	}
}

// TestDefaultKubeVersion_IsWhatHelmDerivesForTheClientLibraryThisModuleRequires
// fails when the default and the client library diverge. Its oracle reads the
// k8s.io/client-go requirement from this module's go.mod, which is the version a
// binary built from this module records in its build information and helm derives
// its own default from, and applies helm's rule — the module's major version plus
// one, its minor version, a zero patch — with its own reading and its own
// arithmetic. So a default written down by hand, or read from anything other than
// the module's build-information entry, fails here the first time the requirement
// moves.
//
// In this test binary that entry is the one TestMain supplies from the same go.mod,
// since a library package's test binary records none, so this test proves how the
// default is read and mapped, not that the provider's own build records the entry.
// That half is proved through the embedded provider by
// TestHelmKubernetesVersion_TheChartRendersForTheVersionTheReleaseNames in
// internal/compiler, which reads go.mod on its own and compares the default the
// provider renders for against it. The go.mod reading here is deliberately not
// shared with TestMain's: one reading cannot disagree with itself.
func TestDefaultKubeVersion_IsWhatHelmDerivesForTheClientLibraryThisModuleRequires(t *testing.T) {
	t.Parallel()

	goMod, err := os.ReadFile(filepath.Join("..", "..", "..", "go.mod"))
	if err != nil {
		t.Fatalf("read the module's go.mod: %v", err)
	}
	var required string
	for _, line := range strings.Split(string(goMod), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[0] == "k8s.io/client-go" {
			required = fields[1]
			break
		}
	}
	if required == "" {
		t.Fatalf("go.mod requires no k8s.io/client-go; the default has no client library to follow")
	}
	parts := strings.Split(strings.TrimPrefix(required, "v"), ".")
	major, err := strconv.Atoi(parts[0])
	if err != nil || len(parts) < 2 {
		t.Fatalf("go.mod requires k8s.io/client-go %q, which carries no major and minor version", required)
	}
	want := common.KubeVersion{
		Version: "v" + strconv.Itoa(major+1) + "." + parts[1] + ".0",
		Major:   strconv.Itoa(major + 1),
		Minor:   parts[1],
	}

	got, err := helmcore.DefaultKubeVersion()
	if err != nil {
		t.Fatalf("the default Kubernetes version: %v", err)
	}
	if got.Version != want.Version || got.Major != want.Major || got.Minor != want.Minor {
		t.Errorf("the default Kubernetes version is %q (major %q, minor %q); helm derives %q (major %q, minor %q) for k8s.io/client-go %s",
			got.Version, got.Major, got.Minor, want.Version, want.Major, want.Minor, required)
	}
}
