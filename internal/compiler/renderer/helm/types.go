// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package helm

// Chart is the Helm Chart.yaml document (apiVersion v2).
type Chart struct {
	APIVersion string `yaml:"apiVersion"`
	Name       string `yaml:"name"`
	Version    string `yaml:"version"`
}

// Deployment is a minimal apps/v1 Deployment for YAML marshaling.
type Deployment struct {
	APIVersion string         `yaml:"apiVersion"`
	Kind       string         `yaml:"kind"`
	Metadata   ObjectMeta     `yaml:"metadata"`
	Spec       DeploymentSpec `yaml:"spec"`
}

// StatefulSet is a minimal apps/v1 StatefulSet for YAML marshaling. Used when
// any Workload volume declares persistentTemplate (chapter 16 §3.5).
type StatefulSet struct {
	APIVersion string          `yaml:"apiVersion"`
	Kind       string          `yaml:"kind"`
	Metadata   ObjectMeta      `yaml:"metadata"`
	Spec       StatefulSetSpec `yaml:"spec"`
}

// DaemonSet is a minimal apps/v1 DaemonSet for YAML marshaling. Emitted for a
// Workload of form: daemon — one pod per eligible node, so it carries no
// replica count.
type DaemonSet struct {
	APIVersion string        `yaml:"apiVersion"`
	Kind       string        `yaml:"kind"`
	Metadata   ObjectMeta    `yaml:"metadata"`
	Spec       DaemonSetSpec `yaml:"spec"`
}

// DaemonSetSpec is a reduced DaemonSet spec. A DaemonSet has no replica count;
// the scheduler places one pod per eligible node.
type DaemonSetSpec struct {
	Selector *LabelSelector  `yaml:"selector"`
	Template PodTemplateSpec `yaml:"template"`
}

// Job is a minimal batch/v1 Job for YAML marshaling. Emitted for a Workload of
// form: task — a run-to-completion workload with no replica count and no
// governing Service.
type Job struct {
	APIVersion string     `yaml:"apiVersion"`
	Kind       string     `yaml:"kind"`
	Metadata   ObjectMeta `yaml:"metadata"`
	Spec       JobSpec    `yaml:"spec"`
}

// JobSpec is a reduced Job spec. The pod template's restartPolicy must be
// Never or OnFailure (Always is invalid for a Job pod).
type JobSpec struct {
	Template PodTemplateSpec `yaml:"template"`
}

// CronJob is a minimal batch/v1 CronJob for YAML marshaling. Emitted for a
// Workload of form: scheduled — a recurring, time-triggered workload whose
// spec.schedule carries the Workload's cron schedule verbatim.
type CronJob struct {
	APIVersion string      `yaml:"apiVersion"`
	Kind       string      `yaml:"kind"`
	Metadata   ObjectMeta  `yaml:"metadata"`
	Spec       CronJobSpec `yaml:"spec"`
}

// CronJobSpec is a reduced CronJob spec. Schedule is the cron expression the
// scheduler fires on; JobTemplate wraps the per-run Job.
type CronJobSpec struct {
	Schedule    string          `yaml:"schedule"`
	JobTemplate JobTemplateSpec `yaml:"jobTemplate"`
}

// JobTemplateSpec is the CronJob's per-run Job template. It reuses JobSpec so a
// scheduled run has the same run-to-completion pod shape as a form: task Job.
type JobTemplateSpec struct {
	Spec JobSpec `yaml:"spec"`
}

// PersistentVolumeClaim is a minimal v1 PVC for YAML marshaling. Emitted as
// a separate resource for each `persistent` volume in a Workload.
type PersistentVolumeClaim struct {
	APIVersion string                    `yaml:"apiVersion"`
	Kind       string                    `yaml:"kind"`
	Metadata   ObjectMeta                `yaml:"metadata"`
	Spec       PersistentVolumeClaimSpec `yaml:"spec"`
}

// Service is a minimal v1 Service for YAML marshaling.
type Service struct {
	APIVersion string      `yaml:"apiVersion"`
	Kind       string      `yaml:"kind"`
	Metadata   ObjectMeta  `yaml:"metadata"`
	Spec       ServiceSpec `yaml:"spec"`
}

// ExternalNameService is a v1 Service of type ExternalName — the render shape
// for a form: reference workload. It CNAMEs an in-cluster name to an external
// host and carries no selector or clusterIP.
type ExternalNameService struct {
	APIVersion string                  `yaml:"apiVersion"`
	Kind       string                  `yaml:"kind"`
	Metadata   ObjectMeta              `yaml:"metadata"`
	Spec       ExternalNameServiceSpec `yaml:"spec"`
}

