// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package helmchartcore

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/pkg/namesegmenttest"
)

// readChartMeta is the test-side stand-in for the removed host chart-meta reader:
// it reads <dir>/Chart.yaml and resolves name/version through the shared
// ReadChartMetaBytes core, so the guardrail assertions exercise the same
// decode-and-validate core the umbrella assembly and the provider guest run.
func readChartMeta(dir string) (name, version string, err error) {
	data, rerr := os.ReadFile(filepath.Join(dir, "Chart.yaml"))
	if rerr != nil {
		return "", "", rerr
	}
	return ReadChartMetaBytes(data, dir)
}

// buildUmbrella is the test-side stand-in for the removed host umbrella adapter:
// it reads each sourced sub-chart's pristine Chart.yaml from disk into a
// filesystem-free CoreSubChart — mirroring the generic host projection the
// assembler now runs — then assembles through AssembleUmbrellaCore, the same core
// the provider guest runs.
func buildUmbrella(boundaryName, aggregateName string, subCharts []renderer.SubChartInfo, chartNameOverride string, indent int) (map[string][]byte, error) {
	subs := make([]CoreSubChart, 0, len(subCharts))
	for _, sc := range subCharts {
		cs := CoreSubChart{InstanceID: sc.InstanceID, Version: sc.Version, Values: sc.Values}
		if s, ok := sc.Source.(string); ok && s != "" {
			data, err := os.ReadFile(filepath.Join(s, "Chart.yaml"))
			if err != nil {
				return nil, err
			}
			cs.ChartYAML = data
		}
		subs = append(subs, cs)
	}
	return AssembleUmbrellaCore(boundaryName, aggregateName, subs, chartNameOverride, indent)
}

// TestHelmNameEncoding_RepresentativeInputs pins the derived spelling of the
// identities a chart name and an alias are actually built from: a name the
// alphabet already admits passes through, an authored hyphen is kept where the
// character after it is admitted, and each character the alphabet does not admit
// reaches its own escape. Non-vacuous — folding any two of the escaped
// characters onto one output reddens the rows carrying both.
func TestHelmNameEncoding_RepresentativeInputs(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in, want string
	}{
		{"simple", "simple"},
		{"data.main-db", "data-zdmain-db"},
		{"my_under_score", "my_under_score"},
		{"a.b_c", "a-zdb_c"},
		{"dist/backend/helm-chart", "dist-zsbackend-zshelm-chart"},
		{"dist/frontend/helm-chart", "dist-zsfrontend-zshelm-chart"},
		{"", "-ze"},
	}
	for _, tc := range cases {
		if got := helmNameEncoding.Segment(tc.in); got != tc.want {
			t.Errorf("helmNameEncoding.Segment(%q) = %q; want %q", tc.in, got, tc.want)
		}
	}
}

// TestHelmNameEncodingDistinguishesInputsThatShareOneName pins, pair by pair, the
// ways distinct authored identities could reach one derived name. Each row is two
// identities an author can legitimately write; the assertion is that they differ.
//
// Each row fails on a specific regression rather than on any change: mapping
// every unadmitted character onto one substitute reddens the punctuation rows,
// and a fixed word standing in for an absent identity reddens the absent row.
func TestHelmNameEncodingDistinguishesInputsThatShareOneName(t *testing.T) {
	t.Parallel()
	cases := []struct {
		left, right  string
		whatItGuards string
	}{
		{"data.main-db", "data-main-db", "the dot and the hyphen must not share one output"},
		{"data.main-db", "data_main-db", "the dot and the underscore must not share one output"},
		{"data.main-db", "data/main-db", "the dot and the path separator must not share one output"},
		{"dist/backend/helm-chart", "dist-backend-helm-chart", "a path separator must not become an authored hyphen"},
		{"", "none", "an absent identity must not be spelled as an authorable word"},
	}
	for _, tc := range cases {
		left, right := helmNameEncoding.Segment(tc.left), helmNameEncoding.Segment(tc.right)
		if left == right {
			t.Errorf("%q and %q both reach %q; %s", tc.left, tc.right, left, tc.whatItGuards)
		}
	}
}

// TestHelmNameSegmentIsInjective asserts no two distinct identities in the shared
// segment corpus reach one derived name. A collision is reported with both inputs
// that produced it, so a regression names the pair rather than only a count.
func TestHelmNameSegmentIsInjective(t *testing.T) {
	t.Parallel()
	seen := make(map[string]string)
	corpus := namesegmenttest.Corpus()
	for _, s := range corpus {
		got := helmNameEncoding.Segment(s)
		if prior, clash := seen[got]; clash {
			t.Fatalf("%q and %q both reach the derived name %q", prior, s, got)
		}
		seen[got] = s
	}
	if len(seen) != len(corpus) {
		t.Fatalf("built %d names from %d identities; each must reach its own name", len(seen), len(corpus))
	}
}

