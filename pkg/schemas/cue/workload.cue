// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Workload — platform-owned kind validated at runtime during Phase B.2
// (parse workload: / workloadFrom: → unify against #Workload).
//
// Mirrors workload-v1alpha1.schema.json in the aggrega-schemas repo. Embedded in the
// aggrega-cli binary via go:embed (see embed.go in this package).
//
// Strictness: every struct is closed. Volumes and probes use disjunction
// for exactly-one-of semantics.

package cue

#Workload: close({
	apiVersion: "aggrega.ai/v1alpha1"
	kind:       "Workload"
	metadata?: close({
		labels?: [string]: string
		annotations?: [string]: string
	})
	replicas?: int & >=0 | *1
	initContainers?: [...#Container]
	containers: [string]: #Container
	volumes?: [string]: #Volume
	services?: [string]: #Service
	scheduling?: close({
		nodeSelector?: [string]: string
		affinity?: {...}
		tolerations?: [...{...}]
		topologySpreadConstraints?: [...{...}]
	})
	podSecurityContext?: close({
		runAsUser?:  int
		runAsGroup?: int
		fsGroup?:    int
		supplementalGroups?: [...int]
	})
	serviceAccount?: string
})

#Container: close({
	image: string
	command?: [...string]
	args?: [...string]
	variables?: [string]: string
	secrets?: [string]:    #ContainerSecret
	ports?: [string]:      int & >=1 & <=65535
	resources?: close({
		requests?: #ResourceList
		limits?:   #ResourceList
	})
	health?: close({
		ready?:   #Probe
		live?:    #Probe
		startup?: #Probe
	})
	volumeMounts?: [...close({
		name:      string
		path:      string
		readonly?: bool | *false
	})]
	restartPolicy?: "Always" | "OnFailure" | "Never"
	lifecycle?: close({
		postStart?: #LifecycleHook
		preStop?:   #LifecycleHook
	})
	securityContext?: close({
		runAsUser?:                int
		runAsGroup?:               int
		runAsNonRoot?:             bool
		readOnlyRootFilesystem?:   bool
		allowPrivilegeEscalation?: bool
		capabilities?: close({
			add?: [...string]
			drop?: [...string]
		})
	})
})

// ContainerSecret: from/as required; when as: file, path is also required.
#ContainerSecret: #ContainerSecretEnv | #ContainerSecretFile

#ContainerSecretEnv: close({
	from: string
	as:   "env"
})

#ContainerSecretFile: close({
	from: string
	as:   "file"
	path: string
})

#ResourceList: close({
	cpu?:    string
	memory?: string
})

// Probe: exactly one of http / tcp / exec. Three closed variants; disjunction.
#Probe: #ProbeHTTP | #ProbeTCP | #ProbeExec

#ProbeHTTP: close({
	http:                 string
	timeoutSeconds?:      int & >=1
	initialDelaySeconds?: int & >=0
	periodSeconds?:       int & >=1
	failureThreshold?:    int & >=1
	successThreshold?:    int & >=1
})

#ProbeTCP: close({
	tcp:                  int & >=1 & <=65535
	timeoutSeconds?:      int & >=1
	initialDelaySeconds?: int & >=0
	periodSeconds?:       int & >=1
	failureThreshold?:    int & >=1
	successThreshold?:    int & >=1
})

#ProbeExec: close({
	exec: [...string]
	timeoutSeconds?:      int & >=1
	initialDelaySeconds?: int & >=0
	periodSeconds?:       int & >=1
	failureThreshold?:    int & >=1
	successThreshold?:    int & >=1
})

// Lifecycle hook: exactly one of exec / httpGet.
#LifecycleHook: #LifecycleExec | #LifecycleHTTPGet

#LifecycleExec: close({
	exec: [...string]
})

#LifecycleHTTPGet: close({
	httpGet: {...}
})

// Volume: exactly one of ephemeral / configmap / secret / persistent / persistentTemplate.
#Volume: #VolEphemeral | #VolConfigMap | #VolSecret | #VolPersistent | #VolPersistentTemplate

#VolEphemeral: close({
	ephemeral: close({
		sizeLimit?: string
	})
})

#VolConfigMap: close({
	configmap: close({
		source: string
		items?: [...{...}]
	})
})

#VolSecret: close({
	secret: close({
		source: string
		items?: [...{...}]
	})
})

#VolPersistent: close({
	persistent: close({
		size:   string
		class?: string
	})
})

#VolPersistentTemplate: close({
	persistentTemplate: close({
		size:   string
		class?: string
	})
})

#Service: close({
	type: "ClusterIP" | "NodePort" | "LoadBalancer" | "Headless"
	ports: [string]: int & >=1 & <=65535
})
