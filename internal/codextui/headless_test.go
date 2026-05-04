package codextui

import (
	"reflect"
	"testing"
)

func TestHeadlessArgsLeavePromptPositional(t *testing.T) {
	got := headlessArgs()
	want := []string{
		"exec",
		"--sandbox", "danger-full-access",
		"--dangerously-bypass-approvals-and-sandbox",
		"-c", `model_instructions_file="GOATED.md"`,
	}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("headlessArgs() = %#v, want %#v", got, want)
	}
}
