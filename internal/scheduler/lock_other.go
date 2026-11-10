//go:build !linux && !darwin && !freebsd && !openbsd && !netbsd && !dragonfly

package scheduler

import "fmt"

// The scheduler refuses to run without an inter-process lock implementation.
type fileLease struct{}

func (*fileLease) Close() error { return nil }

func acquireFileLease(string, bool) (*fileLease, bool, error) {
	return nil, false, fmt.Errorf("coordenação do agendador indisponível nesta plataforma")
}

func (s *Scheduler) stateLease() (*fileLease, error) {
	lease, _, err := acquireFileLease(s.dataFile+".state.lock", false)
	return lease, err
}

func (s *Scheduler) taskLease(id string) (*fileLease, bool, error) {
	return acquireFileLease(s.dataFile+".run."+id+".lock", true)
}
