/*
 * ARCHITECTURAL NOTICE (MILESTONE 1 SHORTCUT)
 * In a mature OAM Aggrega deployment, these plugins will be dynamic WebAssembly (Wasm)
 * binaries that generate specific sidecar configurations (e.g., EnvoyFilters, KongPlugins)
 * based on the target ComponentRecipe.
 * Because we currently lack the ComponentRecipe engine, these hardcoded plugins take a
 * "dumb" shortcut: they simply inject internal flags into the component's opaque Values map.
 * The render engines will later translate these into standard environment variables, assuming
 * the fallback container images are configured to read them.
 */

package plugins

const (
	injectedPromptGuardKey = "_aggrega_injected_env_PROMPT_GUARD_ENABLED"
	injectedDataMaskingKey = "_aggrega_injected_env_DATA_MASKING_MODE"
)

// PromptGuardPlugin is an MVP feature plugin (see file comment).
type PromptGuardPlugin struct{}

func (PromptGuardPlugin) Name() string { return "prompt_guard" }

func (PromptGuardPlugin) Apply(values *map[string]interface{}) error {
	if *values == nil {
		*values = make(map[string]interface{})
	}
	(*values)[injectedPromptGuardKey] = "true"
	return nil
}

// DataMaskingPlugin is an MVP feature plugin (see file comment).
type DataMaskingPlugin struct{}

func (DataMaskingPlugin) Name() string { return "data_masking" }

func (DataMaskingPlugin) Apply(values *map[string]interface{}) error {
	if *values == nil {
		*values = make(map[string]interface{})
	}
	(*values)[injectedDataMaskingKey] = "strict"
	return nil
}

// InjectedEnvPrefix is the values_map key prefix for compiler-injected container environment.
const InjectedEnvPrefix = "_aggrega_injected_env_"
