// Package utils — Utilities (voice + notifications + autosave)
package utils

type VoiceCommander struct{ isListening bool }
type Notifier struct{}
type AutoSaver struct{}

func NewVoiceCommander() *VoiceCommander { return &VoiceCommander{} }
func NewNotifier() *Notifier { return &Notifier{} }
func NewAutoSaver() *AutoSaver { return &AutoSaver{} }

func (v *VoiceCommander) Listen() { v.isListening = true }
func (n *Notifier) Notify(msg string) {}
func (a *AutoSaver) Save(data string) {}
