package platform

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"
)

// The loopback RCI listener serves commands but some Keenetic firmware denies
// /ci downloads there. Both fixed read-only NDMC commands are hardware verified.
func (k *keenetic) configFileNDMC(ctx context.Context, name string) ([]byte, error) {
	command := ""
	switch name {
	case "running-config.txt":
		command = "more running-config"
	case "startup-config.txt":
		command = "more startup-config"
	default:
		return nil, fmt.Errorf("unsupported Keenetic configuration file")
	}
	runner := k.r
	const maxConfigBytes = 4 << 20
	limit := runner.MaxOutput
	if limit <= 0 || limit > maxConfigBytes {
		limit = maxConfigBytes
		runner.MaxOutput = limit
	}
	data, err := runner.Run(ctx, []string{k.cfg.Platform.Keenetic.NDMCBinary, "-c", command})
	if err != nil {
		// Runner errors may contain stderr or stdout, including private config.
		// Retain cancellation classification without propagating command text.
		if errors.Is(err, context.Canceled) {
			return nil, fmt.Errorf("keenetic configuration read interrupted: %w", context.Canceled)
		}
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, fmt.Errorf("keenetic configuration read interrupted: %w", context.DeadlineExceeded)
		}
		return nil, fmt.Errorf("keenetic configuration command failed")
	}
	if len(data) == 0 || int64(len(data)) >= limit || !utf8.Valid(data) || bytes.IndexByte(data, 0) >= 0 {
		return nil, fmt.Errorf("keenetic configuration command returned incomplete output")
	}
	// Confirmed NDMC `more` transport: one CSI erase-line prefix and the same
	// standalone trailer. Never strip escape sequences inside configuration.
	clearLine := []byte("\x1b[K")
	leading, trailing := bytes.HasPrefix(data, clearLine), bytes.HasSuffix(data, clearLine)
	if leading != trailing {
		return nil, fmt.Errorf("keenetic configuration command returned incomplete framing")
	}
	if leading {
		if len(data) < 2*len(clearLine) {
			return nil, fmt.Errorf("keenetic configuration command returned incomplete framing")
		}
		data = data[len(clearLine) : len(data)-len(clearLine)]
	}
	if bytes.IndexByte(data, '\x1b') >= 0 {
		return nil, fmt.Errorf("keenetic configuration command returned invalid framing")
	}
	text := strings.TrimSpace(string(data))
	lines := strings.Split(text, "\n")
	if !strings.HasPrefix(text, "!") || strings.TrimSpace(lines[len(lines)-1]) != "!" || len(keeneticSavedChecksumPattern.FindAllSubmatch(data, -1)) != 1 {
		return nil, fmt.Errorf("keenetic configuration command returned invalid configuration")
	}
	// A header alone cannot prove that the command returned a complete config.
	hasCommand := false
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line != "" && !strings.HasPrefix(line, "!") {
			hasCommand = true
			break
		}
	}
	if !hasCommand {
		return nil, fmt.Errorf("keenetic configuration command returned incomplete configuration")
	}
	return data, nil
}
