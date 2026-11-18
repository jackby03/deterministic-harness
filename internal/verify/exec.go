package verify

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/jackby03/deterministic-harness/internal/registry"
)

// Executor runs one gate command. The host backend exists so the verifier can
// be developed and debugged without an image; the container backend is the real
// one, because a baseline measured in an unpinned environment is an opinion.
type Executor struct {
	backend  string
	runtime  registry.Runtime
	root     string
	imageRef string
	probeErr error
}

// ExecResult is what one gate command produced.
type ExecResult struct {
	Output   []byte
	ExitCode int
	Ran      bool
	Reason   string
}

// NewExecutor probes once per run, so a missing image produces one reason
// repeated on every gate instead of a different failure per gate.
func NewExecutor(backend string, r *registry.Registry, root string) *Executor {
	e := &Executor{backend: backend, runtime: r.Runtime, root: root}
	if backend != "container" {
		return e
	}
	if !strings.HasPrefix(r.Runtime.Digest, "sha256:") {
		e.probeErr = fmt.Errorf("runtime.digest is not pinned (got %q)", r.Runtime.Digest)
		return e
	}
	e.imageRef = r.Runtime.Image + "@" + r.Runtime.Digest

	if _, err := exec.LookPath("docker"); err != nil {
		e.probeErr = fmt.Errorf("docker is not installed")
		return e
	}
	probe := exec.Command("docker", "image", "inspect", e.imageRef)
	probe.Stdout, probe.Stderr = nil, nil
	if err := probe.Run(); err != nil {
		e.probeErr = fmt.Errorf("image %s is not present locally (docker pull it by digest)", e.imageRef)
	}
	return e
}

// Available reports whether the backend can run anything at all.
func (e *Executor) Available() (bool, string) {
	if e.backend != "container" {
		return true, ""
	}
	if e.probeErr != nil {
		return false, e.probeErr.Error()
	}
	return true, ""
}

// Run executes cmd for a gate, passing the gate's inventory through a mounted
// directory so the registry stays the only source of that data.
func (e *Executor) Run(gateName, cmd string, env map[string]string, inventory map[string]any) ExecResult {
	if ok, reason := e.Available(); !ok {
		return ExecResult{Ran: false, Reason: reason}
	}

	if e.backend != "container" {
		c := exec.Command("bash", "-c", cmd)
		c.Dir = e.root
		c.Env = append(os.Environ(), kv(env)...)
		return finish(c.CombinedOutput())
	}

	invDir, err := os.MkdirTemp("", "gate-inv-")
	if err != nil {
		return ExecResult{Ran: false, Reason: "cannot stage inventory: " + err.Error()}
	}
	defer func() { _ = os.RemoveAll(invDir) }()
	for k, v := range inventory {
		raw := fmt.Sprintf("%v", v)
		_ = os.WriteFile(filepath.Join(invDir, k), []byte(raw), 0o600)
	}

	args := dockerArgs(e.runtime, e.root, e.imageRef, gateName, invDir, cmd, env)
	c := exec.Command("docker", args...)
	return finish(c.CombinedOutput())
}

// dockerArgs is pure so the flags can be asserted without a daemon.
func dockerArgs(rt registry.Runtime, root, imageRef, gateName, invDir, cmd string, env map[string]string) []string {
	args := []string{"run", "--rm", "--init"}

	if rt.Platform != "" {
		args = append(args, "--platform", rt.Platform)
	}
	if rt.User != "" {
		args = append(args, "--user", rt.User)
	}
	if rt.Network != "" {
		args = append(args, "--network", rt.Network)
	}
	workdir := rt.Workdir
	if workdir == "" {
		workdir = "/src"
	}
	args = append(args, "--workdir", workdir)

	keys := make([]string, 0, len(rt.Volumes))
	for k := range rt.Volumes {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		spec := rt.Volumes[k]
		if k == "repo" {
			args = append(args, "-v", root+":"+spec)
			continue
		}
		args = append(args, "-v", spec)
	}

	args = append(args,
		"-v", invDir+":/gate-inventory:ro",
		"-e", "GATE_NAME="+gateName,
		"-e", "GATE_INVENTORY=/gate-inventory",
	)

	envKeys := make([]string, 0, len(env))
	for k := range env {
		envKeys = append(envKeys, k)
	}
	sort.Strings(envKeys)
	for _, k := range envKeys {
		args = append(args, "-e", k+"="+env[k])
	}

	args = append(args, imageRef, "bash", "-c", cmd)
	return args
}

func finish(out []byte, err error) ExecResult {
	res := ExecResult{Output: out, Ran: true}
	if err == nil {
		return res
	}
	var exitErr *exec.ExitError
	if ok := asExitError(err, &exitErr); ok {
		res.ExitCode = exitErr.ExitCode()
		return res
	}
	res.Ran = false
	res.Reason = err.Error()
	return res
}

func kv(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k, v := range m {
		out = append(out, k+"="+v)
	}
	sort.Strings(out)
	return out
}
