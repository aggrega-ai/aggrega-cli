// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package entitypin

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/readscope"
)

// linkInto lays a symbolic link at link pointing at target, making the directory
// that holds it first, and skips the test where the platform cannot.
func linkInto(t *testing.T, target, link string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symbolic links are not available here: %v", err)
	}
}

// TestRefuseSharedDirectoryReadsANeighbourOnlyFromInsideItsRoot drives the
// directory rule over neighbours that are links, one fault per row beside
// controls.
//
// The rows discriminate a neighbour read through its link from one judged first:
// a neighbour leading out to an ENTITY would, once read, be named by the
// shared-directory refusal, so the read-hold refusal arriving in its place is the
// evidence that nothing was read; a neighbour leading out to a document of no
// entity kind would, once read, be admitted, so a refusal there is the same
// evidence the other way.
func TestRefuseSharedDirectoryReadsANeighbourOnlyFromInsideItsRoot(t *testing.T) {
	cases := []struct {
		name string
		// layout lays out the files under ws and outside and returns the root the
		// read is held to and the subject's path.
		layout func(t *testing.T, ws, outside string) (root, subject string)
		// held says the read-hold refusal must halt the rule, naming the link the
		// row laid beside the subject (other.yaml).
		held bool
		// sharedNaming is the neighbour the shared-directory refusal must name;
		// empty means not refused by it.
		sharedNaming string
	}{
		{
			name: "a neighbour that is a link to an entity outside the root is not read, and the rule halts naming the link",
			layout: func(t *testing.T, ws, outside string) (string, string) {
				writeFile(t, filepath.Join(outside, "thing.yaml"), entityDoc("Component", "outside/thing"))
				linkInto(t, filepath.Join(outside, "thing.yaml"), filepath.Join(ws, "catalog", "e", "other.yaml"))
				return ws, subjectRecipe(t, filepath.Join(ws, "catalog", "e"))
			},
			held: true,
		},
		{
			name: "a neighbour that is a link to a document of no entity kind outside the root halts too, since the verdict turns on it",
			layout: func(t *testing.T, ws, outside string) (string, string) {
				writeFile(t, filepath.Join(outside, "compose.yaml"), "services:\n  web:\n    image: nginx\n")
				linkInto(t, filepath.Join(outside, "compose.yaml"), filepath.Join(ws, "catalog", "e", "other.yaml"))
				return ws, subjectRecipe(t, filepath.Join(ws, "catalog", "e"))
			},
			held: true,
		},
		{
			name: "control: a neighbour that is a link to a document inside the root is read, and a document of no kind is not in the way",
			layout: func(t *testing.T, ws, outside string) (string, string) {
				writeFile(t, filepath.Join(ws, "fragments", "compose.yaml"), "services:\n  web:\n    image: nginx\n")
				linkInto(t, filepath.Join(ws, "fragments", "compose.yaml"), filepath.Join(ws, "catalog", "e", "other.yaml"))
				return ws, subjectRecipe(t, filepath.Join(ws, "catalog", "e"))
			},
		},
		{
			name: "control: a neighbour that is a link to an entity inside the root is read, and is in the way",
			layout: func(t *testing.T, ws, outside string) (string, string) {
				writeFile(t, filepath.Join(ws, "elsewhere", "thing.yaml"), entityDoc("Component", "inside/thing"))
				linkInto(t, filepath.Join(ws, "elsewhere", "thing.yaml"), filepath.Join(ws, "catalog", "e", "other.yaml"))
				return ws, subjectRecipe(t, filepath.Join(ws, "catalog", "e"))
			},
			sharedNaming: `Component "inside/thing" in other.yaml`,
		},
		{
			name: "a directory link named like a document, leading out, is not beside the entity and is not judged",
			layout: func(t *testing.T, ws, outside string) (string, string) {
				writeFile(t, filepath.Join(outside, "nested", "thing.yaml"), entityDoc("Component", "outside/thing"))
				linkInto(t, filepath.Join(outside, "nested"), filepath.Join(ws, "catalog", "e", "nested.yaml"))
				return ws, subjectRecipe(t, filepath.Join(ws, "catalog", "e"))
			},
		},
		{
			name: "an entity held to its own directory: a neighbour leading out of that directory halts",
			layout: func(t *testing.T, ws, outside string) (string, string) {
				own := filepath.Join(outside, "typed")
				writeFile(t, filepath.Join(outside, "compose.yaml"), "services:\n  web:\n    image: nginx\n")
				linkInto(t, filepath.Join(outside, "compose.yaml"), filepath.Join(own, "other.yaml"))
				return own, subjectRecipe(t, own)
			},
			held: true,
		},
		{
			name: "with no root nothing is held, and the neighbour is read where its link leads",
			layout: func(t *testing.T, ws, outside string) (string, string) {
				writeFile(t, filepath.Join(outside, "thing.yaml"), entityDoc("Component", "outside/thing"))
				linkInto(t, filepath.Join(outside, "thing.yaml"), filepath.Join(ws, "catalog", "e", "other.yaml"))
				return "", subjectRecipe(t, filepath.Join(ws, "catalog", "e"))
			},
			sharedNaming: `Component "outside/thing" in other.yaml`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ws, outside := t.TempDir(), t.TempDir()
			root, subject := tc.layout(t, ws, outside)

			err := RefuseSharedDirectory(root, subject)
			switch {
			case tc.held:
				if !errors.Is(err, readscope.ErrReadLeadsOutOfWorkspace()) {
					t.Fatalf("the neighbour must not be read; want the read-leads-out-of-workspace halt, got %v", err)
				}
				if errors.Is(err, ErrEntitySharesItsDirectory()) {
					t.Fatalf("the halt must be the read hold, not a verdict reached by reading the neighbour: %v", err)
				}
				if code, _ := cerrors.ShortCodeOf(err); code != "E0321" {
					t.Errorf("short code = %q, want E0321", code)
				}
				for _, want := range []string{
					"the link " + filepath.Join(filepath.Dir(subject), "other.yaml") + " leads to ",
					"which must stay inside " + root,
					"A file beside an entity",
					"keep the file itself there rather than a link to it",
				} {
					if !strings.Contains(err.Error(), want) {
						t.Errorf("the refusal must carry %q:\n%v", want, err)
					}
				}
			case tc.sharedNaming != "":
				if !errors.Is(err, ErrEntitySharesItsDirectory()) {
					t.Fatalf("the neighbour must be read and found in the way; got %v", err)
				}
				if !strings.Contains(err.Error(), tc.sharedNaming) {
					t.Errorf("the refusal must name %q:\n%v", tc.sharedNaming, err)
				}
			default:
				if err != nil {
					t.Fatalf("the layout must be admitted; got %v", err)
				}
			}
		})
	}
}

