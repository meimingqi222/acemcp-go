package state

import "sync/atomic"

type Status string

const (
	StatusStarting Status = "starting"
	StatusReady    Status = "ready"
	StatusStopping Status = "stopping"
)

// State holds in-memory minimal status for health/info endpoints.
type State struct {
	started atomic.Bool
	status  atomic.Value // stores Status
}

func New() *State {
	s := &State{}
	s.status.Store(StatusStarting)
	return s
}

func (s *State) IsStarted() bool {
	return s.started.Load()
}

func (s *State) SetReady() {
	s.started.Store(true)
	s.status.Store(StatusReady)
}

func (s *State) SetStopping() {
	s.status.Store(StatusStopping)
}

func (s *State) Status() Status {
	v := s.status.Load()
	if v == nil {
		return StatusStarting
	}
	return v.(Status)
}