// ExternalNameServiceSpec is the reduced spec for an ExternalName Service.
type ExternalNameServiceSpec struct {
	Type         string             `yaml:"type"`
	ExternalName string             `yaml:"externalName"`
	Ports        []ExternalNamePort `yaml:"ports,omitempty"`
}

// ExternalNamePort is a single port on an ExternalName Service.
type ExternalNamePort struct {
	Port int32 `yaml:"port"`
}

// ObjectMeta carries K8s resource metadata.
type ObjectMeta struct {
	Name        string            `yaml:"name"`
	Namespace   string            `yaml:"namespace,omitempty"`
	Labels      map[string]string `yaml:"labels,omitempty"`
	Annotations map[string]string `yaml:"annotations,omitempty"`
}

// DeploymentSpec is a reduced Deployment spec.
type DeploymentSpec struct {
	Replicas *int32          `yaml:"replicas"`
	Selector *LabelSelector  `yaml:"selector"`
	Template PodTemplateSpec `yaml:"template"`
}

// StatefulSetSpec is a reduced StatefulSet spec. ServiceName references the
// headless governing Service; volumeClaimTemplates expands per replica.
type StatefulSetSpec struct {
	Replicas             *int32                  `yaml:"replicas"`
	Selector             *LabelSelector          `yaml:"selector"`
	ServiceName          string                  `yaml:"serviceName,omitempty"`
	Template             PodTemplateSpec         `yaml:"template"`
	VolumeClaimTemplates []PersistentVolumeClaim `yaml:"volumeClaimTemplates,omitempty"`
}

// PersistentVolumeClaimSpec is a reduced PVC spec.
type PersistentVolumeClaimSpec struct {
	AccessModes      []string             `yaml:"accessModes,omitempty"`
	Resources        ResourceRequirements `yaml:"resources"`
	StorageClassName *string              `yaml:"storageClassName,omitempty"`
}

// LabelSelector with matchLabels only.
type LabelSelector struct {
	MatchLabels map[string]string `yaml:"matchLabels"`
}

// PodTemplateSpec wraps pod metadata and spec.
type PodTemplateSpec struct {
	Metadata ObjectMeta `yaml:"metadata"`
	Spec     PodSpec    `yaml:"spec"`
}

// PodSpec holds containers and pod-level configuration.
//
// Fields beyond Containers are zero-valued under sourceless.go's renders
// (which constructs only Containers); their omitempty tags suppress them
// from the YAML output. The Workload synthesizer populates the additional
// fields per the parsed Workload's pod-level shape.
type PodSpec struct {
	Containers                    []Container            `yaml:"containers"`
	InitContainers                []Container            `yaml:"initContainers,omitempty"`
	Volumes                       []Volume               `yaml:"volumes,omitempty"`
	NodeSelector                  map[string]string      `yaml:"nodeSelector,omitempty"`
	Affinity                      any                    `yaml:"affinity,omitempty"`
	Tolerations                   []any                  `yaml:"tolerations,omitempty"`
	TopologySpreadConstraints     []any                  `yaml:"topologySpreadConstraints,omitempty"`
	SecurityContext               *PodSecurityContext    `yaml:"securityContext,omitempty"`
	ServiceAccountName            string                 `yaml:"serviceAccountName,omitempty"`
	ImagePullSecrets              []LocalObjectReference `yaml:"imagePullSecrets,omitempty"`
	PriorityClassName             string                 `yaml:"priorityClassName,omitempty"`
	TerminationGracePeriodSeconds *int64                 `yaml:"terminationGracePeriodSeconds,omitempty"`
	DNSPolicy                     string                 `yaml:"dnsPolicy,omitempty"`
	DNSConfig                     *PodDNSConfig          `yaml:"dnsConfig,omitempty"`
	HostAliases                   []HostAlias            `yaml:"hostAliases,omitempty"`
	RestartPolicy                 string                 `yaml:"restartPolicy,omitempty"`
}

// PodDNSConfig mirrors the K8s pod dnsConfig shape (nameservers, searches,
// options).
type PodDNSConfig struct {
	Nameservers []string `yaml:"nameservers,omitempty"`
	Searches    []string `yaml:"searches,omitempty"`
	Options     []any    `yaml:"options,omitempty"`
}

// HostAlias is a static hostname-to-IP mapping in the pod spec.
type HostAlias struct {
	IP        string   `yaml:"ip,omitempty"`
	Hostnames []string `yaml:"hostnames,omitempty"`
}