// subjectRecipe writes the Recipe the directory rule judges into dir and returns
// its path.
func subjectRecipe(t *testing.T, dir string) string {
	t.Helper()
	path := filepath.Join(dir, "recipe.yaml")
	writeFile(t, path, recipeYAML("subject", "", false))
	return path
}

// TestThePinStepHoldsANeighbourToTheRootItsLockIsHeldTo drives the rule through
// Compute, the pin step's plan for writing, so the root the step hands it is the
// one under test: a catalog directory listed outside the workspace is a root of its
// own, so a neighbour linking to a document elsewhere in that directory is read,
// while one linking out of it halts — the root the lock beside the entity is held
// to, and not the workspace's.
func TestThePinStepHoldsANeighbourToTheRootItsLockIsHeldTo(t *testing.T) {
	for _, tc := range []struct {
		name     string
		leadsOut bool
	}{
		{name: "control: a neighbour linking elsewhere inside the listed directory is read"},
		{name: "a neighbour linking out of the listed directory halts", leadsOut: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ws, outside := t.TempDir(), t.TempDir()
			listed := filepath.Join(outside, "listed")
			subject := filepath.Join(listed, "e", "recipe.yaml")
			writeFile(t, subject, recipeYAML("server", "", false))
			target := filepath.Join(listed, "fragments", "compose.yaml")
			if tc.leadsOut {
				target = filepath.Join(outside, "compose.yaml")
			}
			writeFile(t, target, "services:\n  web:\n    image: nginx\n")
			linkInto(t, target, filepath.Join(listed, "e", "other.yaml"))

			_, err := Compute(PlanInputs{
				View:          catalog.NewLazyLookup(ws, "", []string{listed}, nil, listed),
				Entities:      recipes("helm/server:1.0.0"),
				Resolver:      &recordingResolver{},
				WorkspaceRoot: ws,
				ForWriting:    true,
				NamedDirs:     []string{listed},
			})
			if !tc.leadsOut {
				if err != nil {
					t.Fatalf("the neighbour lies inside the listed directory's root and must be read; got %v", err)
				}
				return
			}
			if !errors.Is(err, readscope.ErrReadLeadsOutOfWorkspace()) {
				t.Fatalf("want the read-leads-out-of-workspace halt for a neighbour leading out of the listed directory; got %v", err)
			}
			if !strings.Contains(err.Error(), "which must stay inside "+listed) {
				t.Errorf("the root must be the listed directory, a root of its own:\n%v", err)
			}
		})
	}
}

// TestNamedDocumentRootIsTheRootTheStepHoldsANamedDocumentsLockTo pins the one
// answer a publication asks to the pin step's own: the workspace root for a
// document inside the workspace, and the document's own directory for one named
// from outside it — including a directory reached through a link leading out,
// which is honoured where it was named.
func TestNamedDocumentRootIsTheRootTheStepHoldsANamedDocumentsLockTo(t *testing.T) {
	ws, outside := t.TempDir(), t.TempDir()
	writeFile(t, filepath.Join(ws, "catalog", "e", "recipe.yaml"), "x\n")
	writeFile(t, filepath.Join(outside, "typed", "recipe.yaml"), "x\n")
	linkInto(t, filepath.Join(outside, "typed"), filepath.Join(ws, "linked"))
	for _, tc := range []struct {
		name, entity, want string
	}{
		{name: "a document inside the workspace", entity: filepath.Join(ws, "catalog", "e", "recipe.yaml"), want: ws},
		{name: "a document named outside the workspace", entity: filepath.Join(outside, "typed", "recipe.yaml"), want: filepath.Join(outside, "typed")},
		{name: "a document named through a directory link leading out", entity: filepath.Join(ws, "linked", "recipe.yaml"), want: filepath.Join(ws, "linked")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := NamedDocumentRoot(ws, tc.entity); got != tc.want {
				t.Errorf("NamedDocumentRoot = %q; want %q", got, tc.want)
			}
			abs, _ := filepath.Abs(tc.entity)
			if got := lockRoot(PlanInputs{WorkspaceRoot: ws, NamedDirs: namedDocumentDirs(abs)}, abs); got != tc.want {
				t.Errorf("the step's own lock root = %q; want %q, the answer a publication asks", got, tc.want)
			}
		})
	}
}
