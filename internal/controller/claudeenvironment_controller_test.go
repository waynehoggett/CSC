package controller

import (
	"context"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	cscv1alpha1 "github.com/waynehoggett/csc/api/v1alpha1"
	"github.com/waynehoggett/csc/internal/naming"
)

func testScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(s); err != nil {
		t.Fatal(err)
	}
	if err := cscv1alpha1.AddToScheme(s); err != nil {
		t.Fatal(err)
	}
	for _, gvk := range []schema.GroupVersionKind{scaledObjectGVK, triggerAuthGVK} {
		s.AddKnownTypeWithName(gvk, &unstructured.Unstructured{})
		listGVK := gvk
		listGVK.Kind += "List"
		s.AddKnownTypeWithName(listGVK, &unstructured.UnstructuredList{})
	}
	return s
}

func newReconciler(t *testing.T, kedaEnabled bool, objs ...client.Object) (*ClaudeEnvironmentReconciler, client.Client) {
	t.Helper()
	s := testScheme(t)
	c := fake.NewClientBuilder().
		WithScheme(s).
		WithObjects(objs...).
		WithStatusSubresource(&cscv1alpha1.ClaudeEnvironment{}).
		Build()
	return &ClaudeEnvironmentReconciler{
		Client:                     c,
		Scheme:                     s,
		KEDAEnabled:                kedaEnabled,
		DefaultWebhookHandlerImage: "ghcr.io/waynehoggett/csc-webhook-handler:test",
	}, c
}

func secret(ns, name string, data map[string]string) *corev1.Secret {
	d := map[string][]byte{}
	for k, v := range data {
		d[k] = []byte(v)
	}
	return &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name}, Data: d}
}

func reconcileOnce(t *testing.T, r *ClaudeEnvironmentReconciler, env *cscv1alpha1.ClaudeEnvironment) ctrl.Result {
	t.Helper()
	res, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Namespace: env.Namespace, Name: env.Name},
	})
	if err != nil {
		t.Fatalf("reconcile error: %v", err)
	}
	return res
}

func alwaysOnEnv() *cscv1alpha1.ClaudeEnvironment {
	return &cscv1alpha1.ClaudeEnvironment{
		ObjectMeta: metav1.ObjectMeta{Name: "prod", Namespace: "claude-system", UID: "uid-1"},
		Spec: cscv1alpha1.ClaudeEnvironmentSpec{
			EnvironmentID:           "env_abc",
			EnvironmentKeySecretRef: cscv1alpha1.SecretKeySelector{Name: "prod-env-key", Key: "environment-key"},
			Mode:                    cscv1alpha1.ModeAlwaysOn,
			WorkerImage:             "ghcr.io/example/worker:v1",
			AlwaysOn: &cscv1alpha1.AlwaysOnSpec{
				MinReplicas: ptr.To[int32](0),
				MaxReplicas: ptr.To[int32](5),
				Scaling: cscv1alpha1.ScalingSpec{
					OrgApiKeySecretRef: cscv1alpha1.SecretKeySelector{Name: "anthropic-org-key", Key: "api-key"},
				},
			},
		},
	}
}

func TestReconcileAlwaysOn(t *testing.T) {
	env := alwaysOnEnv()
	r, c := newReconciler(t, true, env,
		secret("claude-system", "prod-env-key", map[string]string{"environment-key": "sk-ant-oat01-x"}),
		secret("claude-system", "anthropic-org-key", map[string]string{"api-key": "sk-ant-api03-x"}),
	)

	reconcileOnce(t, r, env)

	var dep appsv1.Deployment
	if err := c.Get(context.Background(), types.NamespacedName{Namespace: "claude-system", Name: "prod-worker"}, &dep); err != nil {
		t.Fatalf("worker deployment not created: %v", err)
	}
	if got := *dep.Spec.Replicas; got != 0 {
		t.Fatalf("expected 0 replicas on create, got %d", got)
	}

	worker := dep.Spec.Template.Spec.Containers[0]
	if worker.Image != "ghcr.io/example/worker:v1" {
		t.Fatalf("unexpected worker image %q", worker.Image)
	}
	if dep.Spec.Template.Spec.AutomountServiceAccountToken == nil || *dep.Spec.Template.Spec.AutomountServiceAccountToken {
		t.Fatal("worker must not automount SA token")
	}
	for _, e := range worker.Env {
		if e.Name == naming.EnvOrgAPIKey {
			t.Fatal("org API key leaked into worker pod")
		}
	}
	hasEnvKey := false
	for _, e := range worker.Env {
		if e.Name == naming.EnvEnvironmentKey && e.ValueFrom != nil && e.ValueFrom.SecretKeyRef != nil {
			hasEnvKey = true
		}
	}
	if !hasEnvKey {
		t.Fatal("worker missing environment key env var")
	}

	// KEDA objects.
	so := &unstructured.Unstructured{}
	so.SetGroupVersionKind(scaledObjectGVK)
	if err := c.Get(context.Background(), types.NamespacedName{Namespace: "claude-system", Name: "prod-worker"}, so); err != nil {
		t.Fatalf("scaledobject not created: %v", err)
	}
	maxRepl, _, _ := unstructured.NestedInt64(so.Object, "spec", "maxReplicaCount")
	if maxRepl != 5 {
		t.Fatalf("expected maxReplicaCount 5, got %d", maxRepl)
	}
	ta := &unstructured.Unstructured{}
	ta.SetGroupVersionKind(triggerAuthGVK)
	if err := c.Get(context.Background(), types.NamespacedName{Namespace: "claude-system", Name: "prod-keda-auth"}, ta); err != nil {
		t.Fatalf("triggerauthentication not created: %v", err)
	}

	// Status reflects readiness and deployment name.
	var got cscv1alpha1.ClaudeEnvironment
	if err := c.Get(context.Background(), types.NamespacedName{Namespace: "claude-system", Name: "prod"}, &got); err != nil {
		t.Fatal(err)
	}
	if got.Status.WorkerDeploymentName != "prod-worker" {
		t.Fatalf("status worker deployment name = %q", got.Status.WorkerDeploymentName)
	}
	if !meta.IsStatusConditionTrue(got.Status.Conditions, cscv1alpha1.ConditionReady) {
		t.Fatal("expected Ready condition true")
	}
}

