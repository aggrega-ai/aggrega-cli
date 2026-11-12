// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package exampleops

import (
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// TestScaffoldExampleListReadsEachExampleOnceInPageOrder pins the reading of the
// list-of-examples page: an example listed under several products and
// capabilities is one example, kept where the page first names it, the order is
// the page's own rather than by name, and a line that only looks like an entry —
// a link to another page, prose, a heading — is not one.
func TestScaffoldExampleListReadsEachExampleOnceInPageOrder(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		page string
		want []Example
	}{
		{
			name: "products and capabilities only: the first line naming each example places it",
			page: "<!-- Generated -->\n\n# Examples by product and capability\n\n" +
				"## By product\n\n### `core/datastore/redis`\n\n" +
				"- [redis-cache](redis-cache/README.md) (`start`) — This lean example compiles a single Redis.\n" +
				"- [apisix-stack](apisix-stack/README.md) (`intermediate`) — A gateway in front of two services.\n\n" +
				"## By capability\n\n### `credentials`\n\n" +
				"- [redis-cache](redis-cache/README.md) (`start`) — This lean example compiles a single Redis.\r\n" +
				"- [Runnable Examples](README.md) — the levels.\n" +
				"- [mismatched](other/README.md) (`start`) — A link to a page it does not name.\n" +
				"Plain prose naming [redis-cache](redis-cache/README.md).\n",
			want: []Example{
				{Name: "redis-cache", Level: "start", Summary: "This lean example compiles a single Redis."},
				{Name: "apisix-stack", Level: "intermediate", Summary: "A gateway in front of two services."},
			},
		},
		{
			name: "a teaching-order section first: its order holds over the sections after it",
			page: "<!-- Generated -->\n\n# Examples by product and capability\n\n" +
				"## In teaching order\n\n### Start here\n\nTier `start-here`: the first examples.\n\n" +
				"- [quickstart-web](quickstart-web/README.md) (`start`) — A web application grown one step at a time.\n" +
				"- [terraform-rds](terraform-rds/README.md) (`advanced`) — A managed database through Terraform.\n\n" +
				"### Catalog only\n\nTier `catalog-only`: catalog entities only.\n\n" +
				"- [redis-cache](redis-cache/README.md) (`start`) — This lean example compiles a single Redis.\n\n" +
				"### Importers\n\nTier `importers`: projects in another format.\n\n" +
				"- [import-compose](import-compose/README.md) (`authoring`) — A Compose file drafted into Aggrega files.\n\n" +
				"## By product\n\n### `core/datastore/postgres`\n\n" +
				"- [terraform-rds](terraform-rds/README.md) (`advanced`) — A managed database through Terraform.\n" +
				"- [quickstart-web](quickstart-web/README.md) (`start`) — A web application grown one step at a time.\n\n" +
				"### `core/datastore/redis`\n\n" +
				"- [redis-cache](redis-cache/README.md) (`start`) — This lean example compiles a single Redis.\n\n" +
				"## By capability\n\n### `import`\n\n" +
				"- [import-compose](import-compose/README.md) (`authoring`) — A Compose file drafted into Aggrega files.\n",
			want: []Example{
				{Name: "quickstart-web", Level: "start", Summary: "A web application grown one step at a time."},
				{Name: "terraform-rds", Level: "advanced", Summary: "A managed database through Terraform."},
				{Name: "redis-cache", Level: "start", Summary: "This lean example compiles a single Redis."},
				{Name: "import-compose", Level: "authoring", Summary: "A Compose file drafted into Aggrega files."},
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := ParseIndex([]byte(tc.page)); !slices.Equal(got, tc.want) {
				t.Errorf("ParseIndex = %+v, want %+v", got, tc.want)
			}
		})
	}
}

// TestScaffoldExampleNextCommandsAreTheBuildSectionsShellBlock pins which lines
// are reported as the commands to run next: the first shell block under the
// level-two Build heading, as written — not a block before that heading, not a
// block of another language inside it, and nothing from the next section.
func TestScaffoldExampleNextCommandsAreTheBuildSectionsShellBlock(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		page string
		want []string
	}{
		{
			name: "the shell block under Build, comments and blank lines kept, edges trimmed",
			page: "---\nexample:\n  level: start\n---\n\n# Redis\n\n```sh\nnot this\n```\n\n## Files\n\n" +
				"## Build\n\nProse first:\n\n```yaml\nkey: value\n```\n\n```sh\n\n# Compose\naggrega build --release a.yaml\n\n" +
				"aggrega build --release b.yaml\n\n```\n\n```sh\nnor this\n```\n\n## Credential note\n",
			want: []string{"# Compose", "aggrega build --release a.yaml", "", "aggrega build --release b.yaml"},
		},
		{
			name: "a bash block, fenced with tildes",
			page: "# X\n\n## Build\n\n~~~bash\naggrega build --release r.yaml\n~~~\n",
			want: []string{"aggrega build --release r.yaml"},
		},
		{
			name: "a Build heading of another level is not the section",
			page: "# X\n\n### Build\n\n```sh\naggrega build\n```\n",
			want: nil,
		},
		{
			name: "a section with no shell block",
			page: "# X\n\n## Build\n\nRun the build.\n\n## Next\n\n```sh\nnot this\n```\n",
			want: nil,
		},
		{
			name: "a heading inside a block does not end the section",
			page: "# X\n\n## Build\n\n```text\n## Not a heading\n```\n\n```shell\naggrega build\n```\n",
			want: []string{"aggrega build"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := NextCommands([]byte(tc.page)); !slices.Equal(got, tc.want) {
				t.Errorf("NextCommands = %q, want %q", got, tc.want)
			}
		})
	}
}

