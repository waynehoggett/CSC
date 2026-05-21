//go:build e2e

// Package e2e exercises CSC against a real cluster (e.g. kind) that already has
// the controller installed (make deploy). Run with:
//
//	go test -tags e2e ./test/e2e/...
//
// It applies a Webhook-mode ClaudeEnvironment and waits for the controller to
// reconcile the handler Deployment, Service, and RBAC.
package e2e

import (
	"context"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	cscv1alpha1 "github.com/waynehoggett/csc/api/v1alpha1"
)

const e2eNamespace = "claude-system"

func TestWebhookModeReconciles(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := cscv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	cfg, err := ctrl.GetConfig()
	if err != nil {
		t.Fatalf("kubeconfig (point KUBECONFIG at your kind cluster): %v", err)
	}
	c, err := client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	ensure(t, c, &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Namespace: e2eNamespace, Name: "e2e-env-key"},
		Data:       map[string][]byte{"environment-key": []byte("sk-ant-oat01-e2e")},
	})
	ensure(t, c, &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Namespace: e2eNamespace, Name: "e2e-webhook-signing"},
		Data:       map[string][]byte{"signing-key": []byte("whsec_e2e")},
	})

	cenv := &cscv1alpha1.ClaudeEnvironment{
		ObjectMeta: metav1.ObjectMeta{Namespace: e2eNamespace, Name: "e2e"},
		Spec: cscv1alpha1.ClaudeEnvironmentSpec{
			EnvironmentID:           "env_e2e",
			EnvironmentKeySecretRef: cscv1alpha1.SecretKeySelector{Name: "e2e-env-key", Key: "environment-key"},
			Mode:                    cscv1alpha1.ModeWebhook,
			WorkerImage:             "ghcr.io/waynehoggett/csc-worker:latest",
			Webhook: &cscv1alpha1.WebhookSpec{
				SigningKeySecretRef:     cscv1alpha1.SecretKeySelector{Name: "e2e-webhook-signing", Key: "signing-key"},
				TTLSecondsAfterFinished: ptr.To[int32](120),
			},
		},
	}
	ensure(t, c, cenv)
	t.Cleanup(func() { _ = c.Delete(ctx, cenv) })

	deadline := time.Now().Add(60 * time.Second)
	for {
		var dep appsv1.Deployment
		err := c.Get(ctx, types.NamespacedName{Namespace: e2eNamespace, Name: "e2e-webhook"}, &dep)
		if err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("webhook handler deployment not reconciled in time: %v", err)
		}
		time.Sleep(time.Second)
	}
}

func ensure(t *testing.T, c client.Client, obj client.Object) {
	t.Helper()
	err := c.Create(context.Background(), obj)
	if err != nil && !apierrors.IsAlreadyExists(err) {
		t.Fatalf("create %T: %v", obj, err)
	}
}
