package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const CookieName = "krm_session"
const defaultIterations = 600000

type Credentials struct {
	SchemaVersion int       `json:"schema_version"`
	Username      string    `json:"username"`
	Algorithm     string    `json:"algorithm"`
	Iterations    int       `json:"iterations"`
	Salt          string    `json:"salt"`
	PasswordHash  string    `json:"password_hash"`
	UpdatedAt     time.Time `json:"updated_at"`
}

func CreateCredentials(path, username, password string) error {
	username = strings.TrimSpace(username)
	if len(username) < 3 || len(username) > 64 || strings.ContainsAny(username, "\r\n\t") {
		return fmt.Errorf("username must be 3..64 characters without controls")
	}
	if len(password) < 10 || len(password) > 1024 {
		return fmt.Errorf("password must be 10..1024 bytes")
	}
	salt := make([]byte, 24)
	if _, err := rand.Read(salt); err != nil {
		return err
	}
	hash := pbkdf2SHA256([]byte(password), salt, defaultIterations, 32)
	value := Credentials{1, username, "pbkdf2-sha256", defaultIterations, base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(hash), time.Now().UTC()}
	data, _ := json.MarshalIndent(value, "", "  ")
	return atomicSecret(path, append(data, '\n'))
}
func LoadCredentials(path string) (Credentials, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Credentials{}, err
	}
	var c Credentials
	if err := json.Unmarshal(data, &c); err != nil {
		return Credentials{}, err
	}
	if c.SchemaVersion != 1 || c.Algorithm != "pbkdf2-sha256" || c.Iterations < 100000 || c.Username == "" {
		return Credentials{}, fmt.Errorf("invalid credentials file")
	}
	return c, nil
}
func Verify(c Credentials, username, password string) bool {
	userOK := subtleString(c.Username, username)
	salt, err1 := base64.RawStdEncoding.DecodeString(c.Salt)
	want, err2 := base64.RawStdEncoding.DecodeString(c.PasswordHash)
	if err1 != nil || err2 != nil || len(want) == 0 {
		return false
	}
	got := pbkdf2SHA256([]byte(password), salt, c.Iterations, len(want))
	hashOK := subtle.ConstantTimeCompare(got, want)
	return userOK&hashOK == 1
}
func subtleString(a, b string) int {
	ha, hb := sha256.Sum256([]byte(a)), sha256.Sum256([]byte(b))
	return subtle.ConstantTimeCompare(ha[:], hb[:])
}
func pbkdf2SHA256(password, salt []byte, iterations, keyLen int) []byte {
	hLen := sha256.Size
	blocks := (keyLen + hLen - 1) / hLen
	out := make([]byte, 0, blocks*hLen)
	buf := make([]byte, len(salt)+4)
	copy(buf, salt)
	for block := 1; block <= blocks; block++ {
		binary.BigEndian.PutUint32(buf[len(salt):], uint32(block))
		mac := hmac.New(sha256.New, password)
		mac.Write(buf)
		u := mac.Sum(nil)
		t := append([]byte(nil), u...)
		for i := 1; i < iterations; i++ {
			mac = hmac.New(sha256.New, password)
			mac.Write(u)
			u = mac.Sum(nil)
			for j := range t {
				t[j] ^= u[j]
			}
		}
		out = append(out, t...)
	}
	return out[:keyLen]
}
func atomicSecret(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".secret-*")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if err := f.Chmod(0600); err != nil {
		f.Close()
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}

type Session struct {
	ID, CSRF, Username, RemoteIP string
	CreatedAt, ExpiresAt         time.Time
}
type SessionStore struct {
	mu       sync.RWMutex
	ttl      time.Duration
	sessions map[string]Session
}

func NewSessionStore(ttl time.Duration) *SessionStore {
	return &SessionStore{ttl: ttl, sessions: map[string]Session{}}
}
func (s *SessionStore) Create(username, ip string) (Session, error) {
	id, err := token(32)
	if err != nil {
		return Session{}, err
	}
	csrf, err := token(32)
	if err != nil {
		return Session{}, err
	}
	now := time.Now().UTC()
	v := Session{id, csrf, username, ip, now, now.Add(s.ttl)}
	s.mu.Lock()
	s.sessions[id] = v
	for k, x := range s.sessions {
		if now.After(x.ExpiresAt) {
			delete(s.sessions, k)
		}
	}
	s.mu.Unlock()
	return v, nil
}
func (s *SessionStore) Get(id string) (Session, bool) {
	s.mu.RLock()
	v, ok := s.sessions[id]
	s.mu.RUnlock()
	if !ok || time.Now().After(v.ExpiresAt) {
		if ok {
			s.Delete(id)
		}
		return Session{}, false
	}
	return v, true
}
func (s *SessionStore) Delete(id string) { s.mu.Lock(); delete(s.sessions, id); s.mu.Unlock() }
func token(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

type Limiter struct {
	mu       sync.Mutex
	window   time.Duration
	max      int
	attempts map[string][]time.Time
}

func NewLimiter(max int, window time.Duration) *Limiter {
	return &Limiter{window: window, max: max, attempts: map[string][]time.Time{}}
}
func (l *Limiter) Allow(key string) bool {
	now := time.Now()
	cut := now.Add(-l.window)
	l.mu.Lock()
	defer l.mu.Unlock()
	old := l.attempts[key]
	kept := old[:0]
	for _, t := range old {
		if t.After(cut) {
			kept = append(kept, t)
		}
	}
	if len(kept) >= l.max {
		l.attempts[key] = kept
		return false
	}
	l.attempts[key] = append(kept, now)
	return true
}
func (l *Limiter) Reset(key string) { l.mu.Lock(); delete(l.attempts, key); l.mu.Unlock() }
func RemoteIP(r *http.Request) string {
	h, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return h
}
func ReadSession(r *http.Request, s *SessionStore) (Session, error) {
	c, err := r.Cookie(CookieName)
	if err != nil {
		return Session{}, err
	}
	v, ok := s.Get(c.Value)
	if !ok {
		return Session{}, errors.New("session expired")
	}
	return v, nil
}
func SetCookie(w http.ResponseWriter, s Session, secure bool) {
	http.SetCookie(w, &http.Cookie{Name: CookieName, Value: s.ID, Path: "/", Expires: s.ExpiresAt, MaxAge: int(time.Until(s.ExpiresAt).Seconds()), HttpOnly: true, Secure: secure, SameSite: http.SameSiteStrictMode})
}
func ClearCookie(w http.ResponseWriter, secure bool) {
	http.SetCookie(w, &http.Cookie{Name: CookieName, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, Secure: secure, SameSite: http.SameSiteStrictMode})
}
