package routing

import "testing"

func TestIsHeartbeatSessionKey(t *testing.T) {
	t.Parallel()

	cases := []struct {
		in   string
		want bool
	}{
		{"", false},
		{"heartbeat", true},
		{" HEARTBEAT ", true},
		{"heartbeat:", true},
		{"heartbeat:telegram:chat-1", true},
		{"heartbeats", false},
		{"heartbeatx:telegram", false},
	}

	for _, tc := range cases {
		if got := IsHeartbeatSessionKey(tc.in); got != tc.want {
			t.Fatalf("IsHeartbeatSessionKey(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestIsCronSessionKey(t *testing.T) {
	t.Parallel()

	cases := []struct {
		in   string
		want bool
	}{
		{"", false},
		{"cron", true},
		{" CRON ", true},
		{"cron:", true},
		{"cron:jobs", true},
		{"cron-123", true},
		{"crontab", false},
		{"cronjob", false},
	}

	for _, tc := range cases {
		if got := IsCronSessionKey(tc.in); got != tc.want {
			t.Fatalf("IsCronSessionKey(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestIsBackgroundSessionKey(t *testing.T) {
	t.Parallel()

	cases := []struct {
		in   string
		want bool
	}{
		{"", false},
		{"heartbeat", true},
		{"heartbeat:telegram:chat-1", true},
		{"cron", true},
		{"cron:jobs", true},
		{"cron-123", true},
		{"telegram:chat-1", false},
		{"cli:direct", false},
	}

	for _, tc := range cases {
		if got := IsBackgroundSessionKey(tc.in); got != tc.want {
			t.Fatalf("IsBackgroundSessionKey(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}
