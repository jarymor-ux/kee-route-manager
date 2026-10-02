package event

import "time"

type Event struct {
	Sequence    uint64         `json:"sequence"`
	Timestamp   time.Time      `json:"timestamp"`
	Level       string         `json:"level"`
	Type        string         `json:"type"`
	Message     string         `json:"message"`
	OperationID string         `json:"operation_id,omitempty"`
	Fields      map[string]any `json:"fields,omitempty"`
}
