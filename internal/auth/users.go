package auth

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

const legacyUserID = "legacy-admin"
const maxUsers = 128

var ErrUserValidation = errors.New("invalid user change")
var ErrUserConflict = errors.New("user changed; reload before saving")
var ErrUserPermission = errors.New("permission denied")

// Permissions is the closed server-side authorization vocabulary.
func Permissions() []string {
	return []string{"vpn.view", "vpn.control", "subscriptions.view", "subscriptions.manage", "router.view", "router.clients", "router.policy", "router.wake", "router.system", "router.reboot", "updates.manage", "users.manage", "config.manage", "events.view"}
}

// Roles are creation/editing templates; authorization always uses the saved permissions.
func Roles() map[string][]string {
	return map[string][]string{
		"admin":           Permissions(),
		"viewer":          {"vpn.view", "subscriptions.view", "router.view", "router.clients", "events.view"},
		"vpn-operator":    {"vpn.view", "vpn.control", "subscriptions.view", "subscriptions.manage", "events.view"},
		"router-operator": {"router.view", "router.clients", "router.policy", "router.wake", "router.system", "router.reboot", "events.view"},
	}
}

type User struct {
	ID          string    `json:"id"`
	Username    string    `json:"username"`
	Enabled     bool      `json:"enabled"`
	Permissions []string  `json:"permissions"`
	UpdatedAt   time.Time `json:"updated_at"`
}

func (u User) Has(permission string) bool {
	for _, p := range u.Permissions {
		if p == permission {
			return true
		}
	}
	return false
}

type UserInput struct {
	ID                string     `json:"id,omitempty"`
	Username          string     `json:"username"`
	Password          string     `json:"password,omitempty"`
	Enabled           bool       `json:"enabled"`
	Permissions       []string   `json:"permissions"`
	ExpectedUpdatedAt *time.Time `json:"expected_updated_at,omitempty"`
}
type UserDeleteInput struct {
	ID                string     `json:"id"`
	ExpectedUpdatedAt *time.Time `json:"expected_updated_at,omitempty"`
}
type userRecord struct {
	User
	Credentials Credentials `json:"credentials"`
	Revision    uint64      `json:"revision"`
}
type usersFile struct {
	SchemaVersion     int          `json:"schema_version"`
	LegacyFingerprint string       `json:"legacy_fingerprint"`
	Users             []userRecord `json:"users"`
}
type UserStore struct {
	mu    sync.RWMutex
	path  string
	value usersFile
}

