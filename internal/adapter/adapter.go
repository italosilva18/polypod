package adapter

import "context"

// MessageHandler processes an incoming message and returns a response.
type MessageHandler func(ctx context.Context, msg InMessage) (OutMessage, error)

// StreamChunk represents a single piece of a streamed response.
type StreamChunk struct {
	Delta string
	Done  bool
	Error error
}

// StreamHandler processes an incoming message and streams the response via chunks channel.
type StreamHandler func(ctx context.Context, msg InMessage, chunks chan<- StreamChunk)

// Channel is the interface every adapter (Telegram, WhatsApp, REST) must implement.
type Channel interface {
	// Name returns the channel identifier (e.g. "telegram", "whatsapp", "rest").
	Name() string

	// Start begins listening for messages. It calls handler for each incoming message.
	// It blocks until ctx is cancelled.
	Start(ctx context.Context, handler MessageHandler) error

	// Send delivers a message through this channel.
	Send(ctx context.Context, msg OutMessage) error
}
