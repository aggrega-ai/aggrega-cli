package plugins

import "testing"

func TestDataMaskingPlugin_Apply(t *testing.T) {
	var values map[string]interface{}
	if err := (DataMaskingPlugin{}).Apply(&values); err != nil {
		t.Fatal(err)
	}
	if values == nil || values["_aggrega_injected_env_DATA_MASKING_MODE"] != "strict" {
		t.Fatalf("Values: %#v", values)
	}
}
