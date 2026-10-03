package codextui

import (
	"os/exec"
	"strings"
	"testing"

	"goated/internal/codexconfig"
)

func TestSessionLaunchCommandQuotesArbitraryArgs(t *testing.T) {
	config := codexconfig.Config{Args: []string{"-c", `custom.key="a b"`, "--enable", "can't-break-shell"}, TUIArgs: []string{"--search"}}
	cmd := sessionLaunchCommand("/tmp/workspace", config)
	for _, want := range []string{`custom.key="a b"`, `--search`, `can'"'"'t-break-shell`} {
		if !strings.Contains(cmd, want) {
			t.Fatalf("command %q does not contain %q", cmd, want)
		}
	}
	if out, err := exec.Command("sh", "-n", "-c", cmd).CombinedOutput(); err != nil {
		t.Fatalf("invalid shell command: %v: %s", err, out)
	}
}
