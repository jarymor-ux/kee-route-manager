package amnezia

import (
 "context"
 "path/filepath"
 "strings"
 "testing"
)

func TestSameTunnelSourceAddressUsesDistinctMarkedPolicyRoutes(t *testing.T) {
 dir := t.TempDir()
 profiles, err := NewProfileStore(dir)
 if err != nil { t.Fatal(err) }
 first, err := profiles.Import("one", []byte(testAWGConfig))
 if err != nil { t.Fatal(err) }
 secondConfig := strings.Replace(testAWGConfig,"Endpoint = vpn.example.org:51820", "Endpoint = vpn2.example.org:51820",1)
 second, err := profiles.Import("two", []byte(secondConfig))
 if err != nil { t.Fatal(err) }
 runner := &fakeCommands{}
 runtime, err := NewRuntime(profiles,filepath.Join(dir,"runtime"),runner)
 if err != nil {t.Fatal(err)}
 if _,_,err = runtime.Ensure(context.Background(),first.ID);err != nil {t.Fatal(err)}
 if _,_,err = runtime.Ensure(context.Background(),second.ID);err != nil {t.Fatal(err)}
 firstMark,err:=tableNumber(first.ID)
 if err!=nil {t.Fatal(err)}
 secondMark,err:=tableNumber(second.ID)
 if err!=nil {t.Fatal(err)}
 if firstMark==secondMark {t.Fatal("profiles must have distinct marks")}
 commands:=strings.Join(runner.calls,"\n")
 for _,mark:=range []string{firstMark,secondMark} {
   if !strings.Contains(commands,"fwmark "+mark+" lookup "+mark) {
      t.Fatalf("AWG mark %s was not isolated: %s",mark,commands)
   }
 }
 if strings.Contains(commands,"from 10.66.66.2/32") {
   t.Fatal("source-IP policy rules cannot isolate AWG profiles sharing tunnel addresses")
 }
}
