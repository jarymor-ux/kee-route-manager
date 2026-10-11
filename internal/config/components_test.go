package config

import (
 "testing"
 "time"
)

func TestComponentUpdateDefaultIsOneWeek(t *testing.T) {
 cfg := Default()
 if cfg.Components.CheckInterval.Duration != 7*24*time.Hour { t.Fatalf("default component updates must be weekly, got %s", cfg.Components.CheckInterval.Duration) }
}

func TestComponentUpdateIntervalValidation(t *testing.T) {
 cfg := Default()
 cfg.Components.CheckInterval = Dur(0)
 if err := cfg.Validate(); err == nil { t.Fatal("zero component update interval accepted") }
 cfg.Components.CheckInterval = Dur(15*time.Minute)
 if err := cfg.Validate(); err == nil { t.Fatal("too frequent component update interval accepted") }
}
