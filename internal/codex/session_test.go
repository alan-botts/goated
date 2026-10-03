package codex

import (
	"reflect"
	"testing"

	"goated/internal/codexconfig"
)

func TestSessionPromptArgsFreshUsesConfiguredModel(t *testing.T) {
	r := NewSessionRuntime("/tmp/workspace", "/tmp/logs", codexconfig.Config{Model: "gpt-6-sol", ModelReasoningEffort: "medium"})
	got := r.promptArgs("", "hello")
	want := []string{"exec", "--json", "--sandbox", "danger-full-access", "--dangerously-bypass-approvals-and-sandbox", "-c", `model_instructions_file="GOATED.md"`, "-c", `model="gpt-6-sol"`, "-c", `model_reasoning_effort="medium"`, "hello"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("promptArgs() = %#v, want %#v", got, want)
	}
}
