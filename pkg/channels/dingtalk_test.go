package channels

import (
	"context"
	"testing"

	"github.com/sipeed/picoclaw/pkg/bus"
)

func TestDingTalk_OnChatBotMessageReceived_NilPayload_NoPanic(t *testing.T) {
	mb := bus.NewMessageBus()
	defer mb.Close()

	c := &DingTalkChannel{
		BaseChannel: NewBaseChannel("dingtalk", nil, mb, nil),
	}

	didPanic := false
	var gotErr error
	func() {
		defer func() {
			if recover() != nil {
				didPanic = true
			}
		}()
		_, gotErr = c.onChatBotMessageReceived(context.Background(), nil)
	}()

	if didPanic {
		t.Fatal("onChatBotMessageReceived should not panic on nil payload")
	}
	if gotErr != nil {
		t.Fatalf("unexpected error: %v", gotErr)
	}
}
