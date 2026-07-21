package claudetui

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
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

// msString renders a timestamp as epoch milliseconds, matching the
// expiresAt format Claude Code writes.
func msString(ts time.Time) string {
	return strconv.FormatInt(ts.UnixMilli(), 10)
}
