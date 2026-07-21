package claudetui

import (
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
	tests := []struct {
		name            string
		tail            string
		creds           credentialsState
		wantOK          bool
		wantRecoverable bool
		wantInSummary   string
	}{
		{
			"stale auth text with valid credentials is recoverable",
			paneWithStaleAuthError,
			credentialsValid,
			false, true, "unexpired OAuth token",
		},
		{
			"auth text with unknown credentials stays non-recoverable",
			paneWithStaleAuthError,
			credentialsUnknown,
			false, false, "run /login",
		},
		{
			"auth text with expired credentials stays non-recoverable",
			paneWithStaleAuthError,
			credentialsExpired,
			false, false, "run /login",
		},
		{
			"overloaded error is recoverable regardless of credentials",
			"● API Error: 529 overloaded_error\n❯",
			credentialsUnknown,
			false, true, "overloaded_error",
		},
		{
			"connection error is recoverable",
			"Could not connect to api.anthropic.com\n❯",
			credentialsValid,
			false, true, "Could not connect",
		},
		{
			"auth text takes precedence over transient errors",
			"authentication_error\noverloaded_error\n❯",
			credentialsUnknown,
			false, false, "run /login",
		},
		{
			"clean idle pane is healthy",
			"● Done.\n\n╭───╮\n│ ❯ │\n╰───╯",
			credentialsUnknown,
			true, true, "ok",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := healthFromPaneTail(tt.tail, tt.creds)
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

// msString renders a timestamp as epoch milliseconds, matching the
// expiresAt format Claude Code writes.
func msString(ts time.Time) string {
	return strconv.FormatInt(ts.UnixMilli(), 10)
}
