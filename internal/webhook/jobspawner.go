package webhook

import (
	"fmt"
	"net/http"
	"regexp"
	"strings"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/waynehoggett/csc/internal/naming"
)

const sessionOutputsPath = "/mnt/session/outputs"

var nonDNS = regexp.MustCompile(`[^a-z0-9-]+`)

// JobSpawner builds and creates per-session Jobs. It is intentionally minimal:
// the Job's worker process performs the actual claim/run using the env key.
type JobSpawner struct {
	Client       client.Client
	Namespace    string
	EnvName      string
	WorkerImage  string
	EnvID        string
	BaseURL      string
	EnvKeySecret string
	EnvKeyKey    string
	TTLSeconds   int32

	// BasePodTemplate, if set, is the operator-supplied jobTemplate the worker
	// container and session env are overlaid onto.
	BasePodTemplate *corev1.PodTemplateSpec
}

// Spawn creates the session Job and returns its name. Duplicate webhook
// deliveries are idempotent: the Job name is derived from the session id, so a
// repeat delivery hits AlreadyExists and is treated as success.
func (s *JobSpawner) Spawn(r *http.Request, sessionID, workID string) (string, error) {
	job := s.BuildJob(sessionID, workID)
	if err := s.Client.Create(r.Context(), job); err != nil {
		if apierrors.IsAlreadyExists(err) {
			return job.Name, nil
		}
		return "", fmt.Errorf("creating job: %w", err)
	}
	return job.Name, nil
}

// BuildJob constructs the session Job. Pure function for straightforward testing.
func (s *JobSpawner) BuildJob(sessionID, workID string) *batchv1.Job {
	labels := map[string]string{
		naming.LabelEnvironment: s.EnvName,
		naming.LabelComponent:   naming.ComponentSession,
		naming.LabelManagedBy:   naming.ManagedByValue,
	}

	tmpl := corev1.PodTemplateSpec{}
	if s.BasePodTemplate != nil {
		tmpl = *s.BasePodTemplate.DeepCopy()
	}
	if tmpl.Labels == nil {
		tmpl.Labels = map[string]string{}
	}
	for k, v := range labels {
		tmpl.Labels[k] = v
	}
	tmpl.Spec.RestartPolicy = corev1.RestartPolicyNever
	// Session pods run untrusted agent code; deny them an API token.
	tmpl.Spec.AutomountServiceAccountToken = ptr.To(false)

	baseURL := s.BaseURL
	if baseURL == "" {
		baseURL = naming.DefaultBaseURL
	}
	worker := corev1.Container{
		Name:    "worker",
		Image:   s.WorkerImage,
		Command: []string{"ant"},
		Args:    []string{"beta:worker", "run"},
		Env: []corev1.EnvVar{
			{
				Name: naming.EnvEnvironmentKey,
				ValueFrom: &corev1.EnvVarSource{SecretKeyRef: &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{Name: s.EnvKeySecret},
					Key:                  s.EnvKeyKey,
				}},
			},
			{Name: naming.EnvEnvironmentID, Value: s.EnvID},
			{Name: naming.EnvBaseURL, Value: baseURL},
			{Name: naming.EnvSessionID, Value: sessionID},
			{Name: naming.EnvWorkID, Value: workID},
		},
		VolumeMounts: []corev1.VolumeMount{{Name: "session-outputs", MountPath: sessionOutputsPath}},
	}
	tmpl.Spec.Containers = upsertContainer(tmpl.Spec.Containers, worker)
	tmpl.Spec.Volumes = ensureVolume(tmpl.Spec.Volumes, corev1.Volume{
		Name:         "session-outputs",
		VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}},
	})

	return &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      jobName(sessionID),
			Namespace: s.Namespace,
			Labels:    labels,
		},
		Spec: batchv1.JobSpec{
			BackoffLimit:            ptr.To[int32](0),
			TTLSecondsAfterFinished: ptr.To(s.TTLSeconds),
			Template:                tmpl,
		},
	}
}

func jobName(sessionID string) string {
	id := nonDNS.ReplaceAllString(strings.ToLower(sessionID), "-")
	id = strings.Trim(id, "-")
	if len(id) > 50 {
		id = strings.Trim(id[:50], "-")
	}
	if id == "" {
		id = "unknown"
	}
	return "session-" + id
}

func upsertContainer(list []corev1.Container, c corev1.Container) []corev1.Container {
	for i := range list {
		if list[i].Name == c.Name {
			list[i].Image = c.Image
			list[i].Command = c.Command
			list[i].Args = c.Args
			list[i].Env = c.Env
			list[i].VolumeMounts = mergeVolumeMounts(list[i].VolumeMounts, c.VolumeMounts)
			return list
		}
	}
	return append([]corev1.Container{c}, list...)
}

func mergeVolumeMounts(base, add []corev1.VolumeMount) []corev1.VolumeMount {
	for _, m := range add {
		found := false
		for i := range base {
			if base[i].Name == m.Name {
				base[i] = m
				found = true
				break
			}
		}
		if !found {
			base = append(base, m)
		}
	}
	return base
}

func ensureVolume(list []corev1.Volume, v corev1.Volume) []corev1.Volume {
	for i := range list {
		if list[i].Name == v.Name {
			return list
		}
	}
	return append(list, v)
}