// sharedBuildSectionCase is one page whose Build section both this package and
// the documentation's own scripts read.
type sharedBuildSectionCase struct {
	name string
	page string
	want []string
}

// sharedBuildSectionCases are the pages the documentation's scripts read with
// their own copy of the reading NextCommands does: those scripts run where no
// aggrega binary is built, so they restate it, and both sides are held to these
// pages and these answers. Each is one page of scripts/testdata/build-commands/ in
// the documentation repository, byte for byte, and its want is that page's entry
// in scripts/testdata/build-commands.expected there.
//
// The list is written out here, and not read from those files, so that this
// package's tests keep their floor in a checkout holding no documentation;
// TestScaffoldExampleNextCommandsReadsTheDocumentationsBuildSectionCases holds the
// copy to the files wherever they are present, naming any case missing from
// either side or differing. Change a case in both places.
var sharedBuildSectionCases = []sharedBuildSectionCase{
	{
		name: "backtick-info",
		page: "# A backtick in a backtick fence's info word opens nothing\n\n## Build\n\n```sh `inline`\naggrega build --release not-a-block.yaml\n\n```bash\naggrega build --release release.yaml --auto-pull\n```\n",
		want: []string{"aggrega build --release release.yaml --auto-pull"},
	},
	{
		name: "blank-block",
		page: "# A Build block holding only blank lines\n\n## Build\n\n```sh\n\n   \n\n```\n",
		want: nil,
	},
	{
		name: "building-heading",
		page: "# A heading that is not exactly Build\n\n## Building\n\n```sh\naggrega build --release release.yaml --auto-pull\n```\n",
		want: nil,
	},
	{
		name: "closing-hashes",
		page: "# A Build heading with a closing run of #\n\n## Build ##\n\n```sh\naggrega build --release release.yaml --auto-pull\n```\n",
		want: []string{"aggrega build --release release.yaml --auto-pull"},
	},
	{
		name: "normal",
		page: "# A page with its sections in order\n\n## Scaffold the example\n\n```sh\naggrega scaffold --example demo\n```\n\n```sh\ncd demo\n```\n\n## Build\n\nBuild each Release:\n\n```yaml\nnot: commands\n```\n\n```sh\n\n# Compose\naggrega build --release release-compose.yaml --auto-pull\n\naggrega build --release release-helm.yaml --auto-pull\n\n```\n\n```sh\nnot the first block\n```\n\n## Next\n\n```sh\nnot this section\n```\n",
		want: []string{"# Compose", "aggrega build --release release-compose.yaml --auto-pull", "", "aggrega build --release release-helm.yaml --auto-pull"},
	},
	{
		name: "tab-indent",
		page: "# A fence indented by a tab opens nothing\n\n## Build\n\n\t```sh\n\taggrega build --release not-a-block.yaml\n\t```\n\n```sh\naggrega build --release release.yaml --auto-pull\n```\n",
		want: []string{"aggrega build --release release.yaml --auto-pull"},
	},
	{
		name: "two-build-sections",
		page: "# The first shell block under any Build heading\n\n## Build\n\n```yaml\nnot: commands\n```\n\n## Notes\n\n```sh\nnot under Build\n```\n\n## Build\n\n```bash\naggrega build --release second.yaml --auto-pull\n```\n",
		want: []string{"aggrega build --release second.yaml --auto-pull"},
	},
	{
		name: "unclosed-build-block",
		page: "# A Build block left open runs to the end of the page\n\n## Build\n\n```sh\naggrega build --release release.yaml --auto-pull\n\n",
		want: []string{"aggrega build --release release.yaml --auto-pull"},
	},
	{
		name: "unclosed-fence-before-build",
		page: "# A fence left open hides the Build heading\n\n~~~text\nleft open\n\n## Build\n\n```sh\naggrega build --release release.yaml --auto-pull\n```\n",
		want: nil,
	},
}

