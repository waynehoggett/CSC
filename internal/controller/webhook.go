package controller

import (
	"context"
	"encoding/json"
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"

	cscv1alpha1 "github.com/waynehoggett/csc/api/v1alpha1"
	"github.com/waynehoggett/csc/internal/naming"
)

const (
	webhookContainerPort = 8080
	jobTemplateMountPath = "/etc/csc/jobtemplate"
	jobTemplateFileName  = "podTemplate.json"
)

func webhookJobTemplateConfigMapName(env *cscv1alpha1.ClaudeEnvironment) string {
	return env.Name + "-webhook-jobtemplate"
}

// reconcileWebhook ensures the webhook handler's RBAC, Deployment, Service, and
// optional Ingress.
func (r *ClaudeEnvironmentReconciler) reconcileWebhook(ctx context.Context, env *cscv1alpha1.ClaudeEnvironment) error {
	if err := r.reconcileWebhookRBAC(ctx, env); err != nil {
		return fmt.Errorf("webhook rbac: %w", err)
	}
	if err := r.reconcileWebhookJobTemplate(ctx, env); err != nil {
		return fmt.Errorf("webhook job template: %w", err)
	}
	if err := r.reconcileWebhookDeployment(ctx, env); err != nil {
		return fmt.Errorf("webhook deployment: %w", err)
	}
	if err := r.reconcileWebhookService(ctx, env); err != nil {
		return fmt.Errorf("webhook service: %w", err)
	}
	if env.Spec.Webhook.Ingress != nil && env.Spec.Webhook.Ingress.Enabled {
		if err := r.reconcileWebhookIngress(ctx, env); err != nil {
			return fmt.Errorf("webhook ingress: %w", err)
		}
	}
	env.Status.WebhookServiceName = naming.WebhookServiceName(env)
	return nil
}

// reconcileWebhookRBAC grants the handler exactly the access it needs: create
// and manage session Jobs in this namespace, and read the env-key Secret.
func (r *ClaudeEnvironmentReconciler) reconcileWebhookRBAC(ctx context.Context, env *cscv1alpha1.ClaudeEnvironment) error {
	saName := naming.WebhookDeploymentName(env)

	sa := &corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{Name: saName, Namespace: env.Namespace}}
	if err := r.apply(ctx, env, sa, func() error {
		sa.Labels = mergeLabels(sa.Labels, naming.ObjectLabels(env, naming.ComponentWebhookHandler))
		return nil
	}); err != nil {
		return err
	}

	role := &rbacv1.Role{ObjectMeta: metav1.ObjectMeta{Name: saName, Namespace: env.Namespace}}
	if err := r.apply(ctx, env, role, func() error {
		role.Labels = mergeLabels(role.Labels, naming.ObjectLabels(env, naming.ComponentWebhookHandler))
		role.Rules = []rbacv1.PolicyRule{
			{
				APIGroups: []string{"batch"},
				Resources: []string{"jobs"},
				Verbs:     []string{"create", "get", "list", "watch", "delete"},
			},
			{
				APIGroups:     []string{""},
				Resources:     []string{"secrets"},
				Verbs:         []string{"get"},
				ResourceNames: []string{env.Spec.EnvironmentKeySecretRef.Name},
			},
		}
		return nil
	}); err != nil {
		return err
	}

	rb := &rbacv1.RoleBinding{ObjectMeta: metav1.ObjectMeta{Name: saName, Namespace: env.Namespace}}
	return r.apply(ctx, env, rb, func() error {
		rb.Labels = mergeLabels(rb.Labels, naming.ObjectLabels(env, naming.ComponentWebhookHandler))
		rb.RoleRef = rbacv1.RoleRef{APIGroup: "rbac.authorization.k8s.io", Kind: "Role", Name: saName}
		rb.Subjects = []rbacv1.Subject{{Kind: "ServiceAccount", Name: saName, Namespace: env.Namespace}}
		return nil
	})
}

// reconcileWebhookJobTemplate serializes the operator-supplied jobTemplate into
// a ConfigMap the handler mounts, bridging the CRD field to the handler process.
func (r *ClaudeEnvironmentReconciler) reconcileWebhookJobTemplate(ctx context.Context, env *cscv1alpha1.ClaudeEnvironment) error {
	payload := []byte("{}")
	if env.Spec.Webhook.JobTemplate != nil {
		b, err := json.Marshal(env.Spec.Webhook.JobTemplate)
		if err != nil {
			return fmt.Errorf("marshal job template: %w", err)
		}
		payload = b
	}
	cm := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: webhookJobTemplateConfigMapName(env), Namespace: env.Namespace}}
	return r.apply(ctx, env, cm, func() error {
		cm.Labels = mergeLabels(cm.Labels, naming.ObjectLabels(env, naming.ComponentWebhookHandler))
		cm.Data = map[string]string{jobTemplateFileName: string(payload)}
		return nil
	})
}

func (r *ClaudeEnvironmentReconciler) reconcileWebhookDeployment(ctx context.Context, env *cscv1alpha1.ClaudeEnvironment) error {
	dep := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: naming.WebhookDeploymentName(env), Namespace: env.Namespace}}
	return r.apply(ctx, env, dep, func() error {
		labels := naming.ObjectLabels(env, naming.ComponentWebhookHandler)
		selector := naming.SelectorLabels(env, naming.ComponentWebhookHandler)
		dep.Labels = mergeLabels(dep.Labels, labels)
		dep.Spec.Replicas = ptr.To(derefInt32(env.Spec.Webhook.HandlerReplicas, 1))
		dep.Spec.Selector = &metav1.LabelSelector{MatchLabels: selector}
		dep.Spec.Template = r.webhookPodTemplate(env)
		return nil
	})
}

