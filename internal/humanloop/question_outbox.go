package humanloop

import (
	"errors"
	"time"
)

// EnqueueQuestionOutboundOnce records a question before its synchronous send.
// DeliveryOwner excludes it from the generic worker even after deferUntil.
// This prevents stale questions after long downtime and concurrent sends.
// A retry of the originating turn reuses the same transport identity.
func (s *Store) EnqueueQuestionOutboundOnce(channel, userID, text, externalID string, deferUntil time.Time) (Message, error) {
	if channel == "" || userID == "" || text == "" || externalID == "" || !deferUntil.After(time.Now()) {
		return Message{}, errors.New("humanloop: pergunta requer canal, destino, texto, ID e prazo futuro")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	key := dedupKey(DirectionOutbound, channel, externalID)
	if id, exists := s.dedup[key]; exists {
		msg, err := s.findLocked(id)
		if err != nil {
			return Message{}, errors.New("humanloop: pergunta antiga sem registro recuperavel")
		}
		if msg.UserID != userID || msg.Text != text || msg.Channel != channel ||
			msg.Direction != DirectionOutbound || msg.DeliveryOwner != "whatsapp_ask_user" {
			return Message{}, errors.New("humanloop: ID de pergunta ja pertence a outra saida")
		}
		if msg.Status == StatusFailed && len(msg.Delivered) == 0 {
			previous := *msg
			msg.Status, msg.Attempts, msg.Error = StatusPending, 1, ""
			msg.UpdatedAt, msg.NextAttemptAt = time.Now(), deferUntil
			msg.CompletedAt, msg.ProcessingAt = nil, nil
			if err := s.saveLocked(); err != nil {
				*msg = previous
				return Message{}, err
			}
		}
		return *msg, nil
	}
	now := time.Now()
	msg := Message{ID: newID(), ExternalID: externalID, Direction: DirectionOutbound,
		DeliveryOwner: "whatsapp_ask_user",
		Status:        StatusPending, Channel: channel, UserID: userID, Text: text,
		Attempts: 1, CreatedAt: now, UpdatedAt: now, NextAttemptAt: deferUntil}
	s.messages = append(s.messages, msg)
	s.rememberDedupLocked(key, msg.ID)
	if err := s.saveLocked(); err != nil {
		s.messages = s.messages[:len(s.messages)-1]
		s.forgetDedupLocked(key)
		return Message{}, err
	}
	return msg, nil
}
