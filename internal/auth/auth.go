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
const maxIterations = 1000000

// Bound concurrent CPU-expensive verifications across all server instances.
var verificationSlots = make(chan struct{}, 2)

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
	value, err := newCredentials(username, password)
	if err != nil {
		return err
	}
	data, _ := json.MarshalIndent(value, "", "  ")
	return atomicSecret(path, append(data, '\n'))
}
func newCredentials(username, password string) (Credentials, error) {
	username = strings.TrimSpace(username)
	if !validUsername(username) {
		return Credentials{}, fmt.Errorf("username must be 3..64 bytes without controls")
	}
	if len(password) < 10 || len(password) > 1024 {
		return Credentials{}, fmt.Errorf("password must be 10..1024 bytes")
	}
	salt := make([]byte, 24)
	if _, err := rand.Read(salt); err != nil {
		return Credentials{}, err
	}
	hash := pbkdf2SHA256([]byte(password), salt, defaultIterations, 32)
	return Credentials{1, username, "pbkdf2-sha256", defaultIterations, base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(hash), time.Now().UTC()}, nil
}
func validUsername(username string) bool {
	if len(username) < 3 || len(username) > 64 || strings.TrimSpace(username) != username {
		return false
	}
	for _, c := range username {
		if c < 32 || c == 127 {
			return false
		}
	}
	return true
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
	if !validCredentials(c) {
		return Credentials{}, fmt.Errorf("invalid credentials file")
	}
	return c, nil
}
func validCredentials(c Credentials) bool {
	if c.SchemaVersion != 1 || c.Algorithm != "pbkdf2-sha256" || c.Iterations < 100000 || c.Iterations > maxIterations || !validUsername(c.Username) {
		return false
	}
	if len(c.Salt) > 64 || len(c.PasswordHash) > 64 {
		return false
	}
	salt, e1 := base64.RawStdEncoding.DecodeString(c.Salt)
	hash, e2 := base64.RawStdEncoding.DecodeString(c.PasswordHash)
	return e1 == nil && e2 == nil && len(salt) == 24 && len(hash) == 32
}
func Verify(c Credentials, username, password string) bool {
	if len(username) < 3 || len(username) > 64 || len(password) < 10 || len(password) > 1024 || !validCredentials(c) {
		return false
	}
	select {
	case verificationSlots <- struct{}{}:
		defer func() { <-verificationSlots }()
	default:
		return false
	}

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
	if err := os.Rename(name, path); err != nil {
		return err
	}
	dir, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}

type Session struct {
	ID, CSRF, Username, RemoteIP string
	UserID                       string
	Revision                     uint64
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
	return s.create(username, ip, "", 0)
}
func (s *SessionStore) CreateForUser(user User, revision uint64, ip string) (Session, error) {
	return s.create(user.Username, ip, user.ID, revision)
}
func (s *SessionStore) create(username, ip, userID string, revision uint64) (Session, error) {
	id, err := token(32)
	if err != nil {
		return Session{}, err
	}
	csrf, err := token(32)
	if err != nil {
		return Session{}, err
	}
	now := time.Now().UTC()
	v := Session{ID: id, CSRF: csrf, Username: username, RemoteIP: ip, CreatedAt: now, ExpiresAt: now.Add(s.ttl), UserID: userID, Revision: revision}
	s.mu.Lock()
	for k, x := range s.sessions {
		if now.After(x.ExpiresAt) {
			delete(s.sessions, k)
		}
	}
	if len(s.sessions) >= 1024 {
		s.mu.Unlock()
		return Session{}, errors.New("session capacity reached")
	}
	s.sessions[id] = v
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
	global   []time.Time
	attempts map[string][]time.Time
}

func NewLimiter(max int, window time.Duration) *Limiter {
	return &Limiter{window: window, max: max, attempts: map[string][]time.Time{}}
}
func (l *Limiter) Allow(key string) bool { return l.allow(key, false) }

// AllowAggregate retains the global budget for a shared local proxy transport.
// Client quotas are enforced at the UI edge using its actual socket peer.
func (l *Limiter) AllowAggregate() bool { return l.allow("", true) }
func (l *Limiter) allow(key string, aggregate bool) bool {
	now := time.Now()
	cut := now.Add(-l.window)
	l.mu.Lock()
	defer l.mu.Unlock()
	// Prune all inactive clients, not only the next client's key.
	for k, ts := range l.attempts {
		if len(ts) == 0 || !ts[len(ts)-1].After(cut) {
			delete(l.attempts, k)
		}
	}
	keptGlobal := l.global[:0]
	for _, t := range l.global {
		if t.After(cut) {
			keptGlobal = append(keptGlobal, t)
		}
	}
	l.global = keptGlobal
	if l.max < 1 || l.window <= 0 || len(l.global) >= l.max*8 {
		return false
	}
	if aggregate {
		l.global = append(l.global, now)
		return true
	}
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
	l.global = append(l.global, now)
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
