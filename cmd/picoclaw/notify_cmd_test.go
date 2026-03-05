package main

import (
	"strings"
	"testing"
)

func TestParseNotifyRequest_PositionalMessage(t *testing.T) {
	req, err := parseNotifyRequest([]string{"Build", "failed"}, strings.NewReader(""))
	if err != nil {
		t.Fatalf("parseNotifyRequest() error = %v", err)
	}
	if req.Source != "local" {
		t.Fatalf("source = %q, want %q", req.Source, "local")
	}
	if req.Content != "Build failed" {
		t.Fatalf("content = %q, want %q", req.Content, "Build failed")
	}
}

func TestParseNotifyRequest_StdinWithSource(t *testing.T) {
	req, err := parseNotifyRequest([]string{"--stdin", "--source", "opencode"}, strings.NewReader("Deploy complete\n"))
	if err != nil {
		t.Fatalf("parseNotifyRequest() error = %v", err)
	}
	if req.Source != "opencode" {
		t.Fatalf("source = %q, want %q", req.Source, "opencode")
	}
	if req.Content != "Deploy complete" {
		t.Fatalf("content = %q, want %q", req.Content, "Deploy complete")
	}
}

func TestParseNotifyRequest_ExplicitTarget(t *testing.T) {
	req, err := parseNotifyRequest([]string{"--channel", "telegram", "--to", "123", "ping"}, strings.NewReader(""))
	if err != nil {
		t.Fatalf("parseNotifyRequest() error = %v", err)
	}
	if req.Channel != "telegram" || req.ChatID != "123" {
		t.Fatalf("target = %s:%s, want %s:%s", req.Channel, req.ChatID, "telegram", "123")
	}
}

func TestParseNotifyRequest_RejectsPartialTarget(t *testing.T) {
	_, err := parseNotifyRequest([]string{"--channel", "telegram", "ping"}, strings.NewReader(""))
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestParseNotifyRequest_RejectsStdinAndPositional(t *testing.T) {
	_, err := parseNotifyRequest([]string{"--stdin", "ping"}, strings.NewReader("from stdin"))
	if err == nil {
		t.Fatal("expected error")
	}
}
