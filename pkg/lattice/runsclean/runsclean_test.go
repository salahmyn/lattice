package runsclean

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/salahmyn/lattice/pkg/lattice/config"
)

func TestInstallFailureStopsTheRun(t *testing.T) {
	rep := Run(context.Background(), t.TempDir(), config.Runtime{
		CleanInstall: "echo broken-lockfile && false",
		Build:        "true",
	})
	if rep.Pass {
		t.Fatal("expected failing report")
	}
	if len(rep.Steps) != 1 || rep.Steps[0].Step != StepInstall || rep.Steps[0].OK {
		t.Fatalf("expected single failing install step, got %+v", rep.Steps)
	}
	if rep.Steps[0].Detail == "" {
		t.Fatal("expected failure detail with output tail")
	}
}

func TestPassWithoutBootSkipsBootAndProbes(t *testing.T) {
	rep := Run(context.Background(), t.TempDir(), config.Runtime{
		CleanInstall: "true",
		Build:        "true",
	})
	if !rep.Pass {
		t.Fatalf("expected pass, got %+v", rep.Steps)
	}
	last := rep.Steps[len(rep.Steps)-1]
	if last.Step != StepBoot || !last.Skipped {
		t.Fatalf("expected skipped boot step, got %+v", last)
	}
}

func TestBootExitingInsideWindowFails(t *testing.T) {
	rep := Run(context.Background(), t.TempDir(), config.Runtime{
		Boot:       "true", // exits immediately
		BootWaitMS: 300,
	})
	if rep.Pass {
		t.Fatal("expected failing report when boot exits early")
	}
}

func TestProbesAgainstBootedApp(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	rep := Run(context.Background(), t.TempDir(), config.Runtime{
		Boot:       "sleep 30",
		BootWaitMS: 50,
		Probes: []config.Probe{
			{URL: srv.URL + "/health", ExpectStatus: 200},
			{URL: srv.URL + "/teams", ExpectStatus: 401},
		},
	})
	if !rep.Pass {
		t.Fatalf("expected pass, got %+v", rep.Steps)
	}

	rep = Run(context.Background(), t.TempDir(), config.Runtime{
		Boot:       "sleep 30",
		BootWaitMS: 50,
		Probes:     []config.Probe{{URL: srv.URL + "/teams", ExpectStatus: 200}},
	})
	if rep.Pass {
		t.Fatal("expected probe status mismatch to fail the report")
	}
}
