package main

import "time"

type localServiceStatus struct {
	Service, Platform, Agent string
	Started, ConnectedAt     time.Time
}
