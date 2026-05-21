package controller

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	cscv1alpha1 "github.com/waynehoggett/csc/api/v1alpha1"
	"github.com/waynehoggett/csc/internal/naming"
)

// envFromSecret builds an EnvVar that sources its value from a Secret key.
func envFromSecret(name string, ref cscv1alpha1.SecretKeySelector) corev1.EnvVar {
	return corev1.EnvVar{
		Name: name,
		ValueFrom: &corev1.EnvVarSource{
			SecretKeyRef: &corev1.SecretKeySelector{
				LocalObjectReference: corev1.LocalObjectReference{Name: ref.Name},
				Key:                  ref.Key,
			},
		},
	}
}

// dataPlaneEnv returns the environment variables shared by every data-plane
// workload (workers and session Jobs). It deliberately excludes the org API
// key — that credential must never reach agent tool calls.
func dataPlaneEnv(env *cscv1alpha1.ClaudeEnvironment) []corev1.EnvVar {
	baseURL := env.Spec.BaseURL
	if baseURL == "" {
		baseURL = naming.DefaultBaseURL
	}
	return []corev1.EnvVar{
		envFromSecret(naming.EnvEnvironmentKey, env.Spec.EnvironmentKeySecretRef),
		{Name: naming.EnvEnvironmentID, Value: env.Spec.EnvironmentID},
		{Name: naming.EnvBaseURL, Value: baseURL},
	}
}

// verifySecretKey confirms the named Secret exists and contains the given key.
// It returns a descriptive error suitable for surfacing in a status condition.
func verifySecretKey(ctx context.Context, c client.Client, namespace string, ref cscv1alpha1.SecretKeySelector) error {
	var secret corev1.Secret
	key := types.NamespacedName{Namespace: namespace, Name: ref.Name}
	if err := c.Get(ctx, key, &secret); err != nil {
		if apierrors.IsNotFound(err) {
			return fmt.Errorf("secret %q not found in namespace %q", ref.Name, namespace)
		}
		return fmt.Errorf("reading secret %q: %w", ref.Name, err)
	}
	if _, ok := secret.Data[ref.Key]; !ok {
		if _, ok := secret.StringData[ref.Key]; !ok {
			return fmt.Errorf("secret %q has no key %q", ref.Name, ref.Key)
		}
	}
	return nil
}
