// Package codexconfig supplies explicit Codex CLI configuration overrides.
package codexconfig

import (
	"fmt"
	"strconv"
	"strings"
)

// Config controls the model and reasoning effort used by a Goated Codex runtime.
// Empty fields preserve the Codex CLI's own defaults.
type Config struct {
	Model                string
	ModelReasoningEffort string
}

// OverrideArgs returns Codex -c arguments for the configured values.
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
