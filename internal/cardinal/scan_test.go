package cardinal

import "testing"

func TestScanPattern(t *testing.T) {
	tests := []struct {
		prefix string
		want   string
	}{
		{prefix: "user:", want: "user:*"},
		{prefix: "user:*", want: "user:*"},
	}

	for _, test := range tests {
		if got := scanPattern(test.prefix); got != test.want {
			t.Errorf("scanPattern(%q) = %q, want %q", test.prefix, got, test.want)
		}
	}
}
