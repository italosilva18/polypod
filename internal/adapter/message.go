package adapter

import "time"

// InMessage is a unified incoming message from any channel.
type InMessage struct {
	ID        string
	Channel   string
	UserID    string
	UserName  string
	Text      string
	Timestamp time.Time
	Metadata  map[string]string
}

// OutMessage is a unified outgoing message to any channel.
type OutMessage struct {
	Channel  string
	UserID   string
	Text     string
	ReplyTo  string
	Metadata map[string]string
}
