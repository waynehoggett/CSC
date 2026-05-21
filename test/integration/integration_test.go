//go:build integration

// Package integration runs the reconciler against a real API server provided by
//
//	envtest. Run with: KUBEBUILDER_ASSETS=$(setup-envtest use -p path) \
//	  go test -tags integration ./test/integration/...
package integration

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"

	cscv1alpha1 "github.com/waynehoggett/csc/api/v1alpha1"
	"github.com/waynehoggett/csc/internal/controller"
)

func TestAlwaysOnReconcileAgainstAPIServer(t *testing.T) {
	env := &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join("..", "..", "config", "crd", "bases")},
		ErrorIfCRDPathMissing: true,
	}
	cfg, err := env.Start()
	if err != nil {
		t.Fatalf("start envtest (set KUBEBUILDER_ASSETS): %v", err)
	}
	t.Cleanup(func() { _ = env.Stop() })

	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := cscv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}

	c, err := client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "claude-system"}}
	if err := c.Create(ctx, ns); err != nil {
		t.Fatal(err)
	}
	mustCreate(t, c, &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Namespace: "claude-system", Name: "prod-env-key"},
		Data:       map[string][]byte{"environment-key": []byte("x")},
	})
	mustCreate(t, c, &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Namespace: "claude-system", Name: "anthropic-org-key"},
		Data:       map[string][]byte{"api-key": []byte("x")},
	})

	cenv := &cscv1alpha1.ClaudeEnvironment{
		ObjectMeta: metav1.ObjectMeta{Namespace: "claude-system", Name: "prod"},
		Spec: cscv1alpha1.ClaudeEnvironmentSpec{
			EnvironmentID:           "env_abc",
			EnvironmentKeySecretRef: cscv1alpha1.SecretKeySelector{Name: "prod-env-key", Key: "environment-key"},
			Mode:                    cscv1alpha1.ModeAlwaysOn,
			WorkerImage:             "ghcr.io/example/worker:v1",
			AlwaysOn: &cscv1alpha1.AlwaysOnSpec{
				MinReplicas: ptr.To[int32](0),
				MaxReplicas: ptr.To[int32](3),
				Scaling:     cscv1alpha1.ScalingSpec{OrgApiKeySecretRef: cscv1alpha1.SecretKeySelector{Name: "anthropic-org-key", Key: "api-key"}},
			},
		},
	}
	mustCreate(t, c, cenv)

	// KEDA CRDs are not installed in envtest, so disable KEDA reconciliation.
	// We drive Reconcile directly rather than starting a manager.
	r := &controller.ClaudeEnvironmentReconciler{Client: c, Scheme: scheme, KEDAEnabled: false}
	req := reconcileRequest(cenv)

	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := r.Reconcile(ctx, req); err != nil {
			t.Fatalf("reconcile: %v", err)
		}
		var dep appsv1.Deployment
		if err := c.Get(ctx, types.NamespacedName{Namespace: "claude-system", Name: "prod-worker"}, &dep); err == nil {
			return // success
		}
		if time.Now().After(deadline) {
			t.Fatal("worker deployment never appeared")
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func reconcileRequest(o client.Object) ctrl.Request {
	return ctrl.Request{NamespacedName: types.NamespacedName{Namespace: o.GetNamespace(), Name: o.GetName()}}
}

func mustCreate(t *testing.T, c client.Client, obj client.Object) {
	t.Helper()
	if err := c.Create(context.Background(), obj); err != nil {
		t.Fatalf("create %T: %v", obj, err)
	}
}
