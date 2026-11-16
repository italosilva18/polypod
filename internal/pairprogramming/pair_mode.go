// Package pair_mode — Pair Programming Mode
package pairprogramming

import "strings"

type PairMode struct {
	isActive   bool
	partner    string
	sessionID  string
}

func NewPairMode() *PairMode {
	return &PairMode{}
}

func (p *PairMode) Activate(partner string) {
	p.isActive = true
	p.partner = partner
	p.sessionID = generateSessionID()
}

func (p *PairMode) Deactivate() {
	p.isActive = false
	p.partner = ""
	p.sessionID = ""
}

func (p *PairMode) IsActive() bool {
	return p.isActive
}

func (p *PairMode) GetPartner() string {
	return p.partner
}

func (p *PairMode) FormatStatus() string {
	if !p.isActive {
		return "Pair mode: inactive"
	}
	return fmt.Sprintf("Pair mode: active with %s", p.partner)
}

func generateSessionID() string {
	return fmt.Sprintf("pair_%d", time.Now().UnixNano())
}
