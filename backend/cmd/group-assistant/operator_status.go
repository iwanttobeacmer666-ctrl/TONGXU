package main

import "time"

type localServiceStatus struct {
	Service, Platform, Agent string
	Started, ConnectedAt     time.Time
	UpdatePaused             bool
}

func (m *operatorManager) serviceStatus() *localServiceStatus {
	out := &localServiceStatus{Service: "offline", Platform: "stopped", Agent: "unchecked", Started: m.started}
	if m.service != nil {
		out.UpdatePaused = m.service.UpdatePaused()
		out.Service = "running"
		if m.stopping {
			out.Service = "stopping"
		}
		out.Agent = "unavailable"
		if m.service.AgentReady() {
			out.Agent = "ready"
		}
		out.Platform = "unknown"
		if m.platform != nil {
			out.Platform, out.ConnectedAt = m.platform.ConnectionStatus()
		}
	}
	return out
}
