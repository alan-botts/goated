package claudetui

import (
	"context"
	"os"
	"os/exec"
	"strings"
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

// runClaudeAuthProbe issues a minimal headless request, mirroring how
// internal/subagent invokes claude (same flags, workspace dir, and
// CLAUDECODE filtering).
func runClaudeAuthProbe(ctx context.Context, workspaceDir string) authProbeResult {
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "claude", "--dangerously-skip-permissions", "-p", "Reply with the single word: ok")
	cmd.Dir = workspaceDir
	cmd.Env = envWithoutClaudeCode()
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
// window. A positive verdict is held for 5 minutes (stale pane text usually
// scrolls away well within that); a failed verdict for 1 minute so a manual
// /login is noticed quickly. Inconclusive results are never cached.
func (b *TmuxBridge) verifiedAuthState(ctx context.Context) authProbeResult {
	b.probeMu.Lock()
	defer b.probeMu.Unlock()
	if time.Now().Before(b.probeExpiry) {
		return b.probeVerdict
	}
	probe := b.authProbe
	if probe == nil {
		probe = runClaudeAuthProbe
	}
	res := probe(ctx, b.WorkspaceDir)
	switch res {
	case authProbeOK:
		b.probeVerdict, b.probeExpiry = res, time.Now().Add(5*time.Minute)
	case authProbeFailed:
		b.probeVerdict, b.probeExpiry = res, time.Now().Add(time.Minute)
	}
	return res
}

// cachedAuthState returns the last unexpired probe verdict without probing —
// safe for hot paths like GetSessionState polling loops.
func (b *TmuxBridge) cachedAuthState() authProbeResult {
	b.probeMu.Lock()
	defer b.probeMu.Unlock()
	if time.Now().Before(b.probeExpiry) {
		return b.probeVerdict
	}
	return authProbeInconclusive
}

func (b *TmuxBridge) invalidateAuthProbe() {
	b.probeMu.Lock()
	b.probeExpiry = time.Time{}
	b.probeMu.Unlock()
}
