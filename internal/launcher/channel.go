package launcher

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/jarymor-ux/kee-route-manager/internal/config"
)

type channelPreference struct {
	Schema  int    `json:"schema"`
	Channel string `json:"channel"`
}

func validChannel(channel string) bool { return channel == "rc" || channel == "stable" }

// This is launcher discovery metadata, separate from the immutable commit
// record and controller state. Absence preserves the startup configuration.
func loadChannel(root, fallback string) (string, error) {
	if !validChannel(fallback) {
		return "", errors.New("unsupported update channel")
	}
	fd, err := syscall.Open(filepath.Join(root, "update-channel.json"), syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if errors.Is(err, os.ErrNotExist) {
		return fallback, nil
	}
	if err != nil {
		return "", err
	}
	f := os.NewFile(uintptr(fd), "update-channel.json")
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() > 4096 || info.Sys().(*syscall.Stat_t).Uid != uint32(os.Geteuid()) {
		return "", errors.New("untrusted update channel preference")
	}
	var value channelPreference
	d := json.NewDecoder(io.LimitReader(f, 4097))
	d.DisallowUnknownFields()
	if err = d.Decode(&value); err != nil {
		return "", err
	}
	if d.Decode(new(any)) != io.EOF || value.Schema != 1 || !validChannel(value.Channel) {
		return "", errors.New("invalid update channel preference")
	}
	return value.Channel, nil
}

// mu must be held; c remains immutable so background loops can read it safely.
func (s *supervisor) channelLocked() string {
	if s.channel != "" {
		return s.channel
	}
	return s.c.Update.Channel
}
func (s *supervisor) channelConfigLocked() config.Config {
	c := s.c
	c.Update.Channel = s.channelLocked()
	return c
}

func (s *supervisor) changeChannel(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		reply(w, 405, map[string]string{"error": "method not allowed"})
		return
	}
	var input *struct {
		Channel string `json:"channel"`
	}
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024))
	d.DisallowUnknownFields()
	if err := d.Decode(&input); err != nil || input == nil || !validChannel(input.Channel) || d.Decode(new(any)) != io.EOF {
		reply(w, 400, map[string]string{"error": "invalid update channel"})
		return
	}
	s.mu.Lock()
	if s.closing || s.ctx.Err() != nil {
		s.mu.Unlock()
		reply(w, 503, map[string]string{"error": "launcher is stopping"})
		return
	}
	if !s.c.Update.Enabled || s.c.Update.GitHubRepository == "" {
		s.mu.Unlock()
		reply(w, 409, map[string]string{"error": "channel switching requires enabled GitHub release discovery"})
		return
	}
	if s.status.Applying {
		s.mu.Unlock()
		reply(w, 409, map[string]string{"error": "update already running"})
		return
	}
	if err := atomicJSON(filepath.Join(s.c.Update.InstallDir, "update-channel.json"), channelPreference{Schema: 1, Channel: input.Channel}); err != nil {
		s.mu.Unlock()
		reply(w, 500, map[string]string{"error": "cannot persist update channel"})
		return
	}
	s.channel = input.Channel
	s.channelRevision++
	s.status.Check = nil
	s.status.CheckedAt = time.Time{}
	s.status.LastError = ""
	s.mu.Unlock()
	reply(w, 200, s.snapshot())
}