func (r *ClaudeEnvironmentReconciler) webhookPodTemplate(env *cscv1alpha1.ClaudeEnvironment) corev1.PodTemplateSpec {
	image := env.Spec.Webhook.HandlerImage
	if image == "" {
		image = r.DefaultWebhookHandlerImage
	}
	baseURL := env.Spec.BaseURL
	if baseURL == "" {
		baseURL = naming.DefaultBaseURL
	}

	envVars := []corev1.EnvVar{
		{Name: naming.EnvEnvironmentID, Value: env.Spec.EnvironmentID},
		{Name: naming.EnvBaseURL, Value: baseURL},
		envFromSecret("WEBHOOK_SIGNING_KEY", env.Spec.Webhook.SigningKeySecretRef),
		{Name: "WORKER_IMAGE", Value: env.Spec.WorkerImage},
		{Name: "SESSION_JOB_NAMESPACE", Value: env.Namespace},
		{Name: "ENV_NAME", Value: env.Name},
		{Name: "ENV_KEY_SECRET_NAME", Value: env.Spec.EnvironmentKeySecretRef.Name},
		{Name: "ENV_KEY_SECRET_KEY", Value: env.Spec.EnvironmentKeySecretRef.Key},
		{Name: "SESSION_JOB_TTL_SECONDS", Value: fmt.Sprintf("%d", derefInt32(env.Spec.Webhook.TTLSecondsAfterFinished, 600))},
		{Name: "WEBHOOK_PORT", Value: fmt.Sprintf("%d", webhookContainerPort)},
		{Name: "JOB_TEMPLATE_PATH", Value: jobTemplateMountPath + "/" + jobTemplateFileName},
	}

	return corev1.PodTemplateSpec{
		ObjectMeta: metav1.ObjectMeta{Labels: naming.ObjectLabels(env, naming.ComponentWebhookHandler)},
		Spec: corev1.PodSpec{
			ServiceAccountName: naming.WebhookDeploymentName(env),
			Volumes: []corev1.Volume{{
				Name: "jobtemplate",
				VolumeSource: corev1.VolumeSource{
					ConfigMap: &corev1.ConfigMapVolumeSource{
						LocalObjectReference: corev1.LocalObjectReference{Name: webhookJobTemplateConfigMapName(env)},
					},
				},
			}},
			Containers: []corev1.Container{{
				Name:         "handler",
				Image:        image,
				Ports:        []corev1.ContainerPort{{Name: "http", ContainerPort: webhookContainerPort}},
				Env:          envVars,
				VolumeMounts: []corev1.VolumeMount{{Name: "jobtemplate", MountPath: jobTemplateMountPath, ReadOnly: true}},
				LivenessProbe: &corev1.Probe{
					ProbeHandler: corev1.ProbeHandler{
						HTTPGet: &corev1.HTTPGetAction{Path: "/healthz", Port: intstr.FromInt32(webhookContainerPort)},
					},
				},
				ReadinessProbe: &corev1.Probe{
					ProbeHandler: corev1.ProbeHandler{
						HTTPGet: &corev1.HTTPGetAction{Path: "/healthz", Port: intstr.FromInt32(webhookContainerPort)},
					},
				},
			}},
		},
	}
}

func (r *ClaudeEnvironmentReconciler) reconcileWebhookService(ctx context.Context, env *cscv1alpha1.ClaudeEnvironment) error {
	svc := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: naming.WebhookServiceName(env), Namespace: env.Namespace}}
	return r.apply(ctx, env, svc, func() error {
		svc.Labels = mergeLabels(svc.Labels, naming.ObjectLabels(env, naming.ComponentWebhookHandler))
		svc.Spec.Selector = naming.SelectorLabels(env, naming.ComponentWebhookHandler)
		svc.Spec.Type = corev1.ServiceTypeClusterIP
		svc.Spec.Ports = []corev1.ServicePort{{
			Name:       "http",
			Port:       80,
			TargetPort: intstr.FromInt32(webhookContainerPort),
			Protocol:   corev1.ProtocolTCP,
		}}
		return nil
	})
}

func (r *ClaudeEnvironmentReconciler) reconcileWebhookIngress(ctx context.Context, env *cscv1alpha1.ClaudeEnvironment) error {
	ing := &networkingv1.Ingress{ObjectMeta: metav1.ObjectMeta{Name: naming.WebhookIngressName(env), Namespace: env.Namespace}}
	cfg := env.Spec.Webhook.Ingress
	pathType := networkingv1.PathTypePrefix
	return r.apply(ctx, env, ing, func() error {
		ing.Labels = mergeLabels(ing.Labels, naming.ObjectLabels(env, naming.ComponentWebhookHandler))
		ing.Annotations = mergeLabels(ing.Annotations, cfg.Annotations)
		ing.Spec.IngressClassName = cfg.IngressClassName
		ing.Spec.Rules = []networkingv1.IngressRule{{
			Host: cfg.Host,
			IngressRuleValue: networkingv1.IngressRuleValue{
				HTTP: &networkingv1.HTTPIngressRuleValue{
					Paths: []networkingv1.HTTPIngressPath{{
						Path:     "/",
						PathType: &pathType,
						Backend: networkingv1.IngressBackend{
							Service: &networkingv1.IngressServiceBackend{
								Name: naming.WebhookServiceName(env),
								Port: networkingv1.ServiceBackendPort{Number: 80},
							},
						},
					}},
				},
			},
		}}
		return nil
	})
}
