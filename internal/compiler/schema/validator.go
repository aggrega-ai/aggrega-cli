// Package schema validates Aggregate and Release YAML against embedded CUE definitions.
//
// The //go:embed directive cannot use ".." paths, so *.cue files are embedded next to them
// in [github.com/cognitio/aggrega-cli/internal/schema] and loaded from here at runtime.
package schema

import (
	"fmt"
	"strings"
	"sync"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/cuecontext"
	"cuelang.org/go/cue/errors"
	"cuelang.org/go/encoding/yaml"

	cuesources "github.com/cognitio/aggrega-cli/internal/schema"
)

var (
	compileOnce sync.Once
	compiled    cue.Value
	compileErr  error
)

func loadCompiledSchema() (cue.Value, error) {
	compileOnce.Do(func() {
		aggBytes, err1 := cuesources.Files.ReadFile("aggregate.cue")
		relBytes, err2 := cuesources.Files.ReadFile("release.cue")
		if err1 != nil {
			compileErr = fmt.Errorf("schema: read aggregate.cue: %w", err1)
			return
		}
		if err2 != nil {
			compileErr = fmt.Errorf("schema: read release.cue: %w", err2)
			return
		}
		ctx := cuecontext.New()
		vAgg := ctx.CompileString(string(aggBytes), cue.Filename("aggregate.cue"))
		if err := vAgg.Err(); err != nil {
			compileErr = fmt.Errorf("schema: compile aggregate.cue: %w", err)
			return
		}
		vRel := ctx.CompileString(string(relBytes), cue.Filename("release.cue"))
		if err := vRel.Err(); err != nil {
			compileErr = fmt.Errorf("schema: compile release.cue: %w", err)
			return
		}
		merged := vAgg.Unify(vRel)
		if err := merged.Err(); err != nil {
			compileErr = fmt.Errorf("schema: unify CUE schemas: %w", err)
			return
		}
		compiled = merged
	})
	return compiled, compileErr
}

// Validate checks raw YAML against the embedded CUE definition for the given kind
// ("Aggregate" or "Release"). On failure, returns an error with CUE diagnostics.
func Validate(data []byte, kind string) error {
	root, err := loadCompiledSchema()
	if err != nil {
		return err
	}

	var defPath string
	switch strings.TrimSpace(kind) {
	case "Aggregate":
		defPath = "#Aggregate"
	case "Release":
		defPath = "#Release"
	default:
		return fmt.Errorf("schema.Validate: unsupported kind %q (want Aggregate or Release)", kind)
	}

	def := root.LookupPath(cue.ParsePath(defPath))
	if !def.Exists() {
		return fmt.Errorf("schema.Validate: missing CUE definition %s", defPath)
	}

	if err := yaml.Validate(data, def); err != nil {
		details := errors.Details(err, nil)
		if strings.TrimSpace(details) != "" {
			return fmt.Errorf("cue validation failed:\n%s", strings.TrimRight(details, "\n"))
		}
		return fmt.Errorf("cue validation failed: %w", err)
	}
	return nil
}