// TestHelmQualifiedChartNameIsInjective asserts the two-segment chart name — an
// aggregate name qualifying a dispatch path — is injective over the cross product
// of the shared corpus. It is a separate assertion from the one-segment case
// because the join is what carries it: replacing the encoding's two-hyphen join
// with a single hyphen leaves every segment distinct and still lets an aggregate
// name ending in a hyphen collide with a dispatch path beginning with one.
func TestHelmQualifiedChartNameIsInjective(t *testing.T) {
	t.Parallel()
	corpus := namesegmenttest.Corpus()
	seen := make(map[string][2]string)
	total := 0
	for _, a := range corpus {
		for _, b := range corpus {
			tuple := [2]string{a, b}
			name := helmNameEncoding.Name(a, b)
			total++
			if prior, clash := seen[name]; clash {
				t.Fatalf("%v and %v both reach the chart name %q", prior, tuple, name)
			}
			seen[name] = tuple
		}
	}
	if len(seen) != total {
		t.Fatalf("built %d names from %d pairs; each pair must reach its own name", len(seen), total)
	}
}

// TestHelmNameSegmentsAreRecoverable reads a qualified chart name back into the
// two identities it was built from and asserts each comes back exactly as
// supplied. Distinctness says no pair in the corpus collided; recovering the
// identities says why — the name still carries them. The reader shares no code
// with the encoder, because a reader that called into the encoder would agree
// with it whatever the encoder did.
func TestHelmNameSegmentsAreRecoverable(t *testing.T) {
	t.Parallel()
	corpus := namesegmenttest.Corpus()
	for _, a := range corpus {
		for _, b := range corpus {
			name := helmNameEncoding.Name(a, b)
			got := namesegmenttest.ReadName(t, name)
			if len(got) != 2 || got[0] != a || got[1] != b {
				t.Fatalf("chart name %q read back as %q, want [%q %q]", name, got, a, b)
			}
		}
	}
}

// TestHelmDerivedNameUsesOnlyTheAliasAlphabet asserts every byte a derived name
// emits is an ASCII letter, a digit, an underscore, or a hyphen — the whole of
// what a Helm dependency alias admits, and the reason one alphabet serves both
// the alias and the chart name. The corpus deliberately carries a space, a tab
// and a non-ASCII letter, none of which may survive.
func TestHelmDerivedNameUsesOnlyTheAliasAlphabet(t *testing.T) {
	t.Parallel()
	for _, s := range namesegmenttest.Corpus() {
		for _, name := range []string{helmNameEncoding.Segment(s), helmNameEncoding.Name(s, s)} {
			for i := 0; i < len(name); i++ {
				c := name[i]
				ok := c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-'
				if !ok {
					t.Fatalf("identity %q derived %q, which carries byte %q at %d", s, name, c, i)
				}
			}
		}
	}
}

// TestHelmDerivedNameDeterministic asserts a derived name is byte-stable across
// repeated calls with identical input (Deterministic Output invariant).
func TestHelmDerivedNameDeterministic(t *testing.T) {
	t.Parallel()
	first := helmNameEncoding.Name("fullstack-app", "dist/backend/helm-chart")
	for i := 0; i < 8; i++ {
		if again := helmNameEncoding.Name("fullstack-app", "dist/backend/helm-chart"); again != first {
			t.Fatalf("derived name not stable on run %d: %q != %q", i, again, first)
		}
	}
}

// TestRenderSourcedChart_HappyPath pins the sourced-render success path: a packed
// chart projection is rebuilt into the file map by RenderSourcedChartCore — the
// same render core the provider guest runs on the projection the host packs.
func TestRenderSourcedChart_HappyPath(t *testing.T) {
	t.Parallel()
	chartYAML := `apiVersion: v2
name: my-chart
version: 0.1.0
`
	proj := &renderer.SourceProjection{Files: []renderer.SourceFile{
		{Path: "Chart.yaml", Content: []byte(chartYAML)},
		{Path: "templates/deploy.yaml", Content: []byte("kind: Deployment\n")},
	}}
	files := RenderSourcedChartCore(proj)
	if _, has := files["Chart.yaml"]; !has {
		t.Errorf("Chart.yaml missing; got keys=%v", keys(files))
	}
	if _, has := files["templates/deploy.yaml"]; !has {
		t.Errorf("templates/deploy.yaml missing")
	}
}

