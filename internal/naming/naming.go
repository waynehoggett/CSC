// Package naming centralizes label keys, object-name derivation, and the
// Anthropic API constants that must stay consistent across the controller,
// worker pods, and the webhook handler.
package naming

import cscv1alpha1 "github.com/waynehoggett/csc/api/v1alpha1"

// Label keys applied to all controller-managed objects.
const (
	LabelEnvironment = "csc.io/environment"
	LabelComponent   = "csc.io/component"
	LabelManagedBy   = "app.kubernetes.io/managed-by"

	ManagedByValue = "claude-sandbox-controller"
)

// Component label values.
const (
	ComponentWorker         = "worker"
	ComponentWebhookHandler = "webhook-handler"
	ComponentSession        = "session"
)

// Anthropic API constants. AnthropicBetaHeader is defined in exactly one place
// so it can be rolled forward deliberately (see SPEC.md §15).
const (
	AnthropicVersionHeader = "2023-06-01"
	AnthropicBetaHeader    = "managed-agents-2026-04-01"
	DefaultBaseURL         = "https://api.anthropic.com"
)

// Environment variable names injected into workers and session Jobs.
const (
	EnvEnvironmentKey = "ANTHROPIC_ENVIRONMENT_KEY"
	EnvEnvironmentID  = "ANTHROPIC_ENVIRONMENT_ID"
	EnvBaseURL        = "ANTHROPIC_BASE_URL"
	EnvSessionID      = "ANTHROPIC_SESSION_ID"
	EnvWorkID         = "ANTHROPIC_WORK_ID"
	// EnvOrgAPIKey is a control-plane credential. It is intentionally never set
	// on worker or session pods.
	EnvOrgAPIKey = "ANTHROPIC_API_KEY"
)

// WorkerDeploymentName returns the worker Deployment name for an environment.
func WorkerDeploymentName(env *cscv1alpha1.ClaudeEnvironment) string {
	return env.Name + "-worker"
}

// ScaledObjectName returns the KEDA ScaledObject name.
func ScaledObjectName(env *cscv1alpha1.ClaudeEnvironment) string {
	return env.Name + "-worker"
}

// TriggerAuthName returns the KEDA TriggerAuthentication name.
func TriggerAuthName(env *cscv1alpha1.ClaudeEnvironment) string {
	return env.Name + "-keda-auth"
}

// WebhookDeploymentName returns the webhook handler Deployment name.
func WebhookDeploymentName(env *cscv1alpha1.ClaudeEnvironment) string {
	return env.Name + "-webhook"
}

// WebhookServiceName returns the webhook handler Service name.
func WebhookServiceName(env *cscv1alpha1.ClaudeEnvironment) string {
	return env.Name + "-webhook"
}

// WebhookIngressName returns the webhook Ingress name.
func WebhookIngressName(env *cscv1alpha1.ClaudeEnvironment) string {
	return env.Name + "-webhook"
}

// SelectorLabels returns the label set identifying objects for one environment
// and component. Used both as pod selectors and object labels.
func SelectorLabels(env *cscv1alpha1.ClaudeEnvironment, component string) map[string]string {
	return map[string]string{
		LabelEnvironment: env.Name,
		LabelComponent:   component,
	}
}

// ObjectLabels returns SelectorLabels plus the managed-by marker.
func ObjectLabels(env *cscv1alpha1.ClaudeEnvironment, component string) map[string]string {
	l := SelectorLabels(env, component)
	l[LabelManagedBy] = ManagedByValue
	return l
}
