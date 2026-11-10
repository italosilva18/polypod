package scheduler

import "fmt"

// editLease prevents a definition edit from racing with a live attempt or an
// edit in another process. The refresh is performed while the lease is held,
// so validation always sees the latest committed definition.
func (s *Scheduler) editLease(id string) (*fileLease, error) {
	lease, acquired, err := s.taskLease(id)
	if err != nil {
		return nil, err
	}
	if !acquired {
		return nil, fmt.Errorf("rotina %s em execucao ou edicao em outro processo", id)
	}
	if err := s.refresh(); err != nil {
		if closeErr := lease.Close(); closeErr != nil {
			s.logger.Warn("falha ao soltar lock de rotina", "id", id, "error", closeErr)
		}
		return nil, err
	}
	return lease, nil
}