func keys(m map[string][]byte) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// TestValidateSourcedChartListing_HappyPath pins that a directory listing
// carrying Chart.yaml at its root validates.
func TestValidateSourcedChartListing_HappyPath(t *testing.T) {
	t.Parallel()
	listing := &renderer.SourceListing{Root: "/acquired/chart", RootIsDir: true, Entries: []renderer.SourceEntry{
		{Path: "Chart.yaml", IsDir: false},
		{Path: "templates", IsDir: true},
		{Path: "templates/deploy.yaml", IsDir: false},
	}}
	if err := ValidateSourcedChartListing(listing); err != nil {
		t.Errorf("valid chart listing rejected: %v", err)
	}
}

// TestValidateSourcedChartListing_NotADirectory pins the not-a-directory
// rejection: a non-directory source (a file or archive) is not a chart.
func TestValidateSourcedChartListing_NotADirectory(t *testing.T) {
	t.Parallel()
	listing := &renderer.SourceListing{Root: "/acquired/notadir", RootIsDir: false}
	err := ValidateSourcedChartListing(listing)
	if err == nil {
		t.Fatal("non-directory source should reject")
	}
	if !strings.Contains(err.Error(), "is not a directory") {
		t.Errorf("error should name the not-a-directory cause; got %v", err)
	}
}

// TestValidateSourcedChartListing_MissingChartYAML pins the missing-Chart.yaml
// rejection.
func TestValidateSourcedChartListing_MissingChartYAML(t *testing.T) {
	t.Parallel()
	listing := &renderer.SourceListing{Root: "/acquired/nochart", RootIsDir: true, Entries: []renderer.SourceEntry{
		{Path: "templates.yaml", IsDir: false},
	}}
	err := ValidateSourcedChartListing(listing)
	if err == nil {
		t.Fatal("directory without Chart.yaml should reject")
	}
	if !strings.Contains(err.Error(), "has no Chart.yaml") {
		t.Errorf("error should name the missing Chart.yaml; got %v", err)
	}
}

// TestRenderSourcedChart_NestedFiles pins the recursive rebuild: a packed
// projection carrying nested forward-slash paths is rebuilt into the file map by
// the render core with those paths preserved.
func TestRenderSourcedChart_NestedFiles(t *testing.T) {
	t.Parallel()
	proj := &renderer.SourceProjection{Files: []renderer.SourceFile{
		{Path: "Chart.yaml", Content: []byte("apiVersion: v2\nname: n\nversion: 0.1.0\n")},
		{Path: "templates/deep/nested.yaml", Content: []byte("nested: true\n")},
	}}
	files := RenderSourcedChartCore(proj)
	if _, has := files["templates/deep/nested.yaml"]; !has {
		t.Errorf("nested file missing; got keys=%v", keys(files))
	}
}

// TestReadChartMeta_AllBranches pins missing-file, parse-error, empty-name,
// empty-version, and happy paths.
func TestReadChartMeta_AllBranches(t *testing.T) {
	t.Parallel()
	// Missing file.
	_, _, err := readChartMeta("/nonexistent/path")
	if err == nil {
		t.Errorf("missing path should error")
	}

	// Parse error: malformed YAML in Chart.yaml.
	tmp := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmp, "Chart.yaml"), []byte("::: not yaml :::"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, _, err = readChartMeta(tmp)
	if err == nil {
		t.Errorf("malformed YAML should error")
	}

	// Empty name.
	tmp2 := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmp2, "Chart.yaml"), []byte("apiVersion: v2\nversion: 0.1.0\n"), 0o644); err != nil {
		t.Fatalf("write empty: %v", err)
	}
	_, _, err = readChartMeta(tmp2)
	if err == nil {
		t.Errorf("empty name should error")
	}

	// Empty version (the parallel guard).
	tmp2b := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmp2b, "Chart.yaml"), []byte("apiVersion: v2\nname: real-name\n"), 0o644); err != nil {
		t.Fatalf("write empty version: %v", err)
	}
	if _, _, err := readChartMeta(tmp2b); err == nil {
		t.Errorf("empty version should error")
	}

	// Happy.
	tmp3 := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmp3, "Chart.yaml"), []byte("apiVersion: v2\nname: real-name\nversion: 0.1.0\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, gotVer, err := readChartMeta(tmp3)
	if err != nil {
		t.Fatalf("happy: %v", err)
	}
	if got != "real-name" {
		t.Errorf("name = %q; want real-name", got)
	}
	if gotVer != "0.1.0" {
		t.Errorf("version = %q; want 0.1.0", gotVer)
	}
}

