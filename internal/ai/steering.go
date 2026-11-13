package ai

import (
	"context"
	"strings"
)

type steeringContextKey struct{}
type steeringObserverKey struct{}

// WithSteering supplies optional user guidance to the next model boundary.
// The channel is scoped to a single interactive turn and must be buffered.
func WithSteering(ctx context.Context, guidance <-chan string) context.Context {
	return context.WithValue(ctx, steeringContextKey{}, guidance)
}

// WithSteeringObserver records guidance for the conversation transcript.
func WithSteeringObserver(ctx context.Context, observe func(string)) context.Context {
	return context.WithValue(ctx, steeringObserverKey{}, observe)
}

func takeSteering(ctx context.Context) []string {
	guidance, ok := ctx.Value(steeringContextKey{}).(<-chan string)
	if !ok || guidance == nil {
		return nil
	}
	var messages []string
	for len(messages) < 8 {
		select {
		case message, open := <-guidance:
			if !open {
				return messages
			}
			if message = strings.TrimSpace(message); message != "" {
				messages = append(messages, message)
				if observe, ok := ctx.Value(steeringObserverKey{}).(func(string)); ok && observe != nil {
					observe(message)
				}
			}
		default:
			return messages
		}
	}
	return messages
}
