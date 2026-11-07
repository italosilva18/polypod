package tts

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/costa/polypod/internal/humanloop"
	"github.com/costa/polypod/internal/skill"
)

type fakeVoiceSynth struct{ text string }

func (f *fakeVoiceSynth) Synthesize(_ context.Context, text string) ([]byte, error) {
	f.text = text
	return []byte("OggSvoice"), nil
}

type fakeMediaQueue struct {
	chatID, caption, filename, mimeType string
	data                                []byte
}

func (f *fakeMediaQueue) QueueMedia(chatID, caption, filename, mimeType string, data []byte) (humanloop.Message, error) {
	f.chatID, f.caption, f.filename, f.mimeType = chatID, caption, filename, mimeType
	f.data = append([]byte(nil), data...)
	return humanloop.Message{ID: "queued-voice"}, nil
}

func TestWhatsAppVoiceSkillQueuesSynthesizedAudio(t *testing.T) {
	reg := skill.NewRegistry()
	synth := &fakeVoiceSynth{}
	queue := &fakeMediaQueue{}
	if err := RegisterWhatsAppVoiceSkill(reg, queue, "owner@s.whatsapp.net", synth); err != nil {
		t.Fatal(err)
	}
	registered, ok := reg.Get("whatsapp_send_voice")
	if !ok || registered.ExecuteResult == nil {
		t.Fatal("voice skill was not registered")
	}
	result, err := registered.ExecuteResult(context.Background(), map[string]any{"text": "Olá, usuário"})
	if err != nil || result.IsError || !strings.Contains(result.Content, "queued-voice") {
		t.Fatalf("skill result = %+v, %v", result, err)
	}
	if synth.text != "Olá, usuário" || queue.chatID != "owner@s.whatsapp.net" || queue.caption != "" ||
		queue.filename != "voz.ogg" || queue.mimeType != "audio/ogg; codecs=opus" || !bytes.Equal(queue.data, []byte("OggSvoice")) {
		t.Fatalf("unexpected synthesis/queue details: synth=%q queue=%+v", synth.text, queue)
	}
}
