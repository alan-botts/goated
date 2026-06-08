// Package adminctl implements owner-only "/admin ..." control commands that a
// connector handles directly — before a message is routed to the runtime — so
// they work as a remote escape hatch even when the runtime session is wedged or
// busy. It is connector-agnostic: connectors parse the command, call Execute,
// send back Result.Reply, and run Result.After (if any).
package adminctl

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// helperProcessPattern matches the per-message goat helper processes that hang
// when the daemon's send path wedges (see the send-path timeout fix). It's an
// extended regex understood by pgrep on both macOS and Linux.
const helperProcessPattern = `goat send_user_(message|file)`

const helpText = "*Admin commands* (owner only)\n" +
	"`/admin status` — daemon pid, host, stuck-helper count\n" +
	"`/admin reap` — kill stuck send_user_message/file helpers\n" +
	"`/admin restart` — restart the daemon\n" +
	"`/admin help` — this message"

// Result is what a connector should do after Execute: send Reply to the owner,
// then call After (if non-nil). After is separated from Execute so the reply is
// delivered before a self-terminating action like restart kills the process.
type Result struct {
	Reply string
	After func()
}

// Parse reports whether text is an "/admin ..." invocation and returns the
// lower-cased subcommand ("help" when bare "/admin"). It does not authorize the
// caller — the connector must confirm the message came from the owner first.
func Parse(text string) (string, bool) {
	fields := strings.Fields(strings.TrimSpace(text))
	if len(fields) == 0 || fields[0] != "/admin" {
		return "", false
	}
	if len(fields) == 1 {
		return "help", true
	}
	return strings.ToLower(fields[1]), true
}

// Execute runs an admin subcommand and returns the reply (and optional deferred
// action). It assumes the caller is already authorized as the owner.
func Execute(sub string) Result {
	switch sub {
	case "help":
		return Result{Reply: helpText}
	case "status":
		return Result{Reply: statusText()}
	case "reap":
		n := reapHelpers()
		return Result{Reply: fmt.Sprintf("Reaped %d stuck helper process(es).", n)}
	case "restart":
		return Result{
			Reply: "🔄 Restarting daemon… back in a moment.",
			After: restartDaemon,
		}
	default:
		return Result{Reply: "Unknown admin command `" + sub + "`.\n\n" + helpText}
	}
}

func statusText() string {
	host, _ := os.Hostname()
	return fmt.Sprintf(
		"✅ Daemon alive and responsive\npid: %d\nhost: %s\nstuck helpers: %d\ntime: %s",
		os.Getpid(), host, countHelpers(), time.Now().Format(time.RFC3339),
	)
}

// helperPIDs returns the PIDs of running goat send_user_message/file helpers,
// excluding this process. pgrep is present on macOS and Linux.
func helperPIDs() []int {
	out, err := exec.Command("pgrep", "-f", helperProcessPattern).Output()
	if err != nil {
		return nil // non-zero exit means no matches
	}
	self := os.Getpid()
	var pids []int
	for _, field := range strings.Fields(string(out)) {
		pid, err := strconv.Atoi(field)
		if err != nil || pid == self {
			continue
		}
		pids = append(pids, pid)
	}
	return pids
}

func countHelpers() int { return len(helperPIDs()) }

// reapHelpers SIGKILLs every stuck helper and returns how many were signalled.
// The owner invoked this explicitly, so it kills all of them regardless of age.
func reapHelpers() int {
	n := 0
	for _, pid := range helperPIDs() {
		if err := syscall.Kill(pid, syscall.SIGKILL); err == nil {
			n++
		}
	}
	return n
}

// restartDaemon launches a detached "goated daemon restart". It survives the
// SIGTERM that the restart sends to this (the current) daemon.
func restartDaemon() {
	exe, err := os.Executable()
	if err != nil {
		return
	}
	cmd := exec.Command(exe, "daemon", "restart", "--reason", "owner remote /admin restart")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	_ = cmd.Start()
	// Detached on purpose — do not Wait. The restart will SIGTERM this process.
}