// TestScaffoldExampleNextCommandsReadsTheSharedBuildSectionCases pins the reading
// of the pages sharedBuildSectionCases lists, in any checkout.
func TestScaffoldExampleNextCommandsReadsTheSharedBuildSectionCases(t *testing.T) {
	t.Parallel()
	for _, tc := range sharedBuildSectionCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := NextCommands([]byte(tc.page)); !slices.Equal(got, tc.want) {
				t.Errorf("NextCommands = %q, want %q", got, tc.want)
			}
		})
	}
}

// buildSectionCasesDir and buildSectionCasesExpected are the documentation's
// case pages and their expected reading, relative to this package's directory in
// a checkout holding the documentation beside this module.
const (
	buildSectionCasesDir      = "../../../aggrega-docs/scripts/testdata/build-commands"
	buildSectionCasesExpected = "../../../aggrega-docs/scripts/testdata/build-commands.expected"
)

// TestScaffoldExampleNextCommandsReadsTheDocumentationsBuildSectionCases reads
// the documentation's case pages and expected reading, where the checkout holds
// them, and holds NextCommands and sharedBuildSectionCases to both: every page
// read by NextCommands gives the commands its expected entry lists, every page
// has an entry and every entry a page, and every page and entry is a row of
// sharedBuildSectionCases with the same bytes and the same answer, and the reverse.
// A checkout without the documentation skips it; the table test above still runs.
func TestScaffoldExampleNextCommandsReadsTheDocumentationsBuildSectionCases(t *testing.T) {
	t.Parallel()
	entries, err := os.ReadDir(buildSectionCasesDir)
	if err != nil {
		t.Skipf("the documentation's build-section cases are not present at %s (standalone checkout): %v", buildSectionCasesDir, err)
	}
	expected := readBuildSectionExpected(t, buildSectionCasesExpected)
	pages := map[string]string{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		data, rerr := os.ReadFile(filepath.Join(buildSectionCasesDir, e.Name()))
		if rerr != nil {
			t.Fatalf("read %s: %v", e.Name(), rerr)
		}
		pages[strings.TrimSuffix(e.Name(), ".md")] = string(data)
	}
	if len(pages) == 0 {
		t.Fatalf("no case page under %s", buildSectionCasesDir)
	}

	for _, name := range slices.Sorted(maps.Keys(pages)) {
		want, ok := expected[name]
		if !ok {
			t.Errorf("case %s: the page has no entry in %s", name, buildSectionCasesExpected)
			continue
		}
		if got := NextCommands([]byte(pages[name])); !slices.Equal(got, want) {
			t.Errorf("case %s: NextCommands reads the page as %q, the expected file says %q", name, got, want)
		}
	}
	for _, name := range slices.Sorted(maps.Keys(expected)) {
		if _, ok := pages[name]; !ok {
			t.Errorf("case %s: the expected file has an entry and %s has no page", name, buildSectionCasesDir)
		}
	}

	table := map[string]sharedBuildSectionCase{}
	for _, tc := range sharedBuildSectionCases {
		table[tc.name] = tc
	}
	for _, name := range slices.Sorted(maps.Keys(pages)) {
		tc, ok := table[name]
		switch {
		case !ok:
			t.Errorf("case %s: the documentation has the page and sharedBuildSectionCases has no row", name)
		case tc.page != pages[name]:
			t.Errorf("case %s: the row's page differs from %s/%s.md", name, buildSectionCasesDir, name)
		}
		if want, eok := expected[name]; ok && eok && !slices.Equal(tc.want, want) {
			t.Errorf("case %s: the row wants %q, the expected file says %q", name, tc.want, want)
		}
	}
	for _, name := range slices.Sorted(maps.Keys(table)) {
		if _, ok := pages[name]; !ok {
			t.Errorf("case %s: sharedBuildSectionCases has the row and %s has no page", name, buildSectionCasesDir)
		}
	}
}

// readBuildSectionExpected reads the expected reading of the case pages: a line
// "case <name>" opens each case, and every "| <line>" after it is one command
// line its page gives, as written. A case with no such line gives none.
func readBuildSectionExpected(t *testing.T, path string) map[string][]string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("the case pages are present and their expected reading is not: %v", err)
	}
	out := map[string][]string{}
	name := ""
	for i, line := range strings.Split(strings.TrimSuffix(string(data), "\n"), "\n") {
		switch {
		case strings.HasPrefix(line, "case "):
			name = strings.TrimPrefix(line, "case ")
			if _, dup := out[name]; dup {
				t.Fatalf("%s:%d: case %s is listed twice", path, i+1, name)
			}
			out[name] = nil
		case strings.HasPrefix(line, "| ") && name != "":
			out[name] = append(out[name], strings.TrimPrefix(line, "| "))
		default:
			t.Fatalf("%s:%d: neither a case line nor a command line: %q", path, i+1, line)
		}
	}
	return out
}
