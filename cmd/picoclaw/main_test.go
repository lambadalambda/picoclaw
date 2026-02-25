package main

import "testing"

func TestHeartbeatSuppressesDelivery(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  bool
	}{
		{name: "exact token", input: "HEARTBEAT_OK", want: true},
		{name: "mixed with text", input: "all good\n\nHEARTBEAT_OK", want: true},
		{name: "lowercase token", input: "heartbeat_ok", want: true},
		{name: "normal message", input: "Everything looks healthy", want: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := heartbeatSuppressesDelivery(tc.input)
			if got != tc.want {
				t.Fatalf("heartbeatSuppressesDelivery(%q) = %v, want %v", tc.input, got, tc.want)
			}
		})
	}
}
