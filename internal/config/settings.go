package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"time"
)

var (
	ErrSettingsConflict    = errors.New("configuration changed; reload settings")
	ErrSettingsBusy        = errors.New("configuration change is already pending")
	ErrSettingsInvalid     = errors.New("settings do not satisfy configuration constraints")
	ErrSettingsUnavailable = errors.New("configuration editor is unavailable")
	ErrSettingsRecovery    = errors.New("configuration recovery requires operator reconciliation")
)

// EditableSettings is the complete allowlist exposed by the panel. Runtime
// ownership, credentials, routing ports, targets and pool size stay private.
type EditableSettings struct {
	Benchmark     BenchmarkSettings    `json:"benchmark"`
	Health        HealthSettings       `json:"health"`
	Failover      Failover             `json:"failover"`
	Subscriptions SubscriptionSettings `json:"subscriptions"`
	Pool          PoolSettings         `json:"pool"`
}
type BenchmarkSettings struct {
	FullInterval           Duration `json:"full_interval"`
	BatchSize              int      `json:"batch_size"`
	LatencyWorkers         int      `json:"latency_workers"`
	RequestsPerWeight      int      `json:"requests_per_weight"`
	Finalists              int      `json:"finalists"`
	MinImprovementPercent  int      `json:"min_improvement_percent"`
	SwitchCooldown         Duration `json:"switch_cooldown"`
	StabilityBeforeUpgrade Duration `json:"stability_before_upgrade"`
	Speed                  Speed    `json:"speed"`
}
type HealthSettings struct {
	RequestTimeout       Duration   `json:"request_timeout"`
	MaxResponseBytes     ByteSize   `json:"max_response_bytes"`
	HotPoolFreshness     Duration   `json:"hot_pool_freshness"`
	ProviderRetryBackoff []Duration `json:"provider_retry_backoff"`
	RecoveryThreshold    int        `json:"recovery_threshold"`
}
type SubscriptionSettings struct {
	CacheEnabled      bool     `json:"cache_enabled"`
	CacheTTL          Duration `json:"cache_ttl"`
	RefreshInterval   Duration `json:"refresh_interval"`
	RequestTimeout    Duration `json:"request_timeout"`
	MaxResponseBytes  ByteSize `json:"max_response_bytes"`
	MaxNodesPerSource int      `json:"max_nodes_per_source"`
	MaxSources        int      `json:"max_sources"`
	MaxNodes          int      `json:"max_nodes"`
}
type PoolSettings struct {
	ProviderDiversity ProviderDiversity `json:"provider_diversity"`
}

// Missing groups and unknown fields cannot silently reset settings to zero.
func (s *EditableSettings) UnmarshalJSON(data []byte) error {
	type plain EditableSettings
	var keys map[string]json.RawMessage
	if json.Unmarshal(data, &keys) != nil {
		return ErrSettingsInvalid
	}
	for _, key := range []string{"benchmark", "health", "failover", "subscriptions", "pool"} {
		v, ok := keys[key]
		if !ok || bytes.Equal(bytes.TrimSpace(v), []byte("null")) {
			return ErrSettingsInvalid
		}
	}
	// Save requests replace the complete visible form; omitted fields must not
	// accidentally disable booleans or reset valid zero-valued thresholds.
	expected, _ := json.Marshal(SettingsFromConfig(Default()))
	if !completeSettingsJSON(data, expected) {
		return ErrSettingsInvalid
	}
	var value plain
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if dec.Decode(&value) != nil {
		return ErrSettingsInvalid
	}
	if dec.Decode(new(any)) != io.EOF {
		return ErrSettingsInvalid
	}
	*s = EditableSettings(value)
	return nil
}

func completeSettingsJSON(data, expected []byte) bool {
	var actual, required map[string]json.RawMessage
	if json.Unmarshal(expected, &required) != nil || required == nil {
		return true
	}
	if json.Unmarshal(data, &actual) != nil || actual == nil {
		return false
	}
	for key, value := range required {
		given, ok := actual[key]
		if !ok || bytes.Equal(bytes.TrimSpace(given), []byte("null")) || !completeSettingsJSON(given, value) {
			return false
		}
	}
	return true
}

func SettingsFromConfig(c Config) EditableSettings {
	b := c.Benchmark
	u := c.Subscriptions
	return EditableSettings{
		Benchmark: BenchmarkSettings{b.FullInterval, b.BatchSize, b.LatencyWorkers, b.RequestsPerWeight, b.Finalists, b.MinImprovementPercent, b.SwitchCooldown, b.StabilityBeforeUpgrade, b.Speed},
		Health:    HealthSettings{c.Health.RequestTimeout, c.Health.MaxResponseBytes, c.Health.HotPoolFreshness, append([]Duration(nil), c.Health.ProviderRetryBackoff...), c.Health.RecoveryThreshold}, Failover: c.Failover,
		Subscriptions: SubscriptionSettings{u.CacheEnabled, u.CacheTTL, u.RefreshInterval, u.RequestTimeout, u.MaxResponseBytes, u.MaxNodesPerSource, u.MaxSources, u.MaxNodes},
		Pool:          PoolSettings{c.Pool.ProviderDiversity},
	}
}

// ApplyTo validates the entire merged controller configuration.
func (s EditableSettings) ApplyTo(c Config) (Config, error) {
	b := &c.Benchmark
	v := s.Benchmark
	b.FullInterval = v.FullInterval
	b.BatchSize = v.BatchSize
	b.LatencyWorkers = v.LatencyWorkers
	b.RequestsPerWeight = v.RequestsPerWeight
	b.Finalists = v.Finalists
	b.MinImprovementPercent = v.MinImprovementPercent
	b.SwitchCooldown = v.SwitchCooldown
	b.StabilityBeforeUpgrade = v.StabilityBeforeUpgrade
	b.Speed = v.Speed
	c.Health.RequestTimeout = s.Health.RequestTimeout
	c.Health.MaxResponseBytes = s.Health.MaxResponseBytes
	c.Health.HotPoolFreshness = s.Health.HotPoolFreshness
	c.Health.ProviderRetryBackoff = append([]Duration(nil), s.Health.ProviderRetryBackoff...)
	c.Health.RecoveryThreshold = s.Health.RecoveryThreshold
	c.Failover = s.Failover
	u := &c.Subscriptions
	n := s.Subscriptions
	u.CacheEnabled = n.CacheEnabled
	u.CacheTTL = n.CacheTTL
	u.RefreshInterval = n.RefreshInterval
	u.RequestTimeout = n.RequestTimeout
	u.MaxResponseBytes = n.MaxResponseBytes
	u.MaxNodesPerSource = n.MaxNodesPerSource
	u.MaxSources = n.MaxSources
	u.MaxNodes = n.MaxNodes
	c.Pool.ProviderDiversity = s.Pool.ProviderDiversity
	if c.Instance.Role != "controller" || b.FullInterval.Duration < time.Minute || c.Validate() != nil {
		return Config{}, ErrSettingsInvalid
	}
	return c, nil
}
