package claudetui

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"goated/internal/agent"
)

// paneWithStaleAuthError reproduces a real incident: the transcript still
// shows an old 401 while the session is idle at a prompt and credentials are
// valid again.
const paneWithStaleAuthError = `● Please run /login · API Error: 401 {"type":"error","error":{"type":"authentication_error","message":"OAuth token has expired."},"request_id":null}

● Done — replied in the telegram thread.

╭──────────────────────────────────────────╮
│ ❯                                        │
╰──────────────────────────────────────────╯
`

func TestOauthCredentialsStateAt(t *testing.T) {
	now := time.Now()
	writeCreds := func(t *testing.T, content string) string {
		t.Helper()
		path := filepath.Join(t.TempDir(), ".credentials.json")
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}

	tests := []struct {
		name string
		path string
		want credentialsState
	}{
		{
			"missing file",
			filepath.Join(t.TempDir(), "nope", ".credentials.json"),
			credentialsUnknown,
		},
		{
			"malformed json",
			writeCreds(t, "{not json"),
			credentialsUnknown,
		},
		{
			"no oauth section",
			writeCreds(t, `{"somethingElse": true}`),
			credentialsUnknown,
		},
		{
			"zero expiry",
			writeCreds(t, `{"claudeAiOauth": {"expiresAt": 0}}`),
			credentialsUnknown,
		},
		{
			"unexpired token",
			writeCreds(t, `{"claudeAiOauth": {"expiresAt": `+msString(now.Add(time.Hour))+`}}`),
			credentialsValid,
		},
		{
			"expired token",
			writeCreds(t, `{"claudeAiOauth": {"expiresAt": `+msString(now.Add(-time.Hour))+`}}`),
			credentialsExpired,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := oauthCredentialsStateAt(tt.path, now); got != tt.want {
				t.Errorf("oauthCredentialsStateAt() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestOauthCredentialsStateHonorsConfigDir(t *testing.T) {
	dir := t.TempDir()
	content := `{"claudeAiOauth": {"expiresAt": ` + msString(time.Now().Add(time.Hour)) + `}}`
	if err := os.WriteFile(filepath.Join(dir, ".credentials.json"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLAUDE_CONFIG_DIR", dir)
	if got := oauthCredentialsState(time.Now()); got != credentialsValid {
		t.Errorf("oauthCredentialsState() = %v, want credentialsValid", got)
	}
}

func TestHealthFromPaneTail(t *testing.T) {
	// probeMustNotRun marks cases where the classification must be decided
	// without spending a headless request.
	probeMustNotRun := func(t *testing.T) func() authProbeResult {
		return func() authProbeResult {
			t.Error("auth probe must not run for this case")
			return authProbeInconclusive
		}
	}
	probeReturning := func(res authProbeResult) func(*testing.T) func() authProbeResult {
		return func(*testing.T) func() authProbeResult {
			return func() authProbeResult { return res }
		}
	}

	tests := []struct {
		name            string
		tail            string
		creds           credentialsState
		probe           func(*testing.T) func() authProbeResult
		wantOK          bool
		wantRecoverable bool
		wantInSummary   string
	}{
		{
			"stale auth text with valid credentials and passing probe is healthy",
			paneWithStaleAuthError,
			credentialsValid,
			probeReturning(authProbeOK),
			true, true, "headless probe verified credentials",
		},
		{
			"auth text with valid-looking but revoked credentials is non-recoverable",
			paneWithStaleAuthError,
			credentialsValid,
			probeReturning(authProbeFailed),
			false, false, "run /login",
		},
		{
			"auth text with inconclusive probe is recoverable for retry",
			paneWithStaleAuthError,
			credentialsValid,
			probeReturning(authProbeInconclusive),
			false, true, "inconclusive",
		},
		{
			"auth text with unknown credentials stays non-recoverable without probing",
			paneWithStaleAuthError,
			credentialsUnknown,
			func(t *testing.T) func() authProbeResult { return probeMustNotRun(t) },
			false, false, "run /login",
		},
		{
			"auth text with expired credentials stays non-recoverable without probing",
			paneWithStaleAuthError,
			credentialsExpired,
			func(t *testing.T) func() authProbeResult { return probeMustNotRun(t) },
			false, false, "run /login",
		},
		{
			"overloaded error is recoverable without probing",
			"● API Error: 529 overloaded_error\n❯",
			credentialsUnknown,
			func(t *testing.T) func() authProbeResult { return probeMustNotRun(t) },
			false, true, "overloaded_error",
		},
		{
			"connection error is recoverable without probing",
			"Could not connect to api.anthropic.com\n❯",
			credentialsValid,
			func(t *testing.T) func() authProbeResult { return probeMustNotRun(t) },
			false, true, "Could not connect",
		},
		{
			"auth text takes precedence over transient errors",
			"authentication_error\noverloaded_error\n❯",
			credentialsUnknown,
			func(t *testing.T) func() authProbeResult { return probeMustNotRun(t) },
			false, false, "run /login",
		},
		{
			"clean idle pane is healthy without probing",
			"● Done.\n\n╭───╮\n│ ❯ │\n╰───╯",
			credentialsUnknown,
			func(t *testing.T) func() authProbeResult { return probeMustNotRun(t) },
			true, true, "ok",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := healthFromPaneTail(tt.tail, tt.creds, tt.probe(t))
			if got.OK != tt.wantOK || got.Recoverable != tt.wantRecoverable {
				t.Errorf("healthFromPaneTail() = {OK:%v Recoverable:%v}, want {OK:%v Recoverable:%v}",
					got.OK, got.Recoverable, tt.wantOK, tt.wantRecoverable)
			}
			if !strings.Contains(got.Summary, tt.wantInSummary) {
				t.Errorf("summary %q does not contain %q", got.Summary, tt.wantInSummary)
			}
		})
	}
}

func TestVerifiedAuthStateCachesVerdicts(t *testing.T) {
	calls := 0
	verdict := authProbeOK
	b := &TmuxBridge{
		WorkspaceDir: t.TempDir(),
		authProbe: func(context.Context, string) authProbeResult {
			calls++
			return verdict
		},
	}

	if got := b.verifiedAuthState(context.Background()); got != authProbeOK {
		t.Fatalf("first call = %v, want authProbeOK", got)
	}
	if got := b.verifiedAuthState(context.Background()); got != authProbeOK {
		t.Fatalf("second call = %v, want authProbeOK", got)
	}
	if calls != 1 {
		t.Errorf("probe ran %d times, want 1 (positive verdict must be cached)", calls)
	}
	if got := b.cachedAuthState(); got != authProbeOK {
		t.Errorf("cachedAuthState() = %v, want authProbeOK", got)
	}

	b.invalidateAuthProbe()
	if got := b.cachedAuthState(); got != authProbeInconclusive {
		t.Errorf("cachedAuthState() after invalidation = %v, want authProbeInconclusive", got)
	}
	verdict = authProbeInconclusive
	if got := b.verifiedAuthState(context.Background()); got != authProbeInconclusive {
		t.Fatalf("post-invalidation call = %v, want authProbeInconclusive", got)
	}
	if calls != 2 {
		t.Errorf("probe ran %d times, want 2 (invalidation must force a re-probe)", calls)
	}
	// Inconclusive verdicts are not cached: the next call probes again.
	b.verifiedAuthState(context.Background())
	if calls != 3 {
		t.Errorf("probe ran %d times, want 3 (inconclusive must not be cached)", calls)
	}
}

// TestHealthProbePopulatesSharedCache pins the contract the dispatch path
// depends on: GetHealth's probe (via healthFromSnapshot) must land in the
// same cache GetSessionState reads. If it didn't, classifySessionState would
// see no verdict and return BlockedAuth on every poll while stale auth text
// is visible — false "login expired" messages mid-dispatch.
func TestHealthProbePopulatesSharedCache(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", credsDir(t, time.Now().Add(time.Hour)))
	b := &TmuxBridge{
		WorkspaceDir: t.TempDir(),
		authProbe:    func(context.Context, string) authProbeResult { return authProbeOK },
	}
	if got := b.healthFromSnapshot(context.Background(), paneWithStaleAuthError); !got.OK {
		t.Fatalf("healthFromSnapshot() = {OK:false Summary:%q}, want OK:true", got.Summary)
	}
	if got := b.cachedAuthState(); got != authProbeOK {
		t.Errorf("cachedAuthState() after healthFromSnapshot = %v, want authProbeOK", got)
	}
	if got := b.classifySessionState(paneWithStaleAuthError, paneWithStaleAuthError); got.Kind != agent.SessionStateAwaitingInput {
		t.Errorf("classifySessionState() after verified GetHealth = %v, want AwaitingInput", got.Kind)
	}
}

// TestRestartAndResetInvalidateProbeCache pins that both session-recycling
// entry points drop the cached verdict — the bound on the revoked-token
// trade-off window. The canceled context makes the tmux operations fail
// fast without touching a real tmux server; invalidation must happen anyway.
func TestRestartAndResetInvalidateProbeCache(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	recycles := map[string]func(*TmuxBridge){
		"RestartSession":    func(b *TmuxBridge) { _ = b.RestartSession(ctx) },
		"ResetConversation": func(b *TmuxBridge) { _, _ = b.ResetConversation(ctx, "") },
	}
	for name, recycle := range recycles {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			b := &TmuxBridge{
				WorkspaceDir: dir,
				LogDir:       dir,
				SessionName:  "goated-test-does-not-exist",
				authProbe:    func(context.Context, string) authProbeResult { return authProbeOK },
			}
			if got := b.verifiedAuthState(context.Background()); got != authProbeOK {
				t.Fatalf("seeding probe cache failed: %v", got)
			}
			recycle(b)
			if got := b.cachedAuthState(); got != authProbeInconclusive {
				t.Errorf("cachedAuthState() after %s = %v, want authProbeInconclusive (cache must be invalidated)", name, got)
			}
		})
	}
}

// TestCacheReadersDoNotBlockOnInFlightProbe pins that cachedAuthState and
// invalidateAuthProbe stay responsive while a probe is running — a hung
// probe must never wedge GetSessionState polling or session restarts.
func TestCacheReadersDoNotBlockOnInFlightProbe(t *testing.T) {
	release := make(chan struct{})
	started := make(chan struct{})
	b := &TmuxBridge{
		WorkspaceDir: t.TempDir(),
		authProbe: func(context.Context, string) authProbeResult {
			close(started)
			<-release
			return authProbeOK
		},
	}
	go b.verifiedAuthState(context.Background())
	<-started

	done := make(chan struct{})
	go func() {
		b.cachedAuthState()
		b.invalidateAuthProbe()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Error("cachedAuthState/invalidateAuthProbe blocked behind an in-flight probe")
	}
	close(release)
}

// TestProbeVerdictTTLSemantics pins the load-bearing timing rules with a
// fake clock: the ~15-min positive TTL, the ~1-min negative TTL, and the
// probing-path renewal that guarantees a dispatch admitted by GetHealth
// cannot see its positive verdict expire inside the gateway's 5-min
// post-send polling window.
func TestProbeVerdictTTLSemantics(t *testing.T) {
	base := time.Now()
	current := base
	calls := 0
	verdict := authProbeOK
	b := &TmuxBridge{
		WorkspaceDir: t.TempDir(),
		nowFn:        func() time.Time { return current },
		authProbe: func(context.Context, string) authProbeResult {
			calls++
			return verdict
		},
	}
	ctx := context.Background()

	// t=0: probe runs, positive verdict cached until t+15m.
	if got := b.verifiedAuthState(ctx); got != authProbeOK || calls != 1 {
		t.Fatalf("initial probe: verdict=%v calls=%d", got, calls)
	}

	// t=6m: 9m of life left (>= renew window) — cache hit, no re-probe.
	current = base.Add(6 * time.Minute)
	if got := b.verifiedAuthState(ctx); got != authProbeOK || calls != 1 {
		t.Errorf("t=6m: verdict=%v calls=%d, want cache hit with 1 call", got, calls)
	}

	// t=8m: under 8m of life left — the probing path must renew so a
	// dispatch admitted now outlives the 5-min polling window.
	current = base.Add(8 * time.Minute)
	if got := b.verifiedAuthState(ctx); got != authProbeOK || calls != 2 {
		t.Errorf("t=8m: verdict=%v calls=%d, want renewal probe (2 calls)", got, calls)
	}
	// Renewal reset expiry to t=8m+15m=23m. The hot path honors it to the end...
	current = base.Add(22 * time.Minute)
	if got := b.cachedAuthState(); got != authProbeOK {
		t.Errorf("t=22m: cachedAuthState=%v, want authProbeOK (positive TTL is 15m from renewal)", got)
	}
	// ...and not a moment longer.
	current = base.Add(23*time.Minute + time.Second)
	if got := b.cachedAuthState(); got != authProbeInconclusive {
		t.Errorf("t=23m+1s: cachedAuthState=%v, want expired", got)
	}

	// Negative verdicts: cached for ~1 minute, honored without renewal.
	verdict = authProbeFailed
	nb := &TmuxBridge{
		WorkspaceDir: t.TempDir(),
		nowFn:        func() time.Time { return current },
		authProbe: func(context.Context, string) authProbeResult {
			calls++
			return verdict
		},
	}
	negBase := current
	if got := nb.verifiedAuthState(ctx); got != authProbeFailed {
		t.Fatalf("negative probe: verdict=%v", got)
	}
	probesAfterNeg := calls
	current = negBase.Add(30 * time.Second)
	if got := nb.verifiedAuthState(ctx); got != authProbeFailed || calls != probesAfterNeg {
		t.Errorf("t=+30s: verdict=%v calls=%d, want cached failure with no re-probe", got, calls)
	}
	if got := nb.cachedAuthState(); got != authProbeFailed {
		t.Errorf("t=+30s: cachedAuthState=%v, want authProbeFailed", got)
	}
	current = negBase.Add(61 * time.Second)
	if got := nb.cachedAuthState(); got != authProbeInconclusive {
		t.Errorf("t=+61s: cachedAuthState=%v, want expired (negative TTL is ~1m)", got)
	}
}

// msString renders a timestamp as epoch milliseconds, matching the
// expiresAt format Claude Code writes.
func msString(ts time.Time) string {
	return strconv.FormatInt(ts.UnixMilli(), 10)
}

// credsDir writes a credentials file expiring at the given time into a fresh
// config dir and returns the dir, for use with t.Setenv(CLAUDE_CONFIG_DIR).
func credsDir(t *testing.T, expiresAt time.Time) string {
	t.Helper()
	dir := t.TempDir()
	content := `{"claudeAiOauth": {"expiresAt": ` + msString(expiresAt) + `}}`
	if err := os.WriteFile(filepath.Join(dir, ".credentials.json"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func probeNever(t *testing.T) func(context.Context, string) authProbeResult {
	return func(context.Context, string) authProbeResult {
		t.Error("auth probe must not run for this case")
		return authProbeInconclusive
	}
}

// TestHealthFromSnapshotWiring pins that GetHealth's classification really
// consults the on-disk credentials and the probe — a mutation that hardcodes
// the credentials state (reintroducing the crash-loop incident) must fail
// here, not just in the pure-function tests.
func TestHealthFromSnapshotWiring(t *testing.T) {
	ctx := context.Background()

	t.Run("stale auth text with valid on-disk creds and passing probe is healthy", func(t *testing.T) {
		t.Setenv("CLAUDE_CONFIG_DIR", credsDir(t, time.Now().Add(time.Hour)))
		b := &TmuxBridge{
			WorkspaceDir: t.TempDir(),
			authProbe:    func(context.Context, string) authProbeResult { return authProbeOK },
		}
		got := b.healthFromSnapshot(ctx, paneWithStaleAuthError)
		if !got.OK {
			t.Errorf("healthFromSnapshot() = {OK:false Summary:%q}, want OK:true", got.Summary)
		}
	})

	t.Run("stale auth text with expired on-disk creds is non-recoverable without probing", func(t *testing.T) {
		t.Setenv("CLAUDE_CONFIG_DIR", credsDir(t, time.Now().Add(-time.Hour)))
		b := &TmuxBridge{WorkspaceDir: t.TempDir(), authProbe: probeNever(t)}
		got := b.healthFromSnapshot(ctx, paneWithStaleAuthError)
		if got.OK || got.Recoverable {
			t.Errorf("healthFromSnapshot() = {OK:%v Recoverable:%v}, want non-recoverable failure", got.OK, got.Recoverable)
		}
	})

	t.Run("stale auth text with no creds file is non-recoverable without probing", func(t *testing.T) {
		t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
		b := &TmuxBridge{WorkspaceDir: t.TempDir(), authProbe: probeNever(t)}
		got := b.healthFromSnapshot(ctx, paneWithStaleAuthError)
		if got.OK || got.Recoverable {
			t.Errorf("healthFromSnapshot() = {OK:%v Recoverable:%v}, want non-recoverable failure", got.OK, got.Recoverable)
		}
	})

	t.Run("clean pane is healthy regardless of creds", func(t *testing.T) {
		t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
		b := &TmuxBridge{WorkspaceDir: t.TempDir(), authProbe: probeNever(t)}
		if got := b.healthFromSnapshot(ctx, "● Done.\n\n╭───╮\n│ ❯ │\n╰───╯"); !got.OK {
			t.Errorf("healthFromSnapshot() = {OK:false Summary:%q}, want OK:true", got.Summary)
		}
	})
}

// TestClassifySessionState pins the GetSessionState half of the fix: the
// BlockedAuth gate must consult both the on-disk credentials and the cached
// probe verdict, and the newly unified "API Error: 401" pattern must match.
func TestClassifySessionState(t *testing.T) {
	seedProbeOK := func(t *testing.T, b *TmuxBridge) {
		t.Helper()
		b.authProbe = func(context.Context, string) authProbeResult { return authProbeOK }
		if got := b.verifiedAuthState(context.Background()); got != authProbeOK {
			t.Fatalf("seeding probe cache failed: %v", got)
		}
	}

	t.Run("stale auth text with valid creds and verified probe awaits input", func(t *testing.T) {
		t.Setenv("CLAUDE_CONFIG_DIR", credsDir(t, time.Now().Add(time.Hour)))
		b := &TmuxBridge{WorkspaceDir: t.TempDir()}
		seedProbeOK(t, b)
		got := b.classifySessionState(paneWithStaleAuthError, paneWithStaleAuthError)
		if got.Kind != agent.SessionStateAwaitingInput {
			t.Errorf("Kind = %v, want AwaitingInput", got.Kind)
		}
	})

	t.Run("auth text with valid creds but no cached verdict blocks on auth", func(t *testing.T) {
		t.Setenv("CLAUDE_CONFIG_DIR", credsDir(t, time.Now().Add(time.Hour)))
		b := &TmuxBridge{WorkspaceDir: t.TempDir()}
		got := b.classifySessionState(paneWithStaleAuthError, paneWithStaleAuthError)
		if got.Kind != agent.SessionStateBlockedAuth {
			t.Errorf("Kind = %v, want BlockedAuth", got.Kind)
		}
	})

	t.Run("auth text with expired creds blocks even with a cached verdict", func(t *testing.T) {
		b := &TmuxBridge{WorkspaceDir: t.TempDir()}
		seedProbeOK(t, b)
		t.Setenv("CLAUDE_CONFIG_DIR", credsDir(t, time.Now().Add(-time.Hour)))
		got := b.classifySessionState(paneWithStaleAuthError, paneWithStaleAuthError)
		if got.Kind != agent.SessionStateBlockedAuth {
			t.Errorf("Kind = %v, want BlockedAuth", got.Kind)
		}
	})

	t.Run("bare API Error 401 text blocks on auth", func(t *testing.T) {
		t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
		b := &TmuxBridge{WorkspaceDir: t.TempDir()}
		pane := "● API Error: 401 request failed\n\n╭───╮\n│ ❯ │\n╰───╯"
		got := b.classifySessionState(pane, pane)
		if got.Kind != agent.SessionStateBlockedAuth {
			t.Errorf("Kind = %v, want BlockedAuth", got.Kind)
		}
	})

	t.Run("stable clean pane with prompt awaits input", func(t *testing.T) {
		t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
		b := &TmuxBridge{WorkspaceDir: t.TempDir()}
		pane := "● Done.\n\n╭───╮\n│ ❯ │\n╰───╯"
		got := b.classifySessionState(pane, pane)
		if got.Kind != agent.SessionStateAwaitingInput {
			t.Errorf("Kind = %v, want AwaitingInput", got.Kind)
		}
	})

	t.Run("changing pane is generating", func(t *testing.T) {
		t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
		b := &TmuxBridge{WorkspaceDir: t.TempDir()}
		got := b.classifySessionState("thinking...", "thinking....")
		if got.Kind != agent.SessionStateGenerating {
			t.Errorf("Kind = %v, want Generating", got.Kind)
		}
	})

	t.Run("stable pane without prompt is unknown-stable", func(t *testing.T) {
		t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
		b := &TmuxBridge{WorkspaceDir: t.TempDir()}
		got := b.classifySessionState("some output", "some output")
		if got.Kind != agent.SessionStateUnknownStable {
			t.Errorf("Kind = %v, want UnknownStable", got.Kind)
		}
	})
}
