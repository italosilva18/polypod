package cli

import (
	"context"
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// beginVoiceCapture keeps recording cancellation separate from the model's
// lifetime. Stopping the microphone must not cancel the later transcription.
func (m *model) beginVoiceCapture(duration time.Duration, announce bool) (model, tea.Cmd) {
	if m.voiceRecording {
		m.messages = append(m.messages, chatEntry{role: "system", content: "Uma gravação já está em andamento.", timestamp: time.Now()})
		m.updateViewport()
		return *m, nil
	}
	if m.cmdDeps.captureVoice == nil {
		m.messages = append(m.messages, chatEntry{role: "error", content: "Entrada por voz indisponível: configure whatsapp.audio_transcription.", timestamp: time.Now()})
		m.updateViewport()
		return *m, nil
	}
	parent := m.ctx
	if parent == nil {
		parent = context.Background()
	}
	recordCtx, cancel := context.WithCancel(parent)
	m.voiceCancel = cancel
	m.voiceRecording = true
	m.voiceStopping = false
	m.voicePartialText = ""
	partials := make(chan string, 1)
	m.voicePartials = partials
	if announce {
		message := "Gravando. Pressione Ctrl+G novamente ou digite /voice stop para transcrever."
		if duration > 0 {
			message = fmt.Sprintf("Gravando por %d segundo(s). Ctrl+G pode parar antes.", int(duration/time.Second))
		}
		m.messages = append(m.messages, chatEntry{role: "system", content: message, timestamp: time.Now()})
	}
	m.updateViewport()
	capture := m.cmdDeps.captureVoice
	final := func() tea.Msg {
		defer close(partials)
		text, err := capture(recordCtx, parent, duration, func(text string) {
			select {
			case partials <- text:
			default:
				select {
				case <-partials:
				default:
				}
				select {
				case partials <- text:
				default:
				}
			}
		})
		return voiceResultMsg{text: text, err: err}
	}
	return *m, tea.Batch(final, waitForVoicePartial(partials))
}

func waitForVoicePartial(partials <-chan string) tea.Cmd {
	if partials == nil {
		return nil
	}
	return func() tea.Msg {
		text, ok := <-partials
		return voicePartialMsg{text: text, ok: ok, source: partials}
	}
}

func (m *model) stopVoiceCapture() (model, tea.Cmd) {
	if !m.voiceRecording {
		m.messages = append(m.messages, chatEntry{role: "system", content: "Nenhuma gravação em andamento.", timestamp: time.Now()})
		m.updateViewport()
		return *m, nil
	}
	if m.voiceStopping {
		return *m, nil
	}
	m.voiceStopping = true
	if m.voiceCancel != nil {
		m.voiceCancel()
		m.voiceCancel = nil
	}
	m.messages = append(m.messages, chatEntry{role: "system", content: "Gravação encerrada. Transcrevendo áudio...", timestamp: time.Now()})
	m.updateViewport()
	return *m, nil
}
