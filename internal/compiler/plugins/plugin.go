package plugins

// Plugin is the future Wasm-backed security / policy extension point. MVP implementations
// mutate opaque component values only (see hardcoded.go).
//
// Apply receives a pointer to the component's Values map so it can allocate the map when nil.
// The signature intentionally avoids importing package builder to prevent an import cycle
// (builder wires the registry during IR construction).
type Plugin interface {
	Name() string
	Apply(values *map[string]interface{}) error
}

// Registry maps feature identifiers (lowercase) to plugins executed during IR build.
var Registry = map[string]Plugin{
	"prompt_guard": PromptGuardPlugin{},
	"data_masking": DataMaskingPlugin{},
}
