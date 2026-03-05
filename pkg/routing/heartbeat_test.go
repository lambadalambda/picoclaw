package routing

import "testing"

func TestHeartbeatTargetRouteFromSessionKey(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		sessionKey string
		wantRoute  string
		wantOK     bool
	}{
		{name: "root", sessionKey: "heartbeat", wantRoute: "", wantOK: false},
		{name: "scoped", sessionKey: "heartbeat:telegram:chat1", wantRoute: "telegram:chat1", wantOK: true},
		{name: "scoped preserves remainder", sessionKey: "HEARTBEAT:TeLeGrAm:chat:1", wantRoute: "TeLeGrAm:chat:1", wantOK: true},
		{name: "empty suffix", sessionKey: "heartbeat:", wantRoute: "", wantOK: false},
		{name: "not heartbeat", sessionKey: "telegram:chat1", wantRoute: "", wantOK: false},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			route, ok := HeartbeatTargetRouteFromSessionKey(tc.sessionKey)
			if ok != tc.wantOK {
				t.Fatalf("ok=%v, want %v", ok, tc.wantOK)
			}
			if route != tc.wantRoute {
				t.Fatalf("route=%q, want %q", route, tc.wantRoute)
			}
		})
	}
}

func TestEncodeHeartbeatSessionKey(t *testing.T) {
	t.Parallel()

	got := EncodeHeartbeatSessionKey("telegram", "chat1")
	if got != "heartbeat:telegram:chat1" {
		t.Fatalf("EncodeHeartbeatSessionKey() = %q, want %q", got, "heartbeat:telegram:chat1")
	}
}
