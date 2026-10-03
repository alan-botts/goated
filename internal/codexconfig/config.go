// Package codexconfig supplies Codex CLI configuration and raw arguments.
package codexconfig

import (
	"fmt"
	"strconv"
	"strings"
)

// Config controls Codex launches. Raw argument slices are trusted operator
// configuration, not user prompt content. Keep each CLI token as one JSON item.
type Config struct {
	Model                string
	ModelReasoningEffort string
	Args                 []string // appended to every Codex launch
	ExecArgs             []string // appended to fresh/headless codex exec launches
	ResumeArgs           []string // appended to codex exec resume launches
	TUIArgs              []string // appended to interactive codex launches
}

// OverrideArgs returns Codex -c arguments for the legacy named values.
func (c Config) OverrideArgs() []string {
	var args []string
	if model := strings.TrimSpace(c.Model); model != "" {
		args = append(args, "-c", fmt.Sprintf("model=%s", strconv.Quote(model)))
	}
	if effort := strings.TrimSpace(c.ModelReasoningEffort); effort != "" {
		args = append(args, "-c", fmt.Sprintf("model_reasoning_effort=%s", strconv.Quote(effort)))
	}
	return args
}

// LaunchArgs appends raw flags after the named overrides, so operator-provided
// raw flags take precedence where Codex accepts repeated options.
func (c Config) LaunchArgs(mode string) []string {
	args := append(c.OverrideArgs(), c.Args...)
	switch mode {
	case "exec":
		return append(args, c.ExecArgs...)
	case "resume":
		return append(args, c.ResumeArgs...)
	case "tui":
		return append(args, c.TUIArgs...)
	default:
		return args
	}
}
