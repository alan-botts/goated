package codex

import (
	"reflect"
	"testing"
)

func TestSessionPromptArgsFreshUsesPositionalPrompt(t *testing.T) {
	r := NewSessionRuntime("/tmp/workspace", "/tmp/logs")

	got := r.promptArgs("", "hello")
	want := []string{
		"exec",
		"--json",
		"--sandbox", "danger-full-access",
		"--dangerously-bypass-approvals-and-sandbox",
		"-c", `model_instructions_file="GOATED.md"`,
		"hello",
	}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("promptArgs(\"\") = %#v, want %#v", got, want)
	}
}

func TestSessionPromptArgsResumeUsesPositionalPromptAfterThreadID(t *testing.T) {
	r := NewSessionRuntime("/tmp/workspace", "/tmp/logs")

	got := r.promptArgs("thread-123", "hello")
	want := []string{
		"exec", "resume",
		"--json",
		"--dangerously-bypass-approvals-and-sandbox",
		"-c", `model_instructions_file="GOATED.md"`,
		"thread-123", "hello",
	}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("promptArgs(thread) = %#v, want %#v", got, want)
	}
}
