//go:build !linux

package serialhardware

import "context"

type platformTransport struct{}

func baudConstant(int) (uint32, error) { return 0, ErrUnsupported }

func (platformTransport) Exchange(context.Context, SerialConfig, []byte, int) (ExchangeResult, error) {
	return ExchangeResult{}, ErrUnsupported
}
