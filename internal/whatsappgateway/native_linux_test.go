package whatsappgateway

import (
	"context"
	"errors"
	"testing"

	"github.com/costa/polypod/internal/humanloop"
	"go.mau.fi/whatsmeow/types"
)

func TestDisconnectedNativeSendDefersWithoutSpendingAttempts(t *testing.T) {
	var native Native
	err := native.Send(context.Background(), Outbound{ChatID: "12345@s.whatsapp.net", Text: "test"})
	if !errors.Is(err, humanloop.ErrRemoteUnavailable) {
		t.Fatalf("disconnected send must signal retryable offline state: %v", err)
	}
}

func TestSenderForEventNormalizesOnlyOwnDevice(t *testing.T) {
	device := types.JID{User: "12345", Server: types.DefaultUserServer, Device: 3}
	bare := device.ToNonAD().String()
	if got := senderForEvent(types.MessageSource{Sender: device, IsFromMe: true}); got != bare {
		t.Fatalf("own sender = %q, want %q", got, bare)
	}
	if got := senderForEvent(types.MessageSource{Sender: device}); got != device.String() {
		t.Fatalf("other sender = %q, want %q", got, device.String())
	}
}
