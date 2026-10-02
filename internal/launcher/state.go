// Package launcher supervises versioned KRM processes. It never opens or restores
// controller state or changes Xray/firewall configuration.
package launcher

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"syscall"
	"time"
)

var versionPattern = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?$`)

type record struct {
	Schema          int       `json:"schema"`
	Active          string    `json:"active"`
	ActiveDigest    string    `json:"active_digest"`
	Previous        string    `json:"previous,omitempty"`
	PreviousDigest  string    `json:"previous_digest,omitempty"`
	Candidate       string    `json:"candidate,omitempty"`
	CandidateDigest string    `json:"candidate_digest,omitempty"`
	Nonce           string    `json:"nonce,omitempty"`
	Phase           string    `json:"phase"`
	UIConfig        string    `json:"ui_config,omitempty"`
	LastError       string    `json:"last_error,omitempty"`
	LastResult      string    `json:"last_result,omitempty"`
	UpdatedAt       time.Time `json:"updated_at"`
}

func privateDir(path string) error {
	if !filepath.IsAbs(path) || filepath.Clean(path) == "/" {
		return errors.New("launcher directory must be an absolute dedicated directory")
	}
	if err := os.MkdirAll(path, 0700); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode().Perm()&0077 != 0 || info.Sys().(*syscall.Stat_t).Uid != uint32(os.Geteuid()) {
		return errors.New("launcher directory must be owner-only and owned by the current user")
	}
	return nil
}

func atomicJSON(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".record-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(append(data, '\n')); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err = os.Rename(f.Name(), path); err != nil {
		return err
	}
	dir, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}

func loadRecord(root string) (record, error) {
	var out record
	path := filepath.Join(root, "launcher.json")
	info, err := os.Lstat(path)
	if err != nil {
		return out, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() > 64<<10 || info.Sys().(*syscall.Stat_t).Uid != uint32(os.Geteuid()) {
		return out, errors.New("untrusted launcher record")
	}
	f, err := os.Open(path)
	if err != nil {
		return out, err
	}
	defer f.Close()
	dec := json.NewDecoder(io.LimitReader(f, (64<<10)+1))
	dec.DisallowUnknownFields()
	if err = dec.Decode(&out); err != nil {
		return out, err
	}
	if dec.Decode(new(any)) != io.EOF {
		return out, errors.New("trailing launcher record data")
	}
	if out.Schema != 1 || (out.Phase != "committed" && out.Phase != "trial") {
		return out, errors.New("unsupported launcher record")
	}
	for _, version := range []string{out.Active, out.Previous, out.Candidate} {
		if version != "" && (len(version) > 128 || !versionPattern.MatchString(version)) {
			return out, errors.New("invalid launcher version")
		}
	}
	if out.Active == "" || len(out.ActiveDigest) != 64 || out.UIConfig != "" && !filepath.IsAbs(out.UIConfig) {
		return out, errors.New("incomplete launcher record")
	}
	if out.Phase == "trial" && (out.Candidate == "" || len(out.Nonce) != 64 || len(out.CandidateDigest) != 64) {
		return out, errors.New("incomplete trial record")
	}
	return out, nil
}

func saveRecord(root string, rec record) error {
	rec.UpdatedAt = time.Now().UTC()
	return atomicJSON(filepath.Join(root, "launcher.json"), rec)
}

// current is a convenience for the CLI and service wrappers; launcher.json is
// the only authoritative commit point and executables use its immutable paths.
func setCurrent(root, version string) error {
	if !versionPattern.MatchString(version) || len(version) > 128 {
		return fmt.Errorf("invalid release version")
	}
	temp := filepath.Join(root, ".current-new")
	if err := os.Remove(temp); err != nil && !os.IsNotExist(err) {
		return err
	}
	if err := os.Symlink(filepath.Join("releases", version), temp); err != nil {
		return err
	}
	defer os.Remove(temp)
	if info, err := os.Lstat(filepath.Join(root, "current")); err == nil && info.Mode()&os.ModeSymlink == 0 {
		return errors.New("refusing to replace a non-symlink current path")
	}
	if err := os.Rename(temp, filepath.Join(root, "current")); err != nil {
		return err
	}
	dir, err := os.Open(root)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}
