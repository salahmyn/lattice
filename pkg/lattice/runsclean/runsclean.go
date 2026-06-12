// Package runsclean implements the V0 runs-clean gate ("gate zero"):
// from a clean state the application installs, builds, boots, and
// answers its smoke probes. A green test suite atop an app that won't
// start is a critical finding, not progress — nothing in a workspace
// may be reported demonstrated while V0 fails.
//
// The gate is driven entirely by the workspace's `runtime:` config
// block; it runs the project's own commands and never guesses a stack.
package runsclean

import (
	"context"
	"fmt"
	"net/http"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"github.com/salahmyn/lattice/pkg/lattice/config"
)

// Step names, in execution order.
const (
	StepInstall = "install"
	StepBuild   = "build"
	StepBoot    = "boot"
	StepProbe   = "probe"
)

// StepResult is the outcome of one V0 step.
type StepResult struct {
	Step    string `json:"step"`              // install | build | boot | probe
	Command string `json:"command,omitempty"` // the command or probe URL
	OK      bool   `json:"ok"`
	Skipped bool   `json:"skipped,omitempty"`
	Detail  string `json:"detail,omitempty"` // failure output tail or probe status
}

// Report is the full V0 outcome. Pass is true only when every executed
// step succeeded.
type Report struct {
	Root  string       `json:"root"`
	Pass  bool         `json:"pass"`
	Steps []StepResult `json:"steps"`
}

const (
	defaultBootWait = 5 * time.Second
	commandTimeout  = 10 * time.Minute
	outputTail      = 400
)

// Run executes the V0 sequence in root per rt. It stops at the first
// failing stage (a broken install makes later stages meaningless) but
// runs all probes once the app is up.
func Run(ctx context.Context, root string, rt config.Runtime) Report {
	rep := Report{Root: root, Pass: true}

	for _, step := range []struct{ name, cmd string }{
		{StepInstall, rt.CleanInstall},
		{StepBuild, rt.Build},
	} {
		if step.cmd == "" {
			rep.Steps = append(rep.Steps, StepResult{Step: step.name, Skipped: true})
			continue
		}
		if out, err := run(ctx, root, step.cmd); err != nil {
			rep.Steps = append(rep.Steps, StepResult{Step: step.name, Command: step.cmd, Detail: tail(out, err)})
			rep.Pass = false
			return rep
		}
		rep.Steps = append(rep.Steps, StepResult{Step: step.name, Command: step.cmd, OK: true})
	}

	if rt.Boot == "" {
		rep.Steps = append(rep.Steps, StepResult{Step: StepBoot, Skipped: true})
		return rep
	}

	boot, stop, err := bootApp(ctx, root, rt.Boot, bootWait(rt))
	rep.Steps = append(rep.Steps, boot)
	if err != nil {
		rep.Pass = false
		return rep
	}
	defer stop()

	client := &http.Client{Timeout: 10 * time.Second}
	for _, p := range rt.Probes {
		rep.Steps = append(rep.Steps, probe(ctx, client, p))
	}
	for _, s := range rep.Steps {
		if !s.OK && !s.Skipped {
			rep.Pass = false
		}
	}
	return rep
}

func bootWait(rt config.Runtime) time.Duration {
	if rt.BootWaitMS > 0 {
		return time.Duration(rt.BootWaitMS) * time.Millisecond
	}
	return defaultBootWait
}

// bootApp starts the boot command in its own process group, waits the
// boot window, and reports failure if the process died inside it. The
// returned stop function terminates the whole group.
func bootApp(ctx context.Context, root, cmd string, wait time.Duration) (StepResult, func(), error) {
	c := exec.CommandContext(ctx, "sh", "-c", cmd)
	c.Dir = root
	c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	var buf strings.Builder
	c.Stdout, c.Stderr = &buf, &buf
	if err := c.Start(); err != nil {
		return StepResult{Step: StepBoot, Command: cmd, Detail: err.Error()}, nil, err
	}
	exited := make(chan error, 1)
	go func() { exited <- c.Wait() }()

	select {
	case err := <-exited:
		detail := fmt.Sprintf("exited within boot window: %v; %s", err, tail(buf.String(), nil))
		return StepResult{Step: StepBoot, Command: cmd, Detail: detail}, nil,
			fmt.Errorf("boot exited early")
	case <-time.After(wait):
	}

	stop := func() {
		// Negative pid signals the process group — boot commands commonly
		// spawn children (shells, dev servers) that a bare Kill would orphan.
		_ = syscall.Kill(-c.Process.Pid, syscall.SIGTERM)
		select {
		case <-exited:
		case <-time.After(5 * time.Second):
			_ = syscall.Kill(-c.Process.Pid, syscall.SIGKILL)
		}
	}
	return StepResult{Step: StepBoot, Command: cmd, OK: true}, stop, nil
}

func probe(ctx context.Context, client *http.Client, p config.Probe) StepResult {
	method := p.Method
	if method == "" {
		method = http.MethodGet
	}
	res := StepResult{Step: StepProbe, Command: fmt.Sprintf("%s %s", method, p.URL)}
	req, err := http.NewRequestWithContext(ctx, method, p.URL, nil)
	if err != nil {
		res.Detail = err.Error()
		return res
	}
	resp, err := client.Do(req)
	if err != nil {
		res.Detail = err.Error()
		return res
	}
	defer resp.Body.Close()
	switch {
	case p.ExpectStatus != 0 && resp.StatusCode != p.ExpectStatus:
		res.Detail = fmt.Sprintf("expected %d, got %d", p.ExpectStatus, resp.StatusCode)
	case p.ExpectStatus == 0 && (resp.StatusCode < 200 || resp.StatusCode > 299):
		res.Detail = fmt.Sprintf("expected 2xx, got %d", resp.StatusCode)
	default:
		res.OK = true
		res.Detail = fmt.Sprintf("%d", resp.StatusCode)
	}
	return res
}

func run(ctx context.Context, root, cmd string) (string, error) {
	cctx, cancel := context.WithTimeout(ctx, commandTimeout)
	defer cancel()
	c := exec.CommandContext(cctx, "sh", "-c", cmd)
	c.Dir = root
	out, err := c.CombinedOutput()
	return string(out), err
}

// tail compresses command output (plus the triggering error) to the
// last few hundred bytes — enough to diagnose, small enough to ledger.
func tail(out string, err error) string {
	s := strings.TrimSpace(out)
	if err != nil {
		if s == "" {
			s = err.Error()
		} else {
			s = err.Error() + ": " + s
		}
	}
	if len(s) > outputTail {
		s = "…" + s[len(s)-outputTail:]
	}
	return s
}