func TestReconcileAlwaysOn_KEDADisabled(t *testing.T) {
	env := alwaysOnEnv()
	r, c := newReconciler(t, false, env,
		secret("claude-system", "prod-env-key", map[string]string{"environment-key": "x"}),
		secret("claude-system", "anthropic-org-key", map[string]string{"api-key": "x"}),
	)
	reconcileOnce(t, r, env)

	so := &unstructured.Unstructured{}
	so.SetGroupVersionKind(scaledObjectGVK)
	err := c.Get(context.Background(), types.NamespacedName{Namespace: "claude-system", Name: "prod-worker"}, so)
	if !apierrors.IsNotFound(err) {
		t.Fatalf("expected no ScaledObject when KEDA disabled, got %v", err)
	}
}

func webhookEnv() *cscv1alpha1.ClaudeEnvironment {
	return &cscv1alpha1.ClaudeEnvironment{
		ObjectMeta: metav1.ObjectMeta{Name: "prod", Namespace: "claude-system", UID: "uid-2"},
		Spec: cscv1alpha1.ClaudeEnvironmentSpec{
			EnvironmentID:           "env_abc",
			EnvironmentKeySecretRef: cscv1alpha1.SecretKeySelector{Name: "prod-env-key", Key: "environment-key"},
			Mode:                    cscv1alpha1.ModeWebhook,
			WorkerImage:             "ghcr.io/example/worker:v1",
			Webhook: &cscv1alpha1.WebhookSpec{
				SigningKeySecretRef:     cscv1alpha1.SecretKeySelector{Name: "prod-webhook-signing", Key: "signing-key"},
				TTLSecondsAfterFinished: ptr.To[int32](600),
				Ingress:                 &cscv1alpha1.IngressSpec{Enabled: true, Host: "hook.example.com"},
			},
		},
	}
}

func TestReconcileWebhook(t *testing.T) {
	env := webhookEnv()
	r, c := newReconciler(t, true, env,
		secret("claude-system", "prod-env-key", map[string]string{"environment-key": "x"}),
		secret("claude-system", "prod-webhook-signing", map[string]string{"signing-key": "whsec_x"}),
	)
	reconcileOnce(t, r, env)

	ctx := context.Background()
	checks := []struct {
		obj  client.Object
		name string
	}{
		{&appsv1.Deployment{}, "prod-webhook"},
		{&corev1.Service{}, "prod-webhook"},
		{&corev1.ServiceAccount{}, "prod-webhook"},
		{&corev1.ConfigMap{}, "prod-webhook-jobtemplate"},
		{&rbacv1.Role{}, "prod-webhook"},
		{&rbacv1.RoleBinding{}, "prod-webhook"},
		{&networkingv1.Ingress{}, "prod-webhook"},
	}
	for _, ch := range checks {
		if err := c.Get(ctx, types.NamespacedName{Namespace: "claude-system", Name: ch.name}, ch.obj); err != nil {
			t.Fatalf("expected %T %q to exist: %v", ch.obj, ch.name, err)
		}
	}

	// Handler must NOT receive the env key directly as a value; it references the
	// secret only via the spawned Job. It also must not see the org key at all.
	var dep appsv1.Deployment
	_ = c.Get(ctx, types.NamespacedName{Namespace: "claude-system", Name: "prod-webhook"}, &dep)
	for _, e := range dep.Spec.Template.Spec.Containers[0].Env {
		if e.Name == naming.EnvOrgAPIKey {
			t.Fatal("org API key leaked into webhook handler")
		}
	}

	// Role grants get on exactly the env-key secret.
	var role rbacv1.Role
	_ = c.Get(ctx, types.NamespacedName{Namespace: "claude-system", Name: "prod-webhook"}, &role)
	foundSecretRule := false
	for _, ru := range role.Rules {
		for _, res := range ru.Resources {
			if res == "secrets" {
				foundSecretRule = true
				if len(ru.ResourceNames) != 1 || ru.ResourceNames[0] != "prod-env-key" {
					t.Fatalf("secret rule not scoped to env key: %+v", ru.ResourceNames)
				}
			}
		}
	}
	if !foundSecretRule {
		t.Fatal("handler Role missing scoped secret rule")
	}
}

func TestReconcile_MissingSecretDegraded(t *testing.T) {
	env := alwaysOnEnv()
	// Only the env key secret exists; org key is missing.
	r, c := newReconciler(t, true, env,
		secret("claude-system", "prod-env-key", map[string]string{"environment-key": "x"}),
	)
	res := reconcileOnce(t, r, env)
	if res.RequeueAfter == 0 {
		t.Fatal("expected requeue on config error")
	}

	var got cscv1alpha1.ClaudeEnvironment
	if err := c.Get(context.Background(), types.NamespacedName{Namespace: "claude-system", Name: "prod"}, &got); err != nil {
		t.Fatal(err)
	}
	if meta.IsStatusConditionTrue(got.Status.Conditions, cscv1alpha1.ConditionReady) {
		t.Fatal("expected Ready to be false when org key secret missing")
	}
	if !meta.IsStatusConditionTrue(got.Status.Conditions, cscv1alpha1.ConditionDegraded) {
		t.Fatal("expected Degraded condition true")
	}
}
