package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Mode selects which worker architecture the controller reconciles for a
// ClaudeEnvironment. The two modes mirror Anthropic's self-hosted sandbox docs.
// +kubebuilder:validation:Enum=AlwaysOn;Webhook
type Mode string

const (
	// ModeAlwaysOn runs a long-lived Deployment of polling workers, scaled by KEDA.
	ModeAlwaysOn Mode = "AlwaysOn"
	// ModeWebhook runs a webhook handler that creates one Job per session.
	ModeWebhook Mode = "Webhook"
)

// SecretKeySelector references a single key within a Secret in the same
// namespace as the ClaudeEnvironment.
type SecretKeySelector struct {
	// Name of the Secret.
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`
	// Key within the Secret holding the value.
	// +kubebuilder:validation:MinLength=1
	Key string `json:"key"`
}

// ScalingSpec configures KEDA-based queue-depth scaling for AlwaysOn mode.
type ScalingSpec struct {
	// OrgApiKeySecretRef references the organization API key. This is a
	// control-plane credential used only by KEDA's TriggerAuthentication to
	// poll /work/stats. It is never mounted into worker pods.
	OrgApiKeySecretRef SecretKeySelector `json:"orgApiKeySecretRef"`

	// TargetQueueDepth is the queue depth per worker KEDA scales toward.
	// +kubebuilder:default=1
	// +kubebuilder:validation:Minimum=1
	// +optional
	TargetQueueDepth *int32 `json:"targetQueueDepth,omitempty"`
}

// AlwaysOnSpec configures the long-running worker Deployment.
type AlwaysOnSpec struct {
	// MinReplicas is the floor KEDA scales to (0 enables scale-to-zero).
	// +kubebuilder:default=0
	// +kubebuilder:validation:Minimum=0
	// +optional
	MinReplicas *int32 `json:"minReplicas,omitempty"`

	// MaxReplicas is the ceiling KEDA scales to.
	// +kubebuilder:default=10
	// +kubebuilder:validation:Minimum=1
	// +optional
	MaxReplicas *int32 `json:"maxReplicas,omitempty"`

	// Workdir is the working directory passed to the worker (default /workspace).
	// +kubebuilder:default="/workspace"
	// +optional
	Workdir string `json:"workdir,omitempty"`

	// PodTemplate, if set, is used as the base PodTemplateSpec for worker pods.
	// The controller overlays the worker container, env, and security defaults.
	// +optional
	PodTemplate *corev1.PodTemplateSpec `json:"podTemplate,omitempty"`

	// Scaling configures KEDA queue-depth scaling.
	Scaling ScalingSpec `json:"scaling"`
}

// IngressSpec configures an optional Ingress for the webhook handler.
type IngressSpec struct {
	// Enabled toggles creation of the Ingress.
	// +optional
	Enabled bool `json:"enabled,omitempty"`

	// Host is the Ingress host (e.g. claude-webhook.example.com).
	// +optional
	Host string `json:"host,omitempty"`

	// IngressClassName names the IngressClass to use.
	// +optional
	IngressClassName *string `json:"ingressClassName,omitempty"`

	// Annotations are merged onto the generated Ingress.
	// +optional
	Annotations map[string]string `json:"annotations,omitempty"`
}

// WebhookSpec configures the webhook handler and per-session Jobs.
type WebhookSpec struct {
	// SigningKeySecretRef references the webhook signing key used to verify
	// inbound event signatures. Mounted only into the handler.
	SigningKeySecretRef SecretKeySelector `json:"signingKeySecretRef"`

	// HandlerReplicas is the number of webhook handler replicas.
	// +kubebuilder:default=1
	// +kubebuilder:validation:Minimum=1
	// +optional
	HandlerReplicas *int32 `json:"handlerReplicas,omitempty"`

	// HandlerImage overrides the webhook handler image. Defaults to the
	// controller's configured default handler image.
	// +optional
	HandlerImage string `json:"handlerImage,omitempty"`

	// Ingress configures the optional Ingress.
	// +optional
	Ingress *IngressSpec `json:"ingress,omitempty"`

	// JobTemplate, if set, is the base PodTemplateSpec for per-session Jobs.
	// +optional
	JobTemplate *corev1.PodTemplateSpec `json:"jobTemplate,omitempty"`

	// TTLSecondsAfterFinished sets ttlSecondsAfterFinished on session Jobs.
	// +kubebuilder:default=600
	// +kubebuilder:validation:Minimum=0
	// +optional
	TTLSecondsAfterFinished *int32 `json:"ttlSecondsAfterFinished,omitempty"`
}

// ClaudeEnvironmentSpec defines the desired state of a ClaudeEnvironment.
type ClaudeEnvironmentSpec struct {
	// EnvironmentID is the Anthropic environment id (e.g. env_abc123).
	// +kubebuilder:validation:MinLength=1
	EnvironmentID string `json:"environmentId"`

	// EnvironmentKeySecretRef references the environment key (data-plane
	// credential) mounted into workers and session Jobs.
	EnvironmentKeySecretRef SecretKeySelector `json:"environmentKeySecretRef"`

	// Mode selects AlwaysOn or Webhook.
	Mode Mode `json:"mode"`

	// WorkerImage is the image used for workers (AlwaysOn) and session Jobs
	// (Webhook). Must contain /bin/bash.
	// +kubebuilder:validation:MinLength=1
	WorkerImage string `json:"workerImage"`

	// BaseURL overrides the Anthropic API base URL.
	// +kubebuilder:default="https://api.anthropic.com"
	// +optional
	BaseURL string `json:"baseURL,omitempty"`

	// AlwaysOn holds AlwaysOn-mode configuration. Required when mode=AlwaysOn.
	// +optional
	AlwaysOn *AlwaysOnSpec `json:"alwaysOn,omitempty"`

	// Webhook holds Webhook-mode configuration. Required when mode=Webhook.
	// +optional
	Webhook *WebhookSpec `json:"webhook,omitempty"`
}

// ClaudeEnvironmentStatus reports the observed state of a ClaudeEnvironment.
type ClaudeEnvironmentStatus struct {
	// Conditions represent the latest observations of the environment's state.
	// +optional
	// +patchMergeKey=type
	// +patchStrategy=merge
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type"`

	// ObservedGeneration is the generation last reconciled by the controller.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// WorkerDeploymentName is the name of the worker Deployment (AlwaysOn).
	// +optional
	WorkerDeploymentName string `json:"workerDeploymentName,omitempty"`

	// WebhookServiceName is the name of the webhook handler Service (Webhook).
	// +optional
	WebhookServiceName string `json:"webhookServiceName,omitempty"`

	// ObservedQueueDepth is the most recently observed work queue depth.
	// +optional
	ObservedQueueDepth int32 `json:"observedQueueDepth,omitempty"`

	// WorkersPolling is the number of ready worker replicas (AlwaysOn).
	// +optional
	WorkersPolling int32 `json:"workersPolling,omitempty"`

	// LastWebhookReceivedAt records the last time a webhook event was handled.
	// +optional
	LastWebhookReceivedAt *metav1.Time `json:"lastWebhookReceivedAt,omitempty"`
}

// Condition types reported in status.
const (
	// ConditionReady is true when the mode-specific resources are reconciled.
	ConditionReady = "Ready"
	// ConditionDegraded is true when reconciliation failed.
	ConditionDegraded = "Degraded"
)

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=cenv;cenvs
// +kubebuilder:printcolumn:name="Mode",type=string,JSONPath=`.spec.mode`
// +kubebuilder:printcolumn:name="Environment",type=string,JSONPath=`.spec.environmentId`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="QueueDepth",type=integer,JSONPath=`.status.observedQueueDepth`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// ClaudeEnvironment is the Schema for the claudeenvironments API.
type ClaudeEnvironment struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ClaudeEnvironmentSpec   `json:"spec,omitempty"`
	Status ClaudeEnvironmentStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// ClaudeEnvironmentList contains a list of ClaudeEnvironment.
type ClaudeEnvironmentList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ClaudeEnvironment `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ClaudeEnvironment{}, &ClaudeEnvironmentList{})
}