// Container is one workload container. Sourceless.go uses only
// Name/Image/Ports/Env; other fields are populated by the Workload
// synthesizer when the parsed Workload declares them.
type Container struct {
	Name            string                    `yaml:"name"`
	Image           string                    `yaml:"image"`
	ImagePullPolicy string                    `yaml:"imagePullPolicy,omitempty"`
	Command         []string                  `yaml:"command,omitempty"`
	Args            []string                  `yaml:"args,omitempty"`
	Ports           []ContainerPort           `yaml:"ports,omitempty"`
	Env             []EnvVar                  `yaml:"env,omitempty"`
	EnvFrom         []EnvFromSource           `yaml:"envFrom,omitempty"`
	Resources       *ResourceRequirements     `yaml:"resources,omitempty"`
	VolumeMounts    []VolumeMount             `yaml:"volumeMounts,omitempty"`
	LivenessProbe   *Probe                    `yaml:"livenessProbe,omitempty"`
	ReadinessProbe  *Probe                    `yaml:"readinessProbe,omitempty"`
	StartupProbe    *Probe                    `yaml:"startupProbe,omitempty"`
	Lifecycle       *Lifecycle                `yaml:"lifecycle,omitempty"`
	SecurityContext *ContainerSecurityContext `yaml:"securityContext,omitempty"`
}

// ContainerPort exposes a port on the container.
type ContainerPort struct {
	Name          string `yaml:"name,omitempty"`
	ContainerPort int32  `yaml:"containerPort"`
	Protocol      string `yaml:"protocol,omitempty"`
}

// EnvVar is a key/value environment variable. ValueFrom references a
// secret or configmap key for delivery via env.
type EnvVar struct {
	Name      string        `yaml:"name"`
	Value     string        `yaml:"value,omitempty"`
	ValueFrom *EnvVarSource `yaml:"valueFrom,omitempty"`
}

// EnvVarSource is a discriminated reference for an env var's source.
// Used when a container secrets entry uses `as: env`.
type EnvVarSource struct {
	SecretKeyRef *SecretKeySelector `yaml:"secretKeyRef,omitempty"`
}

// SecretKeySelector references a secret key by name.
type SecretKeySelector struct {
	Name string `yaml:"name"`
	Key  string `yaml:"key"`
}

// EnvFromSource references an entire ConfigMap or Secret as bulk env, with an
// optional per-source Prefix prepended to each imported variable name. Exactly
// one of ConfigMapRef / SecretRef is set.
type EnvFromSource struct {
	ConfigMapRef *LocalObjectReference `yaml:"configMapRef,omitempty"`
	SecretRef    *LocalObjectReference `yaml:"secretRef,omitempty"`
	Prefix       string                `yaml:"prefix,omitempty"`
}

// LocalObjectReference is a name-only reference to an in-namespace object.
type LocalObjectReference struct {
	Name string `yaml:"name"`
}

// ResourceRequirements expresses container resource requests and limits,
// or PVC size requests.
type ResourceRequirements struct {
	Requests map[string]string `yaml:"requests,omitempty"`
	Limits   map[string]string `yaml:"limits,omitempty"`
}

// VolumeMount mounts a Volume into a container at Path.
type VolumeMount struct {
	Name      string `yaml:"name"`
	MountPath string `yaml:"mountPath"`
	SubPath   string `yaml:"subPath,omitempty"`
	ReadOnly  bool   `yaml:"readOnly,omitempty"`
}

// Volume is a discriminated volume declaration on the pod spec. Aggrega's
// Workload volume variants map to one of: EmptyDir / ConfigMap / Secret /
// PersistentVolumeClaim. The persistentTemplate variant becomes a
// VolumeClaimTemplate on the StatefulSet, not a pod-spec Volume.
type Volume struct {
	Name                  string                             `yaml:"name"`
	EmptyDir              *EmptyDirVolumeSource              `yaml:"emptyDir,omitempty"`
	ConfigMap             *ConfigMapVolumeSource             `yaml:"configMap,omitempty"`
	Secret                *SecretVolumeSource                `yaml:"secret,omitempty"`
	PersistentVolumeClaim *PersistentVolumeClaimVolumeSource `yaml:"persistentVolumeClaim,omitempty"`
}

// EmptyDirVolumeSource — corresponds to Workload's ephemeral volume variant.
type EmptyDirVolumeSource struct {
	SizeLimit string `yaml:"sizeLimit,omitempty"`
}

// ConfigMapVolumeSource — corresponds to Workload's configmap volume variant.
// Source is the name of an existing ConfigMap (not emitted by the synthesizer).
type ConfigMapVolumeSource struct {
	Name string `yaml:"name"`
}

