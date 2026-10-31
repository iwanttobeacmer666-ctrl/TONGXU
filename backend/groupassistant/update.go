package groupassistant

// PauseForUpdate fences new work and media downloads under the same lock used
// for admission. Already reserved downloads and approval responses can finish.
func (s *Service) PauseForUpdate(paused bool) {
	s.accessMu.Lock()
	defer s.accessMu.Unlock()
	s.updatePaused = paused
}

func (s *Service) UpdatePaused() bool {
	s.accessMu.RLock()
	defer s.accessMu.RUnlock()
	return s.updatePaused
}

func (s *Service) UpdateReady() bool {
	s.accessMu.Lock()
	defer s.accessMu.Unlock()
	s.tasksMu.Lock()
	defer s.tasksMu.Unlock()
	for _, tasks := range s.revocations {
		for _, task := range tasks {
			select {
			case <-task.done:
				if task.cleanupErr != nil {
					return false
				}
			default:
				return false
			}
		}
	}
	return s.updatePaused && len(s.tasks) == 0 && len(s.inspectGate) == 0
}
