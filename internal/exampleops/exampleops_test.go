// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package exampleops

import (
	"slices"
	"testing"
)

// TestScaffoldExampleListReadsEachExampleOnceInNameOrder pins the reading of the
// list-of-examples page: an example listed under several products and
// capabilities is one example, the order is by name, and a line that only looks
// like an entry — a link to another page, prose, a heading — is not one.
func TestScaffoldExampleListReadsEachExampleOnceInNameOrder(t *testing.T) {
	t.Parallel()
	page := []byte("<!-- Generated -->\n\n# Examples by product and capability\n\n" +
		"## By product\n\n### `core/datastore/redis`\n\n" +
		"- [redis-cache](redis-cache/README.md) (`start`) — This lean example compiles a single Redis.\n" +
		"- [apisix-stack](apisix-stack/README.md) (`intermediate`) — A gateway in front of two services.\n\n" +
		"## By capability\n\n### `credentials`\n\n" +
		"- [redis-cache](redis-cache/README.md) (`start`) — This lean example compiles a single Redis.\r\n" +
		"- [Runnable Examples](README.md) — the levels.\n" +
		"- [mismatched](other/README.md) (`start`) — A link to a page it does not name.\n" +
		"Plain prose naming [redis-cache](redis-cache/README.md).\n")

	got := ParseIndex(page)
	want := []Example{
		{Name: "apisix-stack", Level: "intermediate", Summary: "A gateway in front of two services."},
		{Name: "redis-cache", Level: "start", Summary: "This lean example compiles a single Redis."},
	}
	if !slices.Equal(got, want) {
		t.Errorf("ParseIndex = %+v, want %+v", got, want)
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
