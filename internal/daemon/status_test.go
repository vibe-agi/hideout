package daemon

import (
	"strings"
	"testing"
)

func TestValidBuildIDMatchesStatusSchema(t *testing.T) {
	valid := strings.Repeat("a", 64)
	for _, test := range []struct {
		value string
		want  bool
	}{
		{value: valid, want: true},
		{value: strings.ToUpper(valid), want: false},
		{value: " " + valid, want: false},
		{value: valid[:63], want: false},
		{value: strings.Repeat("z", 64), want: false},
	} {
		if got := validBuildID(test.value); got != test.want {
			t.Fatalf("validBuildID(%q) = %t, want %t", test.value, got, test.want)
		}
	}
}
