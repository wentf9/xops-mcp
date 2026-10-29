package xops

import (
	"log/slog"
	"slices"
	"sync"

	corelog "github.com/wentf9/xops-cli/core/log"
)

const maxCollectorEvents = 64

// DiagnosticEvent represents a public, redacted protocol lifecycle event.
type DiagnosticEvent struct {
	Kind                   string   `json:"kind"`
	Level                  int      `json:"level,omitempty"`
	NodeID                 string   `json:"nodeID,omitempty"`
	Host                   string   `json:"host,omitempty"`
	Port                   int      `json:"port,omitempty"`
	Proxied                bool     `json:"proxied,omitempty"`
	Accepted               bool     `json:"accepted,omitempty"`
	ServerVersion          string   `json:"serverVersion,omitempty"`
	KeyExchange            string   `json:"keyExchange,omitempty"`
	HostKey                string   `json:"hostKey,omitempty"`
	Fingerprint            string   `json:"fingerprint,omitempty"`
	ReadCipher             string   `json:"readCipher,omitempty"`
	WriteCipher            string   `json:"writeCipher,omitempty"`
	ReadMAC                string   `json:"readMAC,omitempty"`
	WriteMAC               string   `json:"writeMAC,omitempty"`
	Method                 string   `json:"method,omitempty"`
	Methods                []string `json:"methods,omitempty"`
	Attempt                int      `json:"attempt,omitempty"`
	Operation              string   `json:"operation,omitempty"`
	Outcome                string   `json:"outcome,omitempty"`
	PrivilegeCommand       string   `json:"privilegeCommand,omitempty"`
	PrivilegePrompt        string   `json:"privilegePrompt,omitempty"`
	PrivilegePromptPattern string   `json:"privilegePromptPattern,omitempty"`
	ExitCode               *int64   `json:"exitCode,omitempty"`
}

// DiagnosticCollector captures bounded diagnostic events during connection testing and probing.
type DiagnosticCollector struct {
	mu     sync.Mutex
	events []DiagnosticEvent
}

// NewDiagnosticCollector creates a collector with memory preallocated for diagnostic events.
func NewDiagnosticCollector() *DiagnosticCollector {
	return &DiagnosticCollector{
		events: make([]DiagnosticEvent, 0, 16),
	}
}

// DiagnosticLevel indicates interest in DEBUG1 (1) and DEBUG2 (2) events.
func (c *DiagnosticCollector) DiagnosticLevel() int {
	return 2
}

// ObserveDiagnostic safely appends events up to maxCollectorEvents.
func (c *DiagnosticCollector) ObserveDiagnostic(d corelog.Diagnostic) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.events) >= maxCollectorEvents {
		return
	}
	event := DiagnosticEvent{
		Kind:                   string(d.Kind),
		Level:                  d.Level,
		NodeID:                 d.NodeID,
		Host:                   d.Host,
		Port:                   d.Port,
		Proxied:                d.Proxied,
		Accepted:               d.Accepted,
		ServerVersion:          d.ServerVersion,
		KeyExchange:            d.KeyExchange,
		HostKey:                d.HostKey,
		Fingerprint:            d.Fingerprint,
		ReadCipher:             d.ReadCipher,
		WriteCipher:            d.WriteCipher,
		ReadMAC:                d.ReadMAC,
		WriteMAC:               d.WriteMAC,
		Method:                 d.Method,
		Methods:                slices.Clone(d.Methods),
		Attempt:                d.Attempt,
		Operation:              d.Operation,
		Outcome:                d.Outcome,
		PrivilegeCommand:       d.PrivilegeCommand,
		PrivilegePrompt:        d.PrivilegePrompt,
		PrivilegePromptPattern: d.PrivilegePromptPattern,
	}
	if d.HasExitCode {
		code := d.ExitCode
		event.ExitCode = &code
	}
	c.events = append(c.events, event)
}

// Events returns a copy of the captured diagnostic events.
func (c *DiagnosticCollector) Events() []DiagnosticEvent {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.events) == 0 {
		return nil
	}
	out := make([]DiagnosticEvent, len(c.events))
	for i, ev := range c.events {
		ev.Methods = slices.Clone(ev.Methods)
		if ev.ExitCode != nil {
			code := *ev.ExitCode
			ev.ExitCode = &code
		}
		out[i] = ev
	}
	return out
}

// SlogDiagnosticObserver logs sanitized protocol events to slog.Debug.
type SlogDiagnosticObserver struct {
	Level int
}

// DiagnosticLevel returns the configured verbosity level (default 1).
func (o SlogDiagnosticObserver) DiagnosticLevel() int {
	if o.Level <= 0 {
		return 1
	}
	return o.Level
}

// ObserveDiagnostic streams sanitized protocol events to slog.
func (o SlogDiagnosticObserver) ObserveDiagnostic(d corelog.Diagnostic) {
	slog.Debug("ssh diagnostic",
		"kind", string(d.Kind),
		"level", d.Level,
		"nodeID", d.NodeID,
		"host", d.Host,
		"port", d.Port,
		"proxied", d.Proxied,
		"operation", d.Operation,
		"outcome", d.Outcome,
	)
}