// SecretVolumeSource — corresponds to Workload's secret volume variant.
// SecretName is the name of an existing Secret (not emitted by the synthesizer).
type SecretVolumeSource struct {
	SecretName string `yaml:"secretName"`
}

// PersistentVolumeClaimVolumeSource — references an emitted PVC by name
// (corresponds to Workload's persistent volume variant; persistentTemplate
// produces a volumeClaimTemplate inside the StatefulSet, not a pod-spec
// volume).
type PersistentVolumeClaimVolumeSource struct {
	ClaimName string `yaml:"claimName"`
	ReadOnly  bool   `yaml:"readOnly,omitempty"`
}

// Probe is a discriminated K8s probe. Exactly one of HTTPGet / TCPSocket /
// Exec is set, mirroring Aggrega's Workload probe-intent disjunction.
type Probe struct {
	HTTPGet             *HTTPGetAction   `yaml:"httpGet,omitempty"`
	TCPSocket           *TCPSocketAction `yaml:"tcpSocket,omitempty"`
	Exec                *ExecAction      `yaml:"exec,omitempty"`
	TimeoutSeconds      int32            `yaml:"timeoutSeconds,omitempty"`
	InitialDelaySeconds int32            `yaml:"initialDelaySeconds,omitempty"`
	PeriodSeconds       int32            `yaml:"periodSeconds,omitempty"`
	FailureThreshold    int32            `yaml:"failureThreshold,omitempty"`
	SuccessThreshold    int32            `yaml:"successThreshold,omitempty"`
}

// HTTPGetAction describes an HTTP-based probe.
type HTTPGetAction struct {
	Path string `yaml:"path"`
	Port string `yaml:"port"` // named container port
}

// TCPSocketAction describes a TCP-based probe.
type TCPSocketAction struct {
	Port int32 `yaml:"port"`
}

// ExecAction describes a command-based probe.
type ExecAction struct {
	Command []string `yaml:"command"`
}

// Lifecycle holds pre-stop and post-start hooks.
type Lifecycle struct {
	PostStart *LifecycleHandler `yaml:"postStart,omitempty"`
	PreStop   *LifecycleHandler `yaml:"preStop,omitempty"`
}

// LifecycleHandler is a discriminated lifecycle hook (exec or httpGet).
// httpGet is opaque pass-through (`map[string]any`) per Aggrega's K8s-shape
// pass-through stance.
type LifecycleHandler struct {
	Exec    *ExecAction    `yaml:"exec,omitempty"`
	HTTPGet map[string]any `yaml:"httpGet,omitempty"`
}

// PodSecurityContext is the pod-level security identity.
type PodSecurityContext struct {
	RunAsUser          *int64  `yaml:"runAsUser,omitempty"`
	RunAsGroup         *int64  `yaml:"runAsGroup,omitempty"`
	FSGroup            *int64  `yaml:"fsGroup,omitempty"`
	SupplementalGroups []int64 `yaml:"supplementalGroups,omitempty"`
}

// ContainerSecurityContext is the container-level security identity.
type ContainerSecurityContext struct {
	RunAsUser                *int64        `yaml:"runAsUser,omitempty"`
	RunAsGroup               *int64        `yaml:"runAsGroup,omitempty"`
	RunAsNonRoot             *bool         `yaml:"runAsNonRoot,omitempty"`
	ReadOnlyRootFilesystem   *bool         `yaml:"readOnlyRootFilesystem,omitempty"`
	AllowPrivilegeEscalation *bool         `yaml:"allowPrivilegeEscalation,omitempty"`
	Capabilities             *Capabilities `yaml:"capabilities,omitempty"`
}

// Capabilities — Linux capability add/drop lists.
type Capabilities struct {
	Add  []string `yaml:"add,omitempty"`
	Drop []string `yaml:"drop,omitempty"`
}

// ServiceSpec is a reduced Service spec.
type ServiceSpec struct {
	Type      string            `yaml:"type"`
	ClusterIP string            `yaml:"clusterIP,omitempty"` // "None" for Headless
	Selector  map[string]string `yaml:"selector"`
	Ports     []ServicePort     `yaml:"ports"`
}

// ServicePort maps Service port to target port (named when matching a
// container port name; numeric when matching by number).
type ServicePort struct {
	Name       string `yaml:"name,omitempty"`
	Port       int32  `yaml:"port"`
	TargetPort string `yaml:"targetPort"` // string form supports both numeric and named
	Protocol   string `yaml:"protocol,omitempty"`
}