// TestBuildUmbrella_AllBranches pins empty-boundary error, override-name
// path, default-name path with + without aggregate, sourced sub-chart
// (chart-name read), generative sub-chart (no Source), per-sub-chart
// values + nil Values.
func TestBuildUmbrella_AllBranches(t *testing.T) {
	t.Parallel()
	// Empty boundary name.
	_, err := buildUmbrella("", "", nil, "", 2)
	if err == nil {
		t.Errorf("empty boundary should error")
	}

	// Default name with aggregate prefix.
	files, err := buildUmbrella("web", "agg", nil, "", 2)
	if err != nil {
		t.Fatalf("default-with-agg: %v", err)
	}
	if !strings.Contains(string(files["Chart.yaml"]), "name: agg--web") {
		t.Errorf("aggregate-qualified name missing; got\n%s", files["Chart.yaml"])
	}

	// Default name without aggregate.
	files, err = buildUmbrella("solo", "", nil, "", 2)
	if err != nil {
		t.Fatalf("default-no-agg: %v", err)
	}
	if !strings.Contains(string(files["Chart.yaml"]), "name: solo") {
		t.Errorf("solo name missing; got\n%s", files["Chart.yaml"])
	}

	// Override name (cascade).
	files, err = buildUmbrella("web", "agg", nil, "override.name", 2)
	if err != nil {
		t.Fatalf("override: %v", err)
	}
	if !strings.Contains(string(files["Chart.yaml"]), "name: override-zdname") {
		t.Errorf("override name should carry the dot as an escape; got\n%s", files["Chart.yaml"])
	}

	// Generative sub-chart (no Source) + sub-chart with Values.
	subCharts := []renderer.SubChartInfo{
		{InstanceID: "gen-1", Version: "1.0.0", Values: map[string]any{"k": "v"}},
		{InstanceID: "gen-2", Version: ""}, // empty version → default
	}
	files, err = buildUmbrella("web", "", subCharts, "", 2)
	if err != nil {
		t.Fatalf("generative: %v", err)
	}
	// Both sub-charts should appear as dependencies.
	if !strings.Contains(string(files["Chart.yaml"]), "alias: gen-1") {
		t.Errorf("gen-1 dependency missing")
	}
	if !strings.Contains(string(files["Chart.yaml"]), "alias: gen-2") {
		t.Errorf("gen-2 dependency missing")
	}

	// Sourced sub-chart: requires reading Chart.yaml from disk.
	tmp := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmp, "Chart.yaml"), []byte("apiVersion: v2\nname: real-name\nversion: 0.1.0\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	sourcedSC := []renderer.SubChartInfo{
		{InstanceID: "sourced-i", Version: "1.0.0", Source: tmp},
	}
	files, err = buildUmbrella("web", "", sourcedSC, "", 2)
	if err != nil {
		t.Fatalf("sourced: %v", err)
	}
	if !strings.Contains(string(files["Chart.yaml"]), "name: real-name") {
		t.Errorf("sourced dependency should use chart's name; got\n%s", files["Chart.yaml"])
	}

	// Sourced sub-chart with missing Chart.yaml → propagates error.
	badSC := []renderer.SubChartInfo{
		{InstanceID: "bad-i", Source: "/nonexistent/path"},
	}
	_, err = buildUmbrella("web", "", badSC, "", 2)
	if err == nil {
		t.Errorf("missing source-chart should error")
	}
}

// TestBuildUmbrella_EmitsChart pins the umbrella assembly contract: a boundary
// with an aggregate name produces an umbrella Chart.yaml.
func TestBuildUmbrella_EmitsChart(t *testing.T) {
	t.Parallel()
	files, err := buildUmbrella("web", "agg", nil, "", 2)
	if err != nil {
		t.Fatalf("assemble: %v", err)
	}
	if _, has := files["Chart.yaml"]; !has {
		t.Errorf("Chart.yaml missing")
	}
}

// TestBuildUmbrella_EmptyBoundaryErrors pins the empty-boundary guardrail.
func TestBuildUmbrella_EmptyBoundaryErrors(t *testing.T) {
	t.Parallel()
	_, err := buildUmbrella("", "", nil, "", 2)
	if err == nil {
		t.Errorf("empty boundary should error")
	}
}
