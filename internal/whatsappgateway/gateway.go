// Package whatsappgateway connects the existing durable human loop to WhatsApp.
// It does not run an agent, grant tools permissions, or open sessions at construction.
package whatsappgateway

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/costa/polypod/internal/humanloop"
)

const Channel = "whatsapp"

// Event is stored as a JSON envelope in the inbox, preserving transport provenance.
type Event struct {
	ID        string `json:"id"`
	ChatID    string `json:"chat_id"`
	SenderID  string `json:"sender_id"`
	Text      string `json:"text"`
	ReplyTo   string `json:"reply_to,omitempty"`
	MediaPath string `json:"media_path,omitempty"`
	MIME      string `json:"mime,omitempty"`
	FromMe    bool   `json:"from_me,omitempty"`
}

type Outbound struct {
	ChatID, Text, ID, ReplyTo, Participant, QuotedText string
	Media                                              *OutboundMedia
}

// Driver must return only after the server acknowledges Send. A stable ID is
// reused on retry; this is not a guarantee of end-to-end exactly-once delivery.
type Driver interface {
	Send(context.Context, Outbound) error
}

type Gateway struct {
	store          *humanloop.Store
	driver         Driver
	chats, senders map[string]bool
	wake           func()
	questions      *QuestionRelay
}

func New(store *humanloop.Store, driver Driver, chats, senders []string, wake func()) (*Gateway, error) {
	if store == nil || driver == nil || len(chats) == 0 || len(senders) == 0 {
		return nil, errors.New("whatsapp: store, driver e listas de chats/remetentes sao obrigatorios")
	}
	g := &Gateway{store: store, driver: driver, chats: map[string]bool{}, senders: map[string]bool{}, wake: wake}
	for _, s := range chats {
		if strings.TrimSpace(s) == "" {
			return nil, errors.New("whatsapp: chat vazio")
		}
		g.chats[s] = true
	}
	for _, s := range senders {
		if strings.TrimSpace(s) == "" {
			return nil, errors.New("whatsapp: remetente vazio")
		}
		g.senders[s] = true
	}
	return g, nil
}

func (g *Gateway) Allowed(chat, sender string) bool { return g.chats[chat] && g.senders[sender] }

// SetQuestionRelay is called during startup, before the native receive loop.
func (g *Gateway) SetQuestionRelay(relay *QuestionRelay) { g.questions = relay }

// ignoreReason checks identities before the native driver downloads media. A
// message from another linked device is valid input when both identities are
// allowlisted, unless its ID belongs to a durable Polypod outbound message.
func (g *Gateway) ignoreReason(e Event) string {
	if !g.Allowed(e.ChatID, e.SenderID) {
		return "unauthorized"
	}
	if !e.FromMe || e.ID == "" {
		return ""
	}
	_, messages := g.store.Snapshot()
	for _, msg := range messages {
		if msg.Direction == humanloop.DirectionOutbound && msg.Channel == Channel && msg.UserID == e.ChatID &&
			strings.EqualFold(outboundID(msg.ID), e.ID) {
			return "outbound_echo"
		}
	}
	return ""
}

func outboundID(messageID string) string {
	sum := sha256.Sum256([]byte("polypod-whatsapp:" + messageID))
	return strings.ToUpper(hex.EncodeToString(sum[:16]))
}

// Receive must run before transport acknowledgement. Replays are deduplicated
// by chat plus message ID, including across process restarts.
func (g *Gateway) Receive(ctx context.Context, e Event) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if g.ignoreReason(e) != "" {
		return nil
	}
	if e.ID == "" {
		return errors.New("whatsapp: evento sem ID")
	}
	if g.questions != nil {
		consumed, err := g.questions.Consume(ctx, e)
		if err != nil || consumed {
			return err
		}
	}
	if strings.TrimSpace(e.Text) == "" && e.MediaPath == "" {
		return errors.New("whatsapp: evento sem conteudo")
	}
	raw, err := json.Marshal(e)
	if err != nil {
		return err
	}
	_, err = g.store.EnqueueInboundOnce(Channel, e.ChatID, string(raw), e.ChatID+":"+e.ID)
	if err != nil {
		return fmt.Errorf("whatsapp: persistindo entrada: %w", err)
	}
	if g.wake != nil {
		g.wake()
	}
	return nil
}

// Decode gives the agent adapter text and metadata without flattening identities.
func Decode(msg humanloop.Message) (Event, error) {
	var e Event
	if msg.Channel != Channel {
		return e, errors.New("whatsapp: canal incorreto")
	}
	err := json.Unmarshal([]byte(msg.Text), &e)
	return e, err
}

func (g *Gateway) Deliver(ctx context.Context, msg humanloop.Message) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if msg.Channel != Channel || msg.Direction != humanloop.DirectionOutbound || !g.chats[msg.UserID] || msg.ID == "" {
		return errors.New("whatsapp: destino ou mensagem nao autorizado")
	}
	out := Outbound{ChatID: msg.UserID, Text: msg.Text, ID: outboundID(msg.ID)}
	if msg.Media != nil {
		data, err := g.store.ReadOutboundMedia(msg)
		if err != nil {
			return fmt.Errorf("whatsapp: lendo midia enfileirada: %w", err)
		}
		validated, err := validateOutboundMedia(msg.Text, msg.Media.Filename, msg.Media.MIME, data)
		if err != nil || validated.Kind != msg.Media.Kind {
			return errors.New("whatsapp: midia enfileirada invalida")
		}
		out.Media = &validated
	}
	if msg.ReplyTo != "" {
		_, messages := g.store.Snapshot()
		for _, in := range messages {
			if in.ID == msg.ReplyTo && in.Channel == Channel && in.UserID == msg.UserID && in.Direction == humanloop.DirectionInbound {
				e, err := Decode(in)
				if err != nil {
					return err
				}
				out.ReplyTo = e.ID
				out.Participant = e.SenderID
				out.QuotedText = e.Text
				break
			}
		}
	}
	return g.driver.Send(ctx, out)
}
