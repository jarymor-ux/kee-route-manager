package auth

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestUsersMigrationRevocationAndLegacyRecovery(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credentials.json")
	if err := CreateCredentials(path, "admin", "synthetic-password"); err != nil {
		t.Fatal(err)
	}
	original, _ := os.ReadFile(path)
	users, err := LoadUsers(path)
	if err != nil {
		t.Fatal(err)
	}
	admin, rev, ok := users.Authenticate("admin", "synthetic-password")
	if !ok || !admin.Has("users.manage") {
		t.Fatal("legacy login rejected")
	}
	if _, err := users.Save(UserInput{ID: admin.ID, Username: admin.Username, Enabled: false, Permissions: Permissions()}); err == nil {
		t.Fatal("disabled last administrator")
	}
	if err := users.Delete(admin.ID); err == nil {
		t.Fatal("deleted last administrator")
	}
	second, err := users.Save(UserInput{Username: "second-admin", Password: "second-password", Enabled: true, Permissions: Permissions()})
	if err != nil {
		t.Fatal(err)
	}
	changed, err := users.Save(UserInput{ID: admin.ID, Username: "admin", Enabled: true, Permissions: []string{"vpn.view"}})
	if err != nil {
		t.Fatal(err)
	}
	current, ok := users.Current(admin.ID, rev)
	if !ok || current.Has("users.manage") || !reflect.DeepEqual(current.Permissions, changed.Permissions) {
		t.Fatal("permission revocation not immediate")
	}
	_, err = users.Save(UserInput{ID: admin.ID, Username: "admin", Password: "replacement-password", Enabled: true, Permissions: changed.Permissions})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := users.Current(admin.ID, rev); ok {
		t.Fatal("password change retained session")
	}
	bytes, _ := os.ReadFile(path)
	if string(bytes) != string(original) {
		t.Fatal("panel rewrote downgrade credentials")
	}
	reloaded, err := LoadUsers(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, ok := reloaded.Authenticate("admin", "synthetic-password"); ok {
		t.Fatal("legacy password bypassed panel password change")
	}
	if _, _, ok := reloaded.Authenticate("admin", "replacement-password"); !ok {
		t.Fatal("panel password lost on reload")
	}
	if err := CreateCredentials(path, "recovered-admin", "recovery-password"); err != nil {
		t.Fatal(err)
	}
	recovered, err := LoadUsers(path)
	if err != nil {
		t.Fatal(err)
	}
	restored, _, ok := recovered.Authenticate("recovered-admin", "recovery-password")
	if !ok || restored.ID != admin.ID || !restored.Has("users.manage") {
		t.Fatal("offline credential recovery failed")
	}
	if _, _, ok := recovered.Authenticate("second-admin", "second-password"); !ok {
		t.Fatal("recovery lost other users")
	}
	if err := recovered.Delete(second.ID); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path + ".users.json")
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("users file not private")
	}
}

func TestUsersValidationAndFailedPersistence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credentials.json")
	if err := CreateCredentials(path, "admin", "synthetic-password"); err != nil {
		t.Fatal(err)
	}
	users, err := LoadUsers(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, input := range []UserInput{
		{Username: "ab", Password: "long-password", Enabled: true},
		{Username: "control\x00name", Password: "long-password", Enabled: true},
		{Username: "another", Password: "short", Enabled: true},
		{Username: "another", Password: "long-password", Enabled: true, Permissions: []string{"unknown"}},
		{Username: "admin", Password: "long-password", Enabled: true},
		{ID: "missing", Username: "another", Enabled: true},
	} {
		if _, err := users.Save(input); err == nil {
			t.Fatalf("invalid input accepted: %+v", input)
		}
	}
	admin := users.List()[0]
	if _, err := users.Save(UserInput{ID: admin.ID, Username: admin.Username, Enabled: true, Permissions: admin.Permissions}); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path + ".users.json"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path+".users.json", 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := users.Save(UserInput{Username: "valid-user", Password: "valid-password", Enabled: true}); err == nil {
		t.Fatal("persistence failure ignored")
	}
	if len(users.List()) != 1 {
		t.Fatal("failed write became live")
	}
}

func TestUsersLoadAndRecoveryNeverWrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credentials.json")
	if err := CreateCredentials(path, "admin", "synthetic-password"); err != nil {
		t.Fatal(err)
	}
	users, err := LoadUsers(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path + ".users.json"); !os.IsNotExist(err) {
		t.Fatal("load migrated on disk")
	}
	admin := users.List()[0]
	if _, err := users.Save(UserInput{ID: admin.ID, Username: admin.Username, Enabled: true, Permissions: admin.Permissions}); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(path + ".users.json")
	if err := CreateCredentials(path, "recovery", "recovery-password"); err != nil {
		t.Fatal(err)
	}
	recovered, err := LoadUsers(path)
	if err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(path + ".users.json")
	if string(before) != string(after) {
		t.Fatal("read-only recovery wrote user data")
	}
	if _, _, ok := recovered.Authenticate("recovery", "recovery-password"); !ok {
		t.Fatal("read-only recovery unavailable")
	}
	if err := os.WriteFile(path+".users.json", []byte(`{"schema_version":99}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadUsers(path); err == nil {
		t.Fatal("corrupt user sidecar silently discarded")
	}
}

func TestUserEditsCompareVersionAndActorAuthority(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credentials.json")
	if err := CreateCredentials(path, "admin", "synthetic-password"); err != nil {
		t.Fatal(err)
	}
	users, err := LoadUsers(path)
	if err != nil {
		t.Fatal(err)
	}
	admin := users.List()[0]
	target, err := users.SaveAs(UserInput{Username: "other-admin", Password: "other-password", Enabled: true, Permissions: Permissions()}, admin.ID, 1)
	if err != nil {
		t.Fatal(err)
	}
	stamp := target.UpdatedAt
	edits := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() {
			_, err := users.SaveAs(UserInput{ID: target.ID, Username: target.Username, Enabled: true, Permissions: Permissions(), ExpectedUpdatedAt: &stamp}, admin.ID, 1)
			edits <- err
		}()
	}
	succeeded, conflicted := 0, 0
	for i := 0; i < 2; i++ {
		err := <-edits
		if err == nil {
			succeeded++
		} else if errors.Is(err, ErrUserConflict) {
			conflicted++
		} else {
			t.Fatal(err)
		}
	}
	if succeeded != 1 || conflicted != 1 {
		t.Fatalf("concurrent edits: succeeded=%d conflict=%d", succeeded, conflicted)
	}
	if err := users.DeleteAs(UserDeleteInput{ID: target.ID, ExpectedUpdatedAt: &stamp}, admin.ID, 1); !errors.Is(err, ErrUserConflict) {
		t.Fatal("stale deletion allowed", err)
	}
	if _, err := users.SaveAs(UserInput{ID: admin.ID, Username: admin.Username, Enabled: true, Permissions: []string{"vpn.view"}}, admin.ID, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := users.SaveAs(UserInput{ID: target.ID, Username: target.Username, Enabled: true, Permissions: Permissions()}, admin.ID, 1); !errors.Is(err, ErrUserPermission) {
		t.Fatal("revoked actor updated user", err)
	}
	if err := users.DeleteAs(UserDeleteInput{ID: target.ID}, admin.ID, 1); !errors.Is(err, ErrUserPermission) {
		t.Fatal("revoked actor deleted user", err)
	}
}
