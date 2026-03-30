package aggrega

#ComponentType:
	// --- AI-Native Types ---
	"ai-model" |
	"ai-agent" |
	"ai-tool" |
	"ai-pipeline" |
	"ai-gateway" |
	"ai-datastore" |
	// --- Cloud-Native Types ---
	"server" |
	"worker" |
	"task" |
	"function" |
	"stateful-server" |
	"resource" |
	"messaging"

#Component: {
	type: *"server" | #ComponentType
	// Domain 1: developer intent only (opaque values, features, secrets). No infra bindings.
	...
}

#Aggregate: {
	apiVersion: "aggrega.ai/v1alpha1"
	kind:       "Aggregate"
	metadata: {
		name: string
	}
	spec: {
		components: {[string]: #Component}
		// e.g. spec.inputs and other v1alpha1 fields validated by Go after CUE.
		...
	}
}
