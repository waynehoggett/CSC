package main

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"time"

	corev1 "k8s.io/api/core/v1"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/waynehoggett/csc/internal/webhook"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	signingKey := mustEnv(log, "WEBHOOK_SIGNING_KEY")
	cfg, err := ctrl.GetConfig()
	if err != nil {
		log.Error("loading kube config", "err", err)
		os.Exit(1)
	}
	k8s, err := client.New(cfg, client.Options{Scheme: clientgoscheme.Scheme})
	if err != nil {
		log.Error("building kube client", "err", err)
		os.Exit(1)
	}

	spawner := &webhook.JobSpawner{
		Client:          k8s,
		Namespace:       mustEnv(log, "SESSION_JOB_NAMESPACE"),
		EnvName:         os.Getenv("ENV_NAME"),
		WorkerImage:     mustEnv(log, "WORKER_IMAGE"),
		EnvID:           os.Getenv("ANTHROPIC_ENVIRONMENT_ID"),
		BaseURL:         os.Getenv("ANTHROPIC_BASE_URL"),
		EnvKeySecret:    mustEnv(log, "ENV_KEY_SECRET_NAME"),
		EnvKeyKey:       mustEnv(log, "ENV_KEY_SECRET_KEY"),
		TTLSeconds:      int32(envInt("SESSION_JOB_TTL_SECONDS", 600)),
		BasePodTemplate: loadJobTemplate(log, os.Getenv("JOB_TEMPLATE_PATH")),
	}

	handler := &webhook.Handler{
		Verifier: webhook.NewVerifier(signingKey),
		Spawner:  spawner,
		Log:      log,
	}

	port := envInt("WEBHOOK_PORT", 8080)
	srv := &http.Server{
		Addr:              ":" + strconv.Itoa(port),
		Handler:           handler.Routes(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	log.Info("webhook handler listening", "addr", srv.Addr)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Error("server error", "err", err)
		os.Exit(1)
	}
}

func loadJobTemplate(log *slog.Logger, path string) *corev1.PodTemplateSpec {
	if path == "" {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		log.Warn("job template not readable, using built-in default", "path", path, "err", err)
		return nil
	}
	var tmpl corev1.PodTemplateSpec
	if err := json.Unmarshal(data, &tmpl); err != nil {
		log.Warn("job template invalid, using built-in default", "err", err)
		return nil
	}
	return &tmpl
}

func mustEnv(log *slog.Logger, key string) string {
	v := os.Getenv(key)
	if v == "" {
		log.Error("required environment variable not set", "key", key)
		os.Exit(1)
	}
	return v
}

func envInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}