func credentialsFingerprint(c Credentials) string {
	data, _ := json.Marshal(c)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// LoadUsers is read-only, including migration/recovery views. Explicit user
// mutations persist the sidecar; schema-1 credentials remain intact for downgrade.
// Offline passwd restores the legacy administrator on the next startup.
func LoadUsers(credentialsPath string) (*UserStore, error) {
	legacy, err := LoadCredentials(credentialsPath)
	if err != nil {
		return nil, err
	}
	store := &UserStore{path: credentialsPath + ".users.json"}
	fingerprint := credentialsFingerprint(legacy)
	file, err := os.Open(store.path)
	if errors.Is(err, os.ErrNotExist) {
		store.value = usersFile{1, fingerprint, []userRecord{{User{legacyUserID, legacy.Username, true, Permissions(), legacy.UpdatedAt}, legacy, 1}}}
		return store, nil
	}
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		return nil, fmt.Errorf("users file must be private")
	}
	if info.Size() > 1<<20 {
		return nil, fmt.Errorf("users file exceeds size limit")
	}
	decoder := json.NewDecoder(io.LimitReader(file, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&store.value); err != nil {
		return nil, fmt.Errorf("invalid users file")
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return nil, fmt.Errorf("invalid users file")
	}
	if err := validateUsersFile(store.value); err != nil {
		return nil, err
	}
	if fingerprint != store.value.LegacyFingerprint {
		// The owner explicitly reset credentials offline. A conflicting panel user
		// must be renamed/deleted first rather than silently stealing its identity.
		for _, record := range store.value.Users {
			if record.ID != legacyUserID && strings.EqualFold(record.Username, legacy.Username) {
				return nil, fmt.Errorf("offline administrator name conflicts with an existing user")
			}
		}
		found := false
		for i, record := range store.value.Users {
			if record.ID == legacyUserID {
				store.value.Users[i] = userRecord{User{legacyUserID, legacy.Username, true, Permissions(), legacy.UpdatedAt}, legacy, record.Revision + 1}
				found = true
			}
		}
		if !found {
			store.value.Users = append(store.value.Users, userRecord{User{legacyUserID, legacy.Username, true, Permissions(), legacy.UpdatedAt}, legacy, 1})
		}
		store.value.LegacyFingerprint = fingerprint
	}
	return store, nil
}
func validateUsersFile(value usersFile) error {
	if value.SchemaVersion != 1 || len(value.LegacyFingerprint) != 64 || len(value.Users) == 0 || len(value.Users) > maxUsers {
		return fmt.Errorf("invalid users file")
	}
	if _, err := hex.DecodeString(value.LegacyFingerprint); err != nil {
		return fmt.Errorf("invalid users file")
	}
	ids, names := map[string]bool{}, map[string]bool{}
	for _, r := range value.Users {
		p, err := validatedPermissions(r.Permissions)
		if r.ID == "" || len(r.ID) > 64 || ids[r.ID] || names[strings.ToLower(r.Username)] || !validCredentials(r.Credentials) || r.Username != r.Credentials.Username || r.Revision == 0 || err != nil || len(p) != len(r.Permissions) {
			return fmt.Errorf("invalid users file")
		}
		ids[r.ID] = true
		names[strings.ToLower(r.Username)] = true
	}
	if !hasAdministrator(value.Users) {
		return fmt.Errorf("users file has no enabled administrator")
	}
	return nil
}
func validatedPermissions(permissions []string) ([]string, error) {
	known := map[string]bool{}
	for _, p := range Permissions() {
		known[p] = true
	}
	seen := map[string]bool{}
	result := []string{}
	for _, p := range permissions {
		if !known[p] {
			return nil, fmt.Errorf("%w: unknown permission", ErrUserValidation)
		}
		if !seen[p] {
			result = append(result, p)
			seen[p] = true
		}
	}
	sort.Strings(result)
	return result, nil
}
func hasAdministrator(users []userRecord) bool {
	for _, r := range users {
		if r.Enabled && r.Has("users.manage") {
			return true
		}
	}
	return false
}
func copyUser(u User) User { u.Permissions = append([]string{}, u.Permissions...); return u }
func (s *UserStore) List() []User {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]User, 0, len(s.value.Users))
	for _, r := range s.value.Users {
		result = append(result, copyUser(r.User))
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Username < result[j].Username })
	return result
}
func (s *UserStore) Current(id string, revision uint64) (User, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, r := range s.value.Users {
		if r.ID == id && r.Revision == revision && r.Enabled {
			return copyUser(r.User), true
		}
	}
	return User{}, false
}
func (s *UserStore) Authenticate(username, password string) (User, uint64, bool) {
	s.mu.RLock()
	var record userRecord
	// Verify against a real record even for unknown/disabled users to avoid the
	// simple fast failure that distinguishes existing accounts.
	if len(s.value.Users) > 0 {
		record = s.value.Users[0]
	}
	found := false
	for _, r := range s.value.Users {
		if r.Username == username {
			record = r
			found = r.Enabled
			break
		}
	}
	s.mu.RUnlock()
	valid := Verify(record.Credentials, username, password)
	if !valid || !found {
		return User{}, 0, false
	}
	user, ok := s.Current(record.ID, record.Revision)
	return user, record.Revision, ok
}
func (s *UserStore) Save(input UserInput) (User, error) {
	return s.save(input, "", 0)
}

