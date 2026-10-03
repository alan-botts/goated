package codex

import (
	"reflect"
	"testing"

	"goated/internal/codexconfig"
)

func TestHeadlessArgsIncludeConfiguredModel(t *testing.T) {
	got := headlessArgs(codexconfig.Config{Model: "gpt-6-sol", ModelReasoningEffort: "medium"})
	want := []string{"exec", "--sandbox", "danger-full-access", "--dangerously-bypass-approvals-and-sandbox", "-c", `model_instructions_file="GOATED.md"`, "-c", `model="gpt-6-sol"`, "-c", `model_reasoning_effort="medium"`}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("headlessArgs() = %#v, want %#v", got, want)
	}
}
