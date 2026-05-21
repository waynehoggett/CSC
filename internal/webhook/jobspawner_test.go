package webhook

import (
	"net/http"
	"net/http/httptest"
	"testing"

	corev1 "k8s.io/api/core/v1"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/waynehoggett/csc/internal/naming"
)

func testSpawner() *JobSpawner {
	return &JobSpawner{
		Namespace:    "claude-system",
		EnvName:      "prod",
		WorkerImage:  "ghcr.io/example/worker:v1",
		EnvID:        "env_abc",
		BaseURL:      "https://api.anthropic.com",
		EnvKeySecret: "prod-env-key",
		EnvKeyKey:    "environment-key",
		TTLSeconds:   600,
	}
}

func TestBuildJob(t *testing.T) {
	job := testSpawner().BuildJob("sess_ABC-123", "work_1")

	if job.Name != "session-sess-abc-123" {
		t.Fatalf("unexpected job name %q", job.Name)
	}
	if job.Namespace != "claude-system" {
		t.Fatalf("unexpected namespace %q", job.Namespace)
	}
	if job.Spec.BackoffLimit == nil || *job.Spec.BackoffLimit != 0 {
		t.Fatal("expected backoffLimit 0")
	}
	if job.Spec.TTLSecondsAfterFinished == nil || *job.Spec.TTLSecondsAfterFinished != 600 {
		t.Fatal("expected ttl 600")
	}

	spec := job.Spec.Template.Spec
	if spec.RestartPolicy != corev1.RestartPolicyNever {
		t.Fatalf("expected RestartPolicy Never, got %q", spec.RestartPolicy)
	}
	if spec.AutomountServiceAccountToken == nil || *spec.AutomountServiceAccountToken {
		t.Fatal("session pods must not automount the SA token")
	}

	var worker *corev1.Container
	for i := range spec.Containers {
		if spec.Containers[i].Name == "worker" {
			worker = &spec.Containers[i]
		}
	}
	if worker == nil {
		t.Fatal("worker container missing")
	}

	env := map[string]string{}
	envFrom := map[string]string{}
	for _, e := range worker.Env {
		if e.ValueFrom != nil && e.ValueFrom.SecretKeyRef != nil {
			envFrom[e.Name] = e.ValueFrom.SecretKeyRef.Name + "/" + e.ValueFrom.SecretKeyRef.Key
			continue
		}
		env[e.Name] = e.Value
	}
	if got := envFrom[naming.EnvEnvironmentKey]; got != "prod-env-key/environment-key" {
		t.Fatalf("env key not sourced from secret: %q", got)
	}
	if env[naming.EnvSessionID] != "sess_ABC-123" {
		t.Fatalf("session id env wrong: %q", env[naming.EnvSessionID])
	}
	if env[naming.EnvWorkID] != "work_1" {
		t.Fatalf("work id env wrong: %q", env[naming.EnvWorkID])
	}
	if _, leaked := env[naming.EnvOrgAPIKey]; leaked {
		t.Fatal("org API key must never be set on a session pod")
	}

	foundMount := false
	for _, m := range worker.VolumeMounts {
		if m.MountPath == sessionOutputsPath {
			foundMount = true
		}
	}
	if !foundMount {
		t.Fatalf("expected outputs mount at %s", sessionOutputsPath)
	}
}

func TestJobName_Sanitization(t *testing.T) {
	cases := map[string]string{
		"sess_ABC":  "session-sess-abc",
		"a.b/c":     "session-a-b-c",
		"":          "session-unknown",
		"--weird--": "session-weird",
	}
	for in, want := range cases {
		if got := jobName(in); got != want {
			t.Errorf("jobName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestBuildJob_OverlaysBaseTemplate(t *testing.T) {
	s := testSpawner()
	s.BasePodTemplate = &corev1.PodTemplateSpec{
		Spec: corev1.PodSpec{
			NodeSelector: map[string]string{"disktype": "ssd"},
			Containers:   []corev1.Container{{Name: "sidecar", Image: "side:1"}},
		},
	}
	job := s.BuildJob("s1", "")
	spec := job.Spec.Template.Spec
	if spec.NodeSelector["disktype"] != "ssd" {
		t.Fatal("base nodeSelector not preserved")
	}
	names := map[string]bool{}
	for _, c := range spec.Containers {
		names[c.Name] = true
	}
	if !names["sidecar"] || !names["worker"] {
		t.Fatalf("expected sidecar and worker containers, got %v", names)
	}
}

func TestSpawn_Idempotent(t *testing.T) {
	c := fake.NewClientBuilder().WithScheme(clientgoscheme.Scheme).Build()
	s := testSpawner()
	s.Client = c

	req := httptest.NewRequest(http.MethodPost, "/", nil)
	name1, err := s.Spawn(req, "s1", "w1")
	if err != nil {
		t.Fatalf("first spawn: %v", err)
	}
	name2, err := s.Spawn(req, "s1", "w1")
	if err != nil {
		t.Fatalf("duplicate spawn should be idempotent, got %v", err)
	}
	if name1 != name2 {
		t.Fatalf("expected same job name, got %q and %q", name1, name2)
	}
}
