package components

import (
 "context"
 "path/filepath"
 "testing"
 "time"
)

func TestWeeklyCacheSurvivesDaemonRestart(t *testing.T) {
 now:=time.Date(2026,10,12,8,0,0,0,time.UTC)
 path:=filepath.Join(t.TempDir(),"components-status.json")
 firstBackend:=&fakeBackend{latest:map[ID]string{Xray:"1.0",AmneziaWG:"3.1"}}
 first,err:=NewManager("openwrt",7*24*time.Hour,firstBackend,func() time.Time{return now})
 if err!=nil {t.Fatal(err)}
 if err:=first.SetCacheFile(path);err!=nil {t.Fatal(err)}
 firstSnapshot,err:=first.Check(context.Background(),false)
 if err!=nil {t.Fatal(err)}
 now=now.Add(24*time.Hour)
 secondBackend:=&fakeBackend{latest:map[ID]string{Xray:"1.1",AmneziaWG:"3.2"}}
 restarted,err:=NewManager("openwrt",7*24*time.Hour,secondBackend,func() time.Time{return now})
 if err!=nil {t.Fatal(err)}
 if err:=restarted.SetCacheFile(path);err!=nil {t.Fatal(err)}
 cached,err:=restarted.Check(context.Background(),false)
 if err!=nil {t.Fatal(err)}
 if secondBackend.probes!=0 || !cached.CheckedAt.Equal(firstSnapshot.CheckedAt) {t.Fatal("restart lost update-check cadence")}
 now=now.Add(7*24*time.Hour)
 refreshed,err:=restarted.Check(context.Background(),false)
 if err!=nil {t.Fatal(err)}
 if secondBackend.probes==0 || !refreshed.CheckedAt.Equal(now) {t.Fatal("weekly check did not expire")}
}
