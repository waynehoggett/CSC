package controller

import (
	"context"
	"fmt"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	cscv1alpha1 "github.com/waynehoggett/csc/api/v1alpha1"
)

// requeueAfterConfigError is how long to wait before retrying when the spec
// references a Secret that does not yet exist.
const requeueAfterConfigError = 30 * time.Second

// ClaudeEnvironmentReconciler reconciles a ClaudeEnvironment object into the
// mode-specific tree of Kubernetes resources.
type ClaudeEnvironmentReconciler struct {
	client.Client
	Scheme *runtime.Scheme

	// DefaultWebhookHandlerImage is used when spec.webhook.handlerImage is empty.
	DefaultWebhookHandlerImage string

	// KEDAEnabled gates creation of KEDA ScaledObject/TriggerAuthentication.
	// When false the worker Deployment is still created but left unscaled.
	KEDAEnabled bool
}

// +kubebuilder:rbac:groups=csc.io,resources=claudeenvironments,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=csc.io,resources=claudeenvironments/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=csc.io,resources=claudeenvironments/finalizers,verbs=update
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=services,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch
// +kubebuilder:rbac:groups=networking.k8s.io,resources=ingresses,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=batch,resources=jobs,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=serviceaccounts,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=rbac.authorization.k8s.io,resources=roles;rolebindings,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=keda.sh,resources=scaledobjects;triggerauthentications,verbs=get;list;watch;create;update;patch;delete

// Reconcile drives a ClaudeEnvironment toward its desired state.
func (r *ClaudeEnvironmentReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var env cscv1alpha1.ClaudeEnvironment
	if err := r.Get(ctx, req.NamespacedName, &env); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	base := env.DeepCopy()
	env.Status.ObservedGeneration = env.Generation

	result, reconcileErr := r.reconcile(ctx, &env)

	if statusErr := r.patchStatus(ctx, base, &env); statusErr != nil {
		logger.Error(statusErr, "failed to update status")
		if reconcileErr == nil {
			reconcileErr = statusErr
		}
	}
	return result, reconcileErr
}

// reconcile performs validation and mode dispatch, mutating env.Status.
func (r *ClaudeEnvironmentReconciler) reconcile(ctx context.Context, env *cscv1alpha1.ClaudeEnvironment) (ctrl.Result, error) {
	if err := r.validate(ctx, env); err != nil {
		r.markDegraded(env, "InvalidConfiguration", err.Error())
		// Configuration errors (e.g. missing Secret) are user-fixable; requeue
		// on a timer rather than hot-looping with exponential backoff.
		return ctrl.Result{RequeueAfter: requeueAfterConfigError}, nil
	}

	switch env.Spec.Mode {
	case cscv1alpha1.ModeAlwaysOn:
		if err := r.reconcileAlwaysOn(ctx, env); err != nil {
			r.markDegraded(env, "AlwaysOnReconcileFailed", err.Error())
			return ctrl.Result{}, err
		}
	case cscv1alpha1.ModeWebhook:
		if err := r.reconcileWebhook(ctx, env); err != nil {
			r.markDegraded(env, "WebhookReconcileFailed", err.Error())
			return ctrl.Result{}, err
		}
	}

	r.markReady(env)
	return ctrl.Result{}, nil
}

// validate checks cross-field invariants and that referenced Secrets exist.
func (r *ClaudeEnvironmentReconciler) validate(ctx context.Context, env *cscv1alpha1.ClaudeEnvironment) error {
	switch env.Spec.Mode {
	case cscv1alpha1.ModeAlwaysOn:
		if env.Spec.AlwaysOn == nil {
			return fmt.Errorf("spec.alwaysOn is required when mode=AlwaysOn")
		}
		if min, max := derefInt32(env.Spec.AlwaysOn.MinReplicas, 0), derefInt32(env.Spec.AlwaysOn.MaxReplicas, 10); min > max {
			return fmt.Errorf("spec.alwaysOn.minReplicas (%d) must be <= maxReplicas (%d)", min, max)
		}
		if err := verifySecretKey(ctx, r.Client, env.Namespace, env.Spec.AlwaysOn.Scaling.OrgApiKeySecretRef); err != nil {
			return fmt.Errorf("org API key: %w", err)
		}
	case cscv1alpha1.ModeWebhook:
		if env.Spec.Webhook == nil {
			return fmt.Errorf("spec.webhook is required when mode=Webhook")
		}
		if err := verifySecretKey(ctx, r.Client, env.Namespace, env.Spec.Webhook.SigningKeySecretRef); err != nil {
			return fmt.Errorf("webhook signing key: %w", err)
		}
	default:
		return fmt.Errorf("unknown mode %q", env.Spec.Mode)
	}

	if err := verifySecretKey(ctx, r.Client, env.Namespace, env.Spec.EnvironmentKeySecretRef); err != nil {
		return fmt.Errorf("environment key: %w", err)
	}
	return nil
}

func (r *ClaudeEnvironmentReconciler) markReady(env *cscv1alpha1.ClaudeEnvironment) {
	meta.SetStatusCondition(&env.Status.Conditions, metav1.Condition{
		Type:               cscv1alpha1.ConditionReady,
		Status:             metav1.ConditionTrue,
		Reason:             "Reconciled",
		Message:            "Environment resources reconciled",
		ObservedGeneration: env.Generation,
	})
	meta.RemoveStatusCondition(&env.Status.Conditions, cscv1alpha1.ConditionDegraded)
}

func (r *ClaudeEnvironmentReconciler) markDegraded(env *cscv1alpha1.ClaudeEnvironment, reason, msg string) {
	meta.SetStatusCondition(&env.Status.Conditions, metav1.Condition{
		Type:               cscv1alpha1.ConditionReady,
		Status:             metav1.ConditionFalse,
		Reason:             reason,
		Message:            msg,
		ObservedGeneration: env.Generation,
	})
	meta.SetStatusCondition(&env.Status.Conditions, metav1.Condition{
		Type:               cscv1alpha1.ConditionDegraded,
		Status:             metav1.ConditionTrue,
		Reason:             reason,
		Message:            msg,
		ObservedGeneration: env.Generation,
	})
}

func (r *ClaudeEnvironmentReconciler) patchStatus(ctx context.Context, base, env *cscv1alpha1.ClaudeEnvironment) error {
	return r.Status().Patch(ctx, env, client.MergeFrom(base))
}

// apply creates or updates obj, applying mutate and setting env as controller owner.
func (r *ClaudeEnvironmentReconciler) apply(ctx context.Context, env *cscv1alpha1.ClaudeEnvironment, obj client.Object, mutate func() error) error {
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, obj, func() error {
		if mutate != nil {
			if err := mutate(); err != nil {
				return err
			}
		}
		return controllerutil.SetControllerReference(env, obj, r.Scheme)
	})
	return err
}

// SetupWithManager wires the reconciler into the manager.
func (r *ClaudeEnvironmentReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&cscv1alpha1.ClaudeEnvironment{}).
		Owns(&appsv1.Deployment{}).
		Owns(&corev1.Service{}).
		Owns(&corev1.ServiceAccount{}).
		Owns(&corev1.ConfigMap{}).
		Owns(&networkingv1.Ingress{}).
		Owns(&rbacv1.Role{}).
		Owns(&rbacv1.RoleBinding{}).
		Complete(r)
}

func derefInt32(p *int32, def int32) int32 {
	if p == nil {
		return def
	}
	return *p
}
