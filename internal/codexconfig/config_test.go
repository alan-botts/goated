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

func TestLaunchArgsByMode(t *testing.T) {
	config := Config{
		Model: "gpt-6-sol", ModelReasoningEffort: "medium",
		Args:       []string{"--enable", "foo"},
		ExecArgs:   []string{"--skip-git-repo-check"},
		ResumeArgs: []string{"--all"},
		TUIArgs:    []string{"--no-alt-screen"},
	}
	cases := []struct {
		mode string
		last []string
	}{
		{"exec", []string{"--skip-git-repo-check"}},
		{"resume", []string{"--all"}},
		{"tui", []string{"--no-alt-screen"}},
	}
	for _, tc := range cases {
		t.Run(tc.mode, func(t *testing.T) {
			want := append([]string{"-c", `model="gpt-6-sol"`, "-c", `model_reasoning_effort="medium"`, "--enable", "foo"}, tc.last...)
			if got := config.LaunchArgs(tc.mode); !reflect.DeepEqual(got, want) {
				t.Fatalf("LaunchArgs(%q) = %#v, want %#v", tc.mode, got, want)
			}
		})
	}
}
