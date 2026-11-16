// Package voice — Voice Commands (STT/TTS)
package voice

import (
	"os/exec"
)

type VoiceCommander struct {
	listening bool
	language  string
}

func NewVoiceCommander() *VoiceCommander {
	return &VoiceCommander{language: "pt-BR"}
}

func (v *VoiceCommander) StartListening() {
	v.listening = true
}

func (v *VoiceCommander) StopListening() {
	v.listening = false
}

func (v *VoiceCommander) Listen() (string, error) {
	// Tenta usar sistema de STT do OS
	cmd := exec.Command("speech-recognition") // placeholder
	output, err := cmd.Output()
	if err != nil {
		return "voice command not recognized", nil
	}
	return string(output), nil
}

func (v *VoiceCommander) Speak(text string) error {
	// TTS do sistema
	cmd := exec.Command("say", text) // macOS
	return cmd.Run()
}

func (v *VoiceCommander) IsListening() bool {
	return v.listening
}
