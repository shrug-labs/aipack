package util

import "testing"

func TestShellCommandWithOperand(t *testing.T) {
	tests := []struct {
		name              string
		argsBeforeOperand []string
		operand           string
		argsAfterOperand  []string
		want              string
	}{
		{
			name:              "plain operand before trailing flags",
			argsBeforeOperand: []string{"aipack", "pack", "add"},
			operand:           "team-pack",
			argsAfterOperand:  []string{"--profile", "default"},
			want:              "aipack pack add team-pack --profile default",
		},
		{
			name:              "dashed operand after trailing flags and separator",
			argsBeforeOperand: []string{"aipack", "pack", "add"},
			operand:           "-team-pack",
			argsAfterOperand:  []string{"--profile", "default"},
			want:              "aipack pack add --profile default -- -team-pack",
		},
		{
			name:              "quoted operand",
			argsBeforeOperand: []string{"aipack", "search"},
			operand:           "space rule",
			want:              "aipack search 'space rule'",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ShellCommandWithOperand(tt.argsBeforeOperand, tt.operand, tt.argsAfterOperand...); got != tt.want {
				t.Fatalf("ShellCommandWithOperand() = %q, want %q", got, tt.want)
			}
		})
	}
}
