package codexconfig

import (
	"reflect"
	"testing"
)

func TestOverrideArgs(t *testing.T) {
	got := (Config{Model: "gpt-6-sol", ModelReasoningEffort: "medium"}).OverrideArgs()
	want := []string{"-c", `model="gpt-6-sol"`, "-c", `model_reasoning_effort="medium"`}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("OverrideArgs() = %#v, want %#v", got, want)
	}
}
