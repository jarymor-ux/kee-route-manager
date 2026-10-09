package platform

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// NDMS publishes configuration revisions asynchronously after command read-back.
// A moving revision is not evidence of foreign edits: verify the owned content
// on every observation, and only accept a coherent, bounded snapshot.
func (k *keenetic) waitConfigurationRevision(ctx context.Context, accept func(string) bool, verify func(context.Context) error) (string, error) {
	timeout := 5 * time.Second
	if k.r.Timeout > 0 && k.r.Timeout < timeout {
		timeout = k.r.Timeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	for {
		before, err := k.runningConfigChecksum(ctx)
		if err != nil {
			return "", err
		}
		if err = verify(ctx); err != nil {
			return "", err
		}
		after, err := k.runningConfigChecksum(ctx)
		if err != nil {
			return "", err
		}
		if before == after && accept(after) {
			return after, nil
		}
		if err := waitKeeneticConfigurationPoll(ctx); err != nil {
			return "", fmt.Errorf("keenetic configuration revision did not settle: %w", err)
		}
	}
}

func waitKeeneticConfigurationPoll(ctx context.Context) error {
	timer := time.NewTimer(100 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func normalizeKeeneticConfiguration(data []byte) string {
	var lines []string
	for _, line := range strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed != "" && !strings.HasPrefix(trimmed, "!") {
			lines = append(lines, strings.TrimRight(line, " \t"))
		}
	}
	return strings.Join(lines, "\n")
}

// A checksum alone cannot prove an asynchronous save persisted the desired
// content. Keep the owned running snapshot unchanged and verify both the saved
// revision and complete saved commands before reporting success.
func (k *keenetic) waitConfigurationSavedContent(ctx context.Context, expectedChecksum string, expectedConfig []byte) error {
	timeout := k.r.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	expected := normalizeKeeneticConfiguration(expectedConfig)
	if expected == "" {
		return fmt.Errorf("keenetic configuration snapshot is empty")
	}
	for {
		before, err := k.runningConfigChecksum(ctx)
		if err != nil {
			return err
		}
		running, err := k.configFile(ctx, "running-config.txt")
		if err != nil {
			return err
		}
		if before != expectedChecksum || normalizeKeeneticConfiguration(running) != expected {
			return fmt.Errorf("keenetic configuration drift while waiting for saved content")
		}
		saved, err := k.configFile(ctx, "startup-config.txt")
		if err != nil {
			return err
		}
		match := keeneticSavedChecksumPattern.FindSubmatch(saved)
		if len(match) != 2 {
			return fmt.Errorf("keenetic startup-config missing MD5 checksum")
		}
		after, err := k.runningConfigChecksum(ctx)
		if err != nil {
			return err
		}
		if after != expectedChecksum {
			return fmt.Errorf("keenetic configuration drift while waiting for saved content")
		}
		if strings.EqualFold(string(match[1]), expectedChecksum) && normalizeKeeneticConfiguration(saved) == expected {
			return nil
		}
		if err := waitKeeneticConfigurationPoll(ctx); err != nil {
			return fmt.Errorf("keenetic saved configuration content unconfirmed: %w", err)
		}
	}
}
