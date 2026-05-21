package controller

import (
	"context"
	"fmt"
	"strconv"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"

	cscv1alpha1 "github.com/waynehoggett/csc/api/v1alpha1"
	"github.com/waynehoggett/csc/internal/naming"
)

var (
	scaledObjectGVK = schema.GroupVersionKind{Group: "keda.sh", Version: "v1alpha1", Kind: "ScaledObject"}
	triggerAuthGVK  = schema.GroupVersionKind{Group: "keda.sh", Version: "v1alpha1", Kind: "TriggerAuthentication"}
)

// reconcileAlwaysOn ensures the worker Deployment and, when KEDA is enabled, the
// ScaledObject + TriggerAuthentication that scale it on queue depth.
func (r *ClaudeEnvironmentReconciler) reconcileAlwaysOn(ctx context.Context, env *cscv1alpha1.ClaudeEnvironment) error {
	dep := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: naming.WorkerDeploymentName(env), Namespace: env.Namespace}}
	if err := r.apply(ctx, env, dep, func() error {
		mutateWorkerDeployment(env, dep, r.KEDAEnabled)
		return nil
	}); err != nil {
		return fmt.Errorf("worker deployment: %w", err)
	}
	env.Status.WorkerDeploymentName = dep.Name

	if r.KEDAEnabled {
		if err := r.reconcileTriggerAuth(ctx, env); err != nil {
			return fmt.Errorf("trigger authentication: %w", err)
		}
		if err := r.reconcileScaledObject(ctx, env); err != nil {
			return fmt.Errorf("scaled object: %w", err)
		}
	}

	// Reflect ready replicas into status (best effort).
	var current appsv1.Deployment
	if err := r.Get(ctx, types.NamespacedName{Namespace: env.Namespace, Name: dep.Name}, &current); err == nil {
		env.Status.WorkersPolling = current.Status.ReadyReplicas
	}
	return nil
}

func mutateWorkerDeployment(env *cscv1alpha1.ClaudeEnvironment, dep *appsv1.Deployment, kedaEnabled bool) {
	labels := naming.ObjectLabels(env, naming.ComponentWorker)
	selector := naming.SelectorLabels(env, naming.ComponentWorker)

	dep.Labels = mergeLabels(dep.Labels, labels)

	// Replicas: set on create always; on update only when KEDA is not managing
	// scale (otherwise we would fight the ScaledObject).
	creating := dep.ResourceVersion == ""
	if creating || !kedaEnabled {
		dep.Spec.Replicas = ptr.To(derefInt32(env.Spec.AlwaysOn.MinReplicas, 0))
	}

	dep.Spec.Selector = &metav1.LabelSelector{MatchLabels: selector}
	dep.Spec.Template = workerPodTemplate(env)
}

// workerPodTemplate builds the worker PodTemplateSpec, overlaying onto an
// optional user-supplied base so sidecars/volumes/scheduling survive.
func workerPodTemplate(env *cscv1alpha1.ClaudeEnvironment) corev1.PodTemplateSpec {
	tmpl := corev1.PodTemplateSpec{}
	if env.Spec.AlwaysOn.PodTemplate != nil {
		tmpl = *env.Spec.AlwaysOn.PodTemplate.DeepCopy()
	}
	tmpl.Labels = mergeLabels(tmpl.Labels, naming.ObjectLabels(env, naming.ComponentWorker))

	// Workers never need a ServiceAccount token; the env key is their only credential.
	tmpl.Spec.AutomountServiceAccountToken = ptr.To(false)

	workdir := env.Spec.AlwaysOn.Workdir
	if workdir == "" {
		workdir = "/workspace"
	}
	worker := corev1.Container{
		Name:       "worker",
		Image:      env.Spec.WorkerImage,
		Command:    []string{"ant"},
		Args:       []string{"beta:worker", "poll", "--workdir", workdir},
		WorkingDir: workdir,
		Env:        dataPlaneEnv(env),
	}
	tmpl.Spec.Containers = upsertContainer(tmpl.Spec.Containers, worker)
	return tmpl
}

func (r *ClaudeEnvironmentReconciler) reconcileTriggerAuth(ctx context.Context, env *cscv1alpha1.ClaudeEnvironment) error {
	ta := &unstructured.Unstructured{}
	ta.SetGroupVersionKind(triggerAuthGVK)
	ta.SetName(naming.TriggerAuthName(env))
	ta.SetNamespace(env.Namespace)
	return r.apply(ctx, env, ta, func() error {
		ta.SetLabels(naming.ObjectLabels(env, naming.ComponentWorker))
		ref := env.Spec.AlwaysOn.Scaling.OrgApiKeySecretRef
		return unstructured.SetNestedSlice(ta.Object, []interface{}{
			map[string]interface{}{
				"parameter": "apiKey",
				"name":      ref.Name,
				"key":       ref.Key,
			},
		}, "spec", "secretTargetRef")
	})
}

func (r *ClaudeEnvironmentReconciler) reconcileScaledObject(ctx context.Context, env *cscv1alpha1.ClaudeEnvironment) error {
	so := &unstructured.Unstructured{}
	so.SetGroupVersionKind(scaledObjectGVK)
	so.SetName(naming.ScaledObjectName(env))
	so.SetNamespace(env.Namespace)

	baseURL := env.Spec.BaseURL
	if baseURL == "" {
		baseURL = naming.DefaultBaseURL
	}
	statsURL := fmt.Sprintf("%s/v1/environments/%s/work/stats", baseURL, env.Spec.EnvironmentID)
	target := strconv.Itoa(int(derefInt32(env.Spec.AlwaysOn.Scaling.TargetQueueDepth, 1)))

	return r.apply(ctx, env, so, func() error {
		so.SetLabels(naming.ObjectLabels(env, naming.ComponentWorker))
		if err := unstructured.SetNestedMap(so.Object, map[string]interface{}{
			"name": naming.WorkerDeploymentName(env),
		}, "spec", "scaleTargetRef"); err != nil {
			return err
		}
		if err := unstructured.SetNestedField(so.Object, int64(derefInt32(env.Spec.AlwaysOn.MinReplicas, 0)), "spec", "minReplicaCount"); err != nil {
			return err
		}
		if err := unstructured.SetNestedField(so.Object, int64(derefInt32(env.Spec.AlwaysOn.MaxReplicas, 10)), "spec", "maxReplicaCount"); err != nil {
			return err
		}
		trigger := map[string]interface{}{
			"type": "metrics-api",
			"metadata": map[string]interface{}{
				"url":           statsURL,
				"valueLocation": "depth",
				"targetValue":   target,
				"method":        "GET",
				// org key supplied via TriggerAuthentication as the x-api-key header.
				"authMode":         "apiKey",
				"authHeaderMethod": "header",
				"keyParamName":     "x-api-key",
			},
			"authenticationRef": map[string]interface{}{
				"name": naming.TriggerAuthName(env),
			},
		}
		return unstructured.SetNestedSlice(so.Object, []interface{}{trigger}, "spec", "triggers")
	})
}
