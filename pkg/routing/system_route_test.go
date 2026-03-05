package routing

import "testing"

func TestEncodeDecodeSystemRoute_RoundTrip(t *testing.T) {
	t.Parallel()

	route := EncodeSystemRoute("telegram", "chat1")
	if route != "telegram:chat1" {
		t.Fatalf("EncodeSystemRoute() = %q, want %q", route, "telegram:chat1")
	}

	ch, chatID, ok := DecodeSystemRoute(route)
	if !ok {
		t.Fatalf("DecodeSystemRoute(%q) ok=false, want ok=true", route)
	}
	if ch != "telegram" {
		t.Fatalf("DecodeSystemRoute(%q) channel=%q, want %q", route, ch, "telegram")
	}
	if chatID != "chat1" {
		t.Fatalf("DecodeSystemRoute(%q) chat_id=%q, want %q", route, chatID, "chat1")
	}
}

func TestDecodeSystemRoute_ChatIDContainsColons(t *testing.T) {
	t.Parallel()

	route := "heartbeat:telegram:chat:1"
	ch, chatID, ok := DecodeSystemRoute(route)
	if !ok {
		t.Fatalf("DecodeSystemRoute(%q) ok=false, want ok=true", route)
	}
	if ch != "heartbeat" {
		t.Fatalf("channel=%q, want %q", ch, "heartbeat")
	}
	if chatID != "telegram:chat:1" {
		t.Fatalf("chat_id=%q, want %q", chatID, "telegram:chat:1")
	}
}

func TestDecodeSystemRoute_NoSeparator(t *testing.T) {
	t.Parallel()

	route := "direct"
	ch, chatID, ok := DecodeSystemRoute(route)
	if ok {
		t.Fatalf("DecodeSystemRoute(%q) ok=true, want ok=false", route)
	}
	if ch != "" {
		t.Fatalf("channel=%q, want empty", ch)
	}
	if chatID != "direct" {
		t.Fatalf("chat_id=%q, want %q", chatID, "direct")
	}
}

func TestDecodeSystemRoute_EmptyRightSide(t *testing.T) {
	t.Parallel()

	route := "telegram:"
	ch, chatID, ok := DecodeSystemRoute(route)
	if ok {
		t.Fatalf("DecodeSystemRoute(%q) ok=true, want ok=false", route)
	}
	if ch != "" {
		t.Fatalf("channel=%q, want empty", ch)
	}
	if chatID != route {
		t.Fatalf("chat_id=%q, want %q", chatID, route)
	}
}
