package ui

import (
	"context"
	"encoding/json"
	"net/http"
	"os/exec"
	"strings"
	"time"

	"github.com/salahmyn/lattice/pkg/lattice/detect"
	"github.com/salahmyn/lattice/pkg/lattice/onboarding"
)

// pageOnboarding renders the v0.5.0 wizard. The page reads the current
// State from lattice/onboarding.yaml and dispatches on State.Step —
// each step is its own server-rendered form. Saving advances to the
// next step server-side via POST /api/v1/onboarding.
//
// When the state is missing or marked completed, the page bounces the
// user back to overview — the wizard self-deletes from the
// navigation once setup is done.
func (s *Server) pageOnboarding(w http.ResponseWriter, r *http.Request) {
	state, err := onboarding.Load(s.ws.LatticeDir)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if state.Step == "" || state.Completed {
		// Either onboarding has never started (no file) or finished.
		// Redirect to overview rather than show a half-page.
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	s.render(w, "onboarding.html", pageData{
		Title:    "Onboarding",
		Active:   "onboarding",
		JSONHref: "/api/v1/onboarding",
		Breadcrumbs: []crumb{{Label: "Overview", Href: "/"}, {Label: "Onboarding"}},
		Body: map[string]interface{}{
			"State":       state,
			"Steps":       allSteps(state.Step),
			"StepLabel":   stepLabel(state.Step),
		},
	})
}

// apiOnboardingGet returns the current state for clients that prefer
// JSON (the future MCP integration, dashboards, etc).
func (s *Server) apiOnboardingGet(w http.ResponseWriter, r *http.Request) {
	state, err := onboarding.Load(s.ws.LatticeDir)
	if err != nil {
		writeJSONError(w, err, http.StatusInternalServerError)
		return
	}
	writeJSON(w, state)
}

// apiOnboardingPost handles "submit this step and advance". The body
// is the partial State the form just collected; we merge with the
// loaded state, persist, advance Step, and return the new state.
//
// We treat the body as authoritative for *only* the fields the current
// step owns — never blindly overwrite (a malicious request could
// otherwise flip Completed early or rewrite Detected).
func (s *Server) apiOnboardingPost(w http.ResponseWriter, r *http.Request) {
	state, err := onboarding.Load(s.ws.LatticeDir)
	if err != nil {
		writeJSONError(w, err, http.StatusInternalServerError)
		return
	}
	if state.Step == "" {
		writeJSONError(w, errOnboardingNotStarted, http.StatusBadRequest)
		return
	}

	var body struct {
		ProjectMetadata   *onboarding.ProjectMetadata `json:"project_metadata,omitempty"`
		CodeRoots         []string                    `json:"code_roots,omitempty"`
		InstallPackage    string                      `json:"install_package,omitempty"`
		Action            string                      `json:"action,omitempty"` // "advance" | "install" | "finish"
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONError(w, err, http.StatusBadRequest)
		return
	}

	// Apply the per-step writes.
	switch state.Step {
	case onboarding.StepProjectMetadata:
		if body.ProjectMetadata != nil {
			state.ProjectMetadata = *body.ProjectMetadata
		}
	case onboarding.StepConfirmRoots:
		if len(body.CodeRoots) > 0 {
			state.Detected.CodeRoots = body.CodeRoots
		}
	case onboarding.StepPluginInstall:
		// install_package=name → run InstallCommand in a subprocess.
		// Append to PackagesInstalled on success. Skipped if the
		// caller just wants to advance without installing anything.
		if body.InstallPackage != "" {
			pkg := findPackage(state.Detected.NeedsPackages, body.InstallPackage)
			if pkg == nil {
				writeJSONError(w, errUnknownPackage, http.StatusBadRequest)
				return
			}
			cmdLine := detect.InstallCommand(detect.Package{
				Name: pkg.Name, Manager: pkg.Manager,
			})
			if cmdLine != nil {
				if err := runInstallCommand(r.Context(), cmdLine); err != nil {
					writeJSONError(w, err, http.StatusBadGateway)
					return
				}
			}
			if !containsStr(state.PackagesInstalled, pkg.Name) {
				state.PackagesInstalled = append(state.PackagesInstalled, pkg.Name)
			}
		}
	}

	// Advance — or finish, when the action is explicit and the user
	// is on the last step.
	if body.Action == "finish" || onboarding.NextStep(state.Step) == onboarding.StepDone {
		state.Step = onboarding.StepDone
		state.Completed = true
	} else if body.Action == "advance" || body.InstallPackage == "" {
		state.Step = onboarding.NextStep(state.Step)
	}

	if err := onboarding.Save(s.ws.LatticeDir, state); err != nil {
		writeJSONError(w, err, http.StatusInternalServerError)
		return
	}
	writeJSON(w, state)
}

// runInstallCommand executes one install command with a 5-minute cap.
// Mirrors the CLI's detect.go helper; duplicated here so the UI
// package doesn't import internal/cli.
func runInstallCommand(ctx context.Context, args []string) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return &installError{Cmd: strings.Join(args, " "), Output: strings.TrimSpace(string(out)), Err: err}
	}
	return nil
}

type installError struct {
	Cmd    string
	Output string
	Err    error
}

func (e *installError) Error() string {
	return e.Cmd + ": " + e.Err.Error() + " — " + e.Output
}

// errOnboardingNotStarted / errUnknownPackage are returned to the
// browser when the client's POST body doesn't match the current
// server state. Plain errors so writeJSONError shapes them
// consistently.
type sentinelError string

func (s sentinelError) Error() string { return string(s) }

const (
	errOnboardingNotStarted = sentinelError("onboarding has not been started — run `lattice init` first")
	errUnknownPackage       = sentinelError("unknown package name (not in detected.needs_packages)")
)

// findPackage scans the state's NeedsPackages by name. Returns nil if
// the name doesn't match — protects against blindly running an
// install command provided by the client.
func findPackage(pkgs []onboarding.NeedsPackage, name string) *onboarding.NeedsPackage {
	for i := range pkgs {
		if pkgs[i].Name == name {
			return &pkgs[i]
		}
	}
	return nil
}

func containsStr(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}

// allSteps returns the ordered step list and which one is current —
// used by the template to render the breadcrumb-style progress.
func allSteps(current onboarding.Step) []map[string]interface{} {
	order := []onboarding.Step{
		onboarding.StepProjectMetadata,
		onboarding.StepConfirmRoots,
		onboarding.StepPluginInstall,
		onboarding.StepScopeAction,
	}
	out := make([]map[string]interface{}, 0, len(order))
	seen := false
	for _, s := range order {
		state := "pending"
		if s == current {
			state = "current"
			seen = true
		} else if !seen {
			state = "done"
		}
		out = append(out, map[string]interface{}{
			"ID": string(s), "Label": stepLabel(s), "State": state,
		})
	}
	return out
}

func stepLabel(s onboarding.Step) string {
	switch s {
	case onboarding.StepProjectMetadata:
		return "Project metadata"
	case onboarding.StepConfirmRoots:
		return "Confirm code roots"
	case onboarding.StepPluginInstall:
		return "Install plugins"
	case onboarding.StepScopeAction:
		return "Choose next action"
	case onboarding.StepDone:
		return "Done"
	}
	return string(s)
}