// SaveAs rechecks the initiating actor inside the same transaction as the edit.
func (s *UserStore) SaveAs(input UserInput, actorID string, revision uint64) (User, error) {
	if actorID == "" {
		return User{}, ErrUserPermission
	}
	return s.save(input, actorID, revision)
}
func (s *UserStore) mayManage(actorID string, revision uint64) bool {
	for _, r := range s.value.Users {
		if r.ID == actorID && r.Revision == revision && r.Enabled && r.Has("users.manage") {
			return true
		}
	}
	return false
}
func (s *UserStore) save(input UserInput, actorID string, revision uint64) (User, error) {
	input.Username = strings.TrimSpace(input.Username)
	if !validUsername(input.Username) {
		return User{}, fmt.Errorf("%w: username must be 3..64 bytes without controls", ErrUserValidation)
	}
	permissions, err := validatedPermissions(input.Permissions)
	if err != nil {
		return User{}, err
	}
	if input.Password != "" && (len(input.Password) < 10 || len(input.Password) > 1024) {
		return User{}, fmt.Errorf("%w: password must be 10..1024 bytes", ErrUserValidation)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if actorID != "" && !s.mayManage(actorID, revision) {
		return User{}, ErrUserPermission
	}
	next := s.value
	next.Users = append([]userRecord(nil), s.value.Users...)
	index := -1
	for i, r := range next.Users {
		if r.ID == input.ID && input.ID != "" {
			index = i
		}
		if r.ID != input.ID && strings.EqualFold(r.Username, input.Username) {
			return User{}, fmt.Errorf("%w: username already exists", ErrUserValidation)
		}
	}
	if input.ID != "" && index < 0 {
		return User{}, fmt.Errorf("%w: user does not exist", ErrUserValidation)
	}
	if index >= 0 && input.ExpectedUpdatedAt != nil && !input.ExpectedUpdatedAt.Equal(next.Users[index].UpdatedAt) {
		return User{}, ErrUserConflict
	}
	if index < 0 && input.ExpectedUpdatedAt != nil {
		return User{}, fmt.Errorf("%w: new user cannot have an edit precondition", ErrUserValidation)
	}
	var record userRecord
	if index < 0 {
		limit := maxUsers
		legacyPresent := false
		for _, r := range next.Users {
			if r.ID == legacyUserID {
				legacyPresent = true
				break
			}
		}
		if !legacyPresent {
			limit--
		} // reserve owner-recovery account capacity
		if len(next.Users) >= limit {
			return User{}, fmt.Errorf("%w: user limit reached", ErrUserValidation)
		}
		if input.Password == "" {
			return User{}, fmt.Errorf("%w: password required for new user", ErrUserValidation)
		}
		id, err := token(18)
		if err != nil {
			return User{}, err
		}
		record.ID = id
		record.Revision = 1
	} else {
		record = next.Users[index]
	}
	if input.Password != "" {
		credentials, err := newCredentials(input.Username, input.Password)
		if err != nil {
			return User{}, err
		}
		record.Credentials = credentials
		if index >= 0 {
			record.Revision++
		}
	} else {
		record.Credentials.Username = input.Username
	}
	if index >= 0 && (record.Username != input.Username || record.Enabled != input.Enabled) {
		record.Revision++
	}
	record.User = User{record.ID, input.Username, input.Enabled, permissions, time.Now().UTC()}
	if index < 0 {
		next.Users = append(next.Users, record)
	} else {
		next.Users[index] = record
	}
	if !hasAdministrator(next.Users) {
		return User{}, fmt.Errorf("%w: last enabled administrator cannot be removed", ErrUserValidation)
	}
	if err := s.persist(next); err != nil {
		return User{}, err
	}
	s.value = next
	return copyUser(record.User), nil
}
func (s *UserStore) Delete(id string) error {
	return s.delete(UserDeleteInput{ID: id}, "", 0)
}
func (s *UserStore) DeleteAs(input UserDeleteInput, actorID string, revision uint64) error {
	if actorID == "" {
		return ErrUserPermission
	}
	return s.delete(input, actorID, revision)
}
func (s *UserStore) delete(input UserDeleteInput, actorID string, revision uint64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if actorID != "" && !s.mayManage(actorID, revision) {
		return ErrUserPermission
	}
	next := s.value
	next.Users = make([]userRecord, 0, len(s.value.Users))
	found := false
	for _, r := range s.value.Users {
		if r.ID == input.ID {
			if input.ExpectedUpdatedAt != nil && !input.ExpectedUpdatedAt.Equal(r.UpdatedAt) {
				return ErrUserConflict
			}
			found = true
		} else {
			next.Users = append(next.Users, r)
		}
	}
	if !found {
		return fmt.Errorf("%w: user does not exist", ErrUserValidation)
	}
	if !hasAdministrator(next.Users) {
		return fmt.Errorf("%w: last enabled administrator cannot be removed", ErrUserValidation)
	}
	if err := s.persist(next); err != nil {
		return err
	}
	s.value = next
	return nil
}
func (s *UserStore) persist(value usersFile) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return atomicSecret(s.path, append(data, '\n'))
}
