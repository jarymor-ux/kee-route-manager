package preflight

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// CheckNativePolicy is a bounded, read-only activation guard for new native
// setups. XKeen's missing-policy fallback must never silently proxy all clients.
func CheckNativePolicy(ctx context.Context, xkeenConfig string) error {
	return checkNativePolicy(ctx, xkeenConfig, "http://127.0.0.1:79/rci/show/ip/policy")
}

func checkNativePolicy(ctx context.Context, path, endpoint string) error {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return fmt.Errorf("native XKeen configuration unavailable")
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > 64<<10 {
		return fmt.Errorf("invalid native XKeen configuration")
	}
	data, err := io.ReadAll(io.LimitReader(f, (64<<10)+1))
	if err != nil || len(data) > 64<<10 {
		return fmt.Errorf("invalid native XKeen configuration")
	}
	var native map[string]json.RawMessage
	if strictNativeJSON(data, &native) != nil || native == nil {
		return fmt.Errorf("native XKeen configuration must be a JSON object")
	}
	var token string
	if raw, ok := native["rci_token"]; ok {
		if json.Unmarshal(raw, &token) != nil {
			return fmt.Errorf("invalid native RCI authorization")
		}
		if len(token) > 8192 {
			return fmt.Errorf("invalid native RCI authorization")
		}
		for _, c := range token {
			if c < 33 || c > 126 {
				return fmt.Errorf("invalid native RCI authorization")
			}
		}
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return fmt.Errorf("native RCI check unavailable")
	}
	if token != "" {
		req.Header.Set("X-Ndma-Tkn", token)
	}
	client := &http.Client{Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	defer client.CloseIdleConnections()
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("native RCI policy unavailable; activation refused")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("native RCI authorization failed; activation refused")
	}
	data, err = io.ReadAll(io.LimitReader(resp.Body, (256<<10)+1))
	if err != nil || len(data) > 256<<10 {
		return fmt.Errorf("invalid native RCI policy response")
	}
	var value any
	if strictNativeJSON(data, &value) != nil {
		return fmt.Errorf("invalid native RCI policy response")
	}
	var policies []any
	switch v := value.(type) {
	case []any:
		policies = v
	case map[string]any:
		for _, policy := range v {
			policies = append(policies, policy)
		}
	default:
		return fmt.Errorf("invalid native RCI policy response")
	}
	matches := 0
	for _, value := range policies {
		policy, ok := value.(map[string]any)
		if !ok {
			return fmt.Errorf("invalid native RCI policy entry")
		}
		name, ok := policy["description"].(string)
		if !ok || !strings.EqualFold(name, "xkeen") {
			continue
		}
		matches++
		mark, ok := policy["mark"].(string)
		if !ok || len(mark) == 0 || len(mark) > 8 {
			return fmt.Errorf("native xkeen policy has no usable mark; activation refused")
		}
		number, err := strconv.ParseUint(mark, 16, 32)
		if err != nil || number == 0 {
			return fmt.Errorf("native xkeen policy has no usable mark; activation refused")
		}
	}
	if matches != 1 {
		return fmt.Errorf("exactly one existing native xkeen policy is required; activation refused")
	}
	return nil
}

func strictNativeJSON(data []byte, destination any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	if decoder.Decode(new(any)) != io.EOF {
		return fmt.Errorf("trailing JSON")
	}
	return nil
}
