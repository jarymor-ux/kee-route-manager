package components

import (
 "context"
 "errors"
 "testing"
 "time"
)

type fakeBackend struct {
 installed map[ID]string
 latest map[ID]string
 probes int
 downloads int
 installs int
}

func (f *fakeBackend) Installed(_ context.Context, id ID) (string, error) {
 if f.installed == nil { return "", nil }
 return f.installed[id], nil
}
func (f *fakeBackend) Latest(_ context.Context, id ID) (string, error) {
 f.probes++
 if f.latest == nil { return "", errors.New("offline") }
 return f.latest[id], nil
}
func (f *fakeBackend) Install(_ context.Context, id ID, version string) error {
 f.installs++
 if version == "" { return errors.New("empty version") }
 if f.installed == nil { f.installed = map[ID]string{} }
 f.installed[id] = version
 return nil
}

func findItem(t *testing.T, snapshot Snapshot, id ID) Item {
 t.Helper()
 for _, item := range snapshot.Items {
  if item.ID == id { return item }
 }
 t.Fatalf("component %s absent from snapshot", id)
 return Item{}
}

func TestWeeklyCheckAndManualOverrideNeverInstall(t *testing.T) {
 now := time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC)
 backend := &fakeBackend{installed: map[ID]string{Xray: "1.0.0"}, latest: map[ID]string{Xray: "1.1.0", XKeen: "2.0.0", AmneziaWG: "3.1.0"}}
 m, err := NewManager("keenetic", 7*24*time.Hour, backend, func() time.Time { return now })
 if err != nil { t.Fatal(err) }
 first, err := m.Check(context.Background(), false)
 if err != nil { t.Fatal(err) }
 if first.CheckedAt.IsZero() || len(first.Items) != 3 { t.Fatalf("incomplete initial check: %+v", first) }
 xray := findItem(t, first, Xray)
 if !xray.UpdateAvailable || xray.Missing { t.Fatalf("xray update not recognized: %+v", xray) }
 awg := findItem(t, first, AmneziaWG)
 if !awg.Missing { t.Fatalf("missing AmneziaWG not recognized: %+v", awg) }
 initial := backend.probes
 now = now.Add(6*24*time.Hour)
 cached, err := m.Check(context.Background(), false)
 if err != nil { t.Fatal(err) }
 if !cached.CheckedAt.Equal(first.CheckedAt) || backend.probes != initial { t.Fatal("check ignored configured interval") }
 manual, err := m.Check(context.Background(), true)
 if err != nil { t.Fatal(err) }
 if !manual.CheckedAt.Equal(now) || backend.probes <= initial { t.Fatal("manual check did not refresh") }
 if backend.installs != 0 || backend.downloads != 0 { t.Fatal("update discovery installed components") }
 now = now.Add(8*24*time.Hour)
 _, err = m.Check(context.Background(), false)
 if err != nil { t.Fatal(err) }
 if backend.probes <= initial+3 { t.Fatal("scheduled check did not resume after interval") }
}

func TestPlatformSpecificComponentsAndExplicitInstall(t *testing.T) {
 backend := &fakeBackend{installed: map[ID]string{Xray: "1.0.0"}, latest: map[ID]string{Xray:"1.2.0", AmneziaWG:"3.1.0"}}
 m, err := NewManager("linux-systemd", 7*24*time.Hour, backend, time.Now)
 if err != nil { t.Fatal(err) }
 snapshot, err := m.Check(context.Background(), true)
 if err != nil { t.Fatal(err) }
 if len(snapshot.Items) != 2 { t.Fatalf("linux must not require XKeen: %+v", snapshot.Items) }
 if _, err := m.Install(context.Background(), XKeen); err == nil { t.Fatal("unsupported platform installation accepted") }
 if _, err := m.Install(context.Background(), ID("arbitrary-command")); err == nil { t.Fatal("arbitrary component identifier accepted") }
 if backend.installs != 0 { t.Fatal("invalid install executed") }
 item, err := m.Install(context.Background(), AmneziaWG)
 if err != nil { t.Fatal(err) }
 if item.Missing || item.InstalledVersion != "3.1.0" || backend.installs != 1 { t.Fatalf("explicit installation failed: %+v", item) }
}

func TestInvalidIntervalsRefused(t *testing.T) {
 for _, d := range []time.Duration{0, -time.Second, time.Minute, 366*24*time.Hour} {
  if _, err := NewManager("keenetic", d, &fakeBackend{}, time.Now); err == nil { t.Fatalf("accepted interval %s", d) }
 }
}
