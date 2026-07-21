package claudetui

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"
)

// authProbeResult is the verdict of a headless auth probe: an actual request
// through the claude CLI, which is definitive in a way pane text and on-disk
// expiry timestamps are not (a token can be revoked server-side while still
// looking valid on disk).
type authProbeResult int

const (
	// authProbeInconclusive means the probe could not run or failed for a
	// non-auth reason (timeout, network, binary missing). Never cached.
	authProbeInconclusive authProbeResult = iota
	// authProbeOK means a headless request succeeded — credentials work.
	authProbeOK
	// authProbeFailed means the probe hit an auth error — a manual /login
	// really is required.
	authProbeFailed
)

// positiveProbeTTL must comfortably exceed the gateway's longest
// GetSessionState polling window (postSendTimeout, 5 min): if a verified-OK
// verdict expired mid-dispatch while stale auth text was still on screen,
// classifySessionState would flip to BlockedAuth in the middle of a healthy
// long-running task and the user would get a false "login expired" message.
const positiveProbeTTL = 15 * time.Minute

// negativeProbeTTL is short so a manual /login is noticed quickly.
const negativeProbeTTL = time.Minute

// runClaudeAuthProbe issues a minimal headless request, using the same
// invocation conventions as internal/subagent (flags, workspace dir,
// CLAUDECODE filtering).
func runClaudeAuthProbe(ctx context.Context, workspaceDir string) authProbeResult {
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "claude", "--dangerously-skip-permissions", "-p", "Reply with the single word: ok")
	cmd.Dir = workspaceDir
	cmd.Env = envWithoutClaudeCode()
	// The timeout must be enforceable: claude spawns children (MCP servers,
	// shell tools) that inherit the output pipes, and killing only the direct
	// process would leave CombinedOutput blocked on pipe EOF indefinitely.
	// Run the probe in its own process group, kill the whole group on
	// cancellation, and cap the post-kill pipe drain with WaitDelay.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err != nil {
			if errors.Is(err, syscall.ESRCH) {
				return os.ErrProcessDone
			}
			return err
		}
		return nil
	}
	cmd.WaitDelay = 5 * time.Second
	out, err := cmd.CombinedOutput()
	if err == nil {
		return authProbeOK
	}
	if matchPattern(string(out), authErrorPatterns) != "" {
		return authProbeFailed
	}
	return authProbeInconclusive
}

func envWithoutClaudeCode() []string {
	env := os.Environ()
	kept := env[:0]
	for _, kv := range env {
		if !strings.HasPrefix(kv, "CLAUDECODE=") {
			kept = append(kept, kv)
		}
	}
	return kept
}

// verifiedAuthState returns the probe verdict, probing at most once per cache
// window. Probes are serialized on probeRunMu; the cache itself is guarded
// only by probeMu, which is never held across a probe — so cachedAuthState
// and invalidateAuthProbe stay wait-free even while a probe is in flight.
// Inconclusive results are never cached.
func (b *TmuxBridge) verifiedAuthState(ctx context.Context) authProbeResult {
	if res, ok := b.freshVerdict(); ok {
		return res
	}
	b.probeRunMu.Lock()
	defer b.probeRunMu.Unlock()
	// Another caller may have completed a probe while we waited.
	if res, ok := b.freshVerdict(); ok {
		return res
	}
	probe := b.authProbe
	if probe == nil {
		probe = runClaudeAuthProbe
	}
	res := probe(ctx, b.WorkspaceDir)
	b.probeMu.Lock()
	switch res {
	case authProbeOK:
		b.probeVerdict, b.probeExpiry = res, time.Now().Add(positiveProbeTTL)
	case authProbeFailed:
		b.probeVerdict, b.probeExpiry = res, time.Now().Add(negativeProbeTTL)
	}
	b.probeMu.Unlock()
	return res
}

func (b *TmuxBridge) freshVerdict() (authProbeResult, bool) {
	b.probeMu.Lock()
	defer b.probeMu.Unlock()
	if time.Now().Before(b.probeExpiry) {
		return b.probeVerdict, true
	}
	return authProbeInconclusive, false
}

// cachedAuthState returns the last unexpired probe verdict without probing
// and without waiting on any in-flight probe — safe for hot paths like
// GetSessionState polling loops.
func (b *TmuxBridge) cachedAuthState() authProbeResult {
	res, _ := b.freshVerdict()
	return res
}

func (b *TmuxBridge) invalidateAuthProbe() {
	b.probeMu.Lock()
	b.probeExpiry = time.Time{}
	b.probeMu.Unlock()
}
