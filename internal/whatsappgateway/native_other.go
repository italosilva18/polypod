//go:build !linux && !android

package whatsappgateway

import (
	"context"
	"errors"
	"log/slog"
	"time"
)

var errNativeUnsupported = errors.New("whatsapp nativo requer Linux ou Android neste build")

// Native is a non-operational placeholder on platforms without the linked-device driver.
// It keeps the optional gateway from blocking CLI builds on other platforms.
type Native struct {
	SessionDir string
	StatusPath string
	Logger     *slog.Logger
	client     any
	running    bool
}

func (n *Native) Preflight() error { return errNativeUnsupported }

func (n *Native) Send(context.Context, Outbound) error { return errNativeUnsupported }

func (n *Native) Run(context.Context, *Gateway) error { return errNativeUnsupported }

func (n *Native) CleanupMedia(context.Context, *Gateway, time.Time) (int, error) {
	return 0, errNativeUnsupported
}
