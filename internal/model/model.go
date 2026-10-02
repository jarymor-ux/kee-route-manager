package model

import "time"

const StateSchema = 1

type Node struct {
	ID          string   `json:"id"`
	Label       string   `json:"label"`
	Protocol    string   `json:"protocol"`
	Address     string   `json:"address"`
	Port        int      `json:"port"`
	UUID        string   `json:"uuid"`
	Flow        string   `json:"flow,omitempty"`
	Encryption  string   `json:"encryption,omitempty"`
	Network     string   `json:"network"`
	Security    string   `json:"security"`
	Fingerprint string   `json:"fingerprint,omitempty"`
	ServerName  string   `json:"server_name,omitempty"`
	PublicKey   string   `json:"public_key,omitempty"`
	ShortID     string   `json:"short_id,omitempty"`
	SpiderX     string   `json:"spider_x,omitempty"`
	WSHost      string   `json:"ws_host,omitempty"`
	WSPath      string   `json:"ws_path,omitempty"`
	ALPN        []string `json:"alpn,omitempty"`
	Sources     []string `json:"sources"`
}
type Measurement struct {
	NodeID     string    `json:"node_id"`
	LatencyMS  float64   `json:"latency_ms,omitempty"`
	ResponseMS float64   `json:"response_ms,omitempty"`
	SpeedMbps  float64   `json:"speed_mbps,omitempty"`
	Score      float64   `json:"score,omitempty"`
	Failures   int       `json:"failures"`
	Successes  int       `json:"successes"`
	Healthy    bool      `json:"healthy"`
	Error      string    `json:"error,omitempty"`
	CheckedAt  time.Time `json:"checked_at"`
}
type SourceState struct {
	ID                  string    `json:"id"`
	Name                string    `json:"name"`
	Status              string    `json:"status"`
	NodeCount           int       `json:"node_count"`
	LastAttemptAt       time.Time `json:"last_attempt_at,omitempty"`
	LastSuccessAt       time.Time `json:"last_success_at,omitempty"`
	LastError           string    `json:"last_error,omitempty"`
	ConsecutiveFailures int       `json:"consecutive_failures"`
	RetryLevel          int       `json:"retry_level"`
	NextRetryAt         time.Time `json:"next_retry_at,omitempty"`
	UsingCache          bool      `json:"using_cache"`
	CacheExpiresAt      time.Time `json:"cache_expires_at,omitempty"`
}
type Slot struct {
	Index          int       `json:"index"`
	Tag            string    `json:"tag"`
	NodeID         string    `json:"node_id,omitempty"`
	Label          string    `json:"label,omitempty"`
	Sources        []string  `json:"sources,omitempty"`
	Healthy        bool      `json:"healthy"`
	LastVerifiedAt time.Time `json:"last_verified_at,omitempty"`
	Score          float64   `json:"score,omitempty"`
}
type BenchmarkSummary struct {
	OperationID string        `json:"operation_id,omitempty"`
	StartedAt   time.Time     `json:"started_at,omitempty"`
	FinishedAt  time.Time     `json:"finished_at,omitempty"`
	Mode        string        `json:"mode,omitempty"`
	NodeCount   int           `json:"node_count"`
	TestedCount int           `json:"tested_count"`
	WinnerID    string        `json:"winner_id,omitempty"`
	WinnerLabel string        `json:"winner_label,omitempty"`
	Error       string        `json:"error,omitempty"`
	Results     []Measurement `json:"results,omitempty"`
}
type State struct {
	SchemaVersion       int                    `json:"schema_version"`
	Version             string                 `json:"version"`
	UpdatedAt           time.Time              `json:"updated_at"`
	StartedAt           time.Time              `json:"started_at"`
	ActiveSlot          int                    `json:"active_slot"`
	ActiveNodeID        string                 `json:"active_node_id,omitempty"`
	ActiveSince         time.Time              `json:"active_since,omitempty"`
	DirectMode          bool                   `json:"direct_mode"`
	ConsecutiveFailures int                    `json:"consecutive_failures"`
	ConsecutiveSuccess  int                    `json:"consecutive_success"`
	LastHealthAt        time.Time              `json:"last_health_at,omitempty"`
	LastHealthMessage   string                 `json:"last_health_message,omitempty"`
	LastSwitchAt        time.Time              `json:"last_switch_at,omitempty"`
	LastSwitchReason    string                 `json:"last_switch_reason,omitempty"`
	Pool                []Slot                 `json:"pool"`
	Measurements        map[string]Measurement `json:"measurements"`
	Sources             map[string]SourceState `json:"sources"`
	LastBenchmark       BenchmarkSummary       `json:"last_benchmark"`
	XrayConfigured      bool                   `json:"xray_configured"`
	XrayGeneration      int64                  `json:"xray_generation"`
	XrayLastError       string                 `json:"xray_last_error,omitempty"`
}

func NewState(version, prefix string, size int) State {
	now := time.Now().UTC()
	s := State{SchemaVersion: 1, Version: version, UpdatedAt: now, StartedAt: now, ActiveSlot: -1, Measurements: map[string]Measurement{}, Sources: map[string]SourceState{}}
	for i := 0; i < size; i++ {
		s.Pool = append(s.Pool, Slot{Index: i, Tag: prefix + itoa(i)})
	}
	return s
}
func itoa(v int) string {
	if v == 0 {
		return "0"
	}
	b := [20]byte{}
	i := len(b)
	for v > 0 {
		i--
		b[i] = byte('0' + v%10)
		v /= 10
	}
	return string(b[i:])
}

type NodeView struct {
	ID          string      `json:"id"`
	Label       string      `json:"label"`
	Protocol    string      `json:"protocol"`
	Network     string      `json:"network"`
	Security    string      `json:"security"`
	Sources     []string    `json:"sources"`
	Measurement Measurement `json:"measurement"`
	InPool      bool        `json:"in_pool"`
	Active      bool        `json:"active"`
}
