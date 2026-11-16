// Package voice — Voice Processing extracted from Claude Code
// Sources: src/services/voice.ts, src/services/voiceStreamSTT.ts, src/services/voiceKeyterms.ts

package voice

import (
	"fmt"
	"strings"
)

// VoiceConfig holds voice configuration
type VoiceConfig struct {
	Enabled      bool     `json:"enabled"`
	STTProvider  string   `json:"stt_provider"`
	TTSProvider  string   `json:"tts_provider"`
	Language     string   `json:"language"`
	Keywords     []string `json:"keywords"`
}

// SpeechResult holds speech-to-text result
type SpeechResult struct {
	Text       string  `json:"text"`
	Confidence float64 `json:"confidence"`
	Language   string  `json:"language"`
	Duration   float64 `json:"duration"`
}

// TextToSpeechResult holds text-to-speech result
type TextToSpeechResult struct {
	AudioPath  string  `json:"audio_path"`
	Duration   float64 `json:"duration"`
	Format     string  `json:"format"`
}

// VoiceProcessor processes voice input/output
type VoiceProcessor struct {
	config VoiceConfig
}

// NewVoiceProcessor creates new voice processor
func NewVoiceProcessor(config VoiceConfig) *VoiceProcessor {
	return &VoiceProcessor{config: config}
}

// RecognizeSpeech recognizes speech from audio
func (p *VoiceProcessor) RecognizeSpeech(audioPath string) (*SpeechResult, error) {
	// Placeholder - full implementation needs STT integration
	return &SpeechResult{
		Text:       "Recognized text from audio",
		Confidence: 0.95,
		Language:   p.config.Language,
	}, nil
}

// SynthesizeSpeech converts text to speech
func (p *VoiceProcessor) SynthesizeSpeech(text string) (*TextToSpeechResult, error) {
	// Placeholder - full implementation needs TTS integration
	return &TextToSpeechResult{
		AudioPath: "/tmp/speech.mp3",
		Duration:  2.5,
		Format:    "mp3",
	}, nil
}

// ExtractKeywords extracts keywords from text
func ExtractKeywords(text string) []string {
	// Simple keyword extraction
	words := strings.Fields(text)
	keywords := make([]string, 0)
	
	// Filter common words
	stopWords := map[string]bool{
		"the": true, "a": true, "an": true, "and": true, "or": true,
		"but": true, "in": true, "on": true, "at": true, "to": true,
		"for": true, "of": true, "with": true, "by": true,
	}
	
	for _, word := range words {
		word = strings.ToLower(word)
		if len(word) > 3 && !stopWords[word] {
			keywords = append(keywords, word)
		}
	}
	
	return keywords
}

// FormatSpeechResult formats speech result for display
func FormatSpeechResult(result *SpeechResult) string {
	return fmt.Sprintf("🎤 **Speech Recognized**:\n\n%s\n\nConfidence: %.0f%%", 
		result.Text, result.Confidence*100)
}
