package amnezia

import (
    "bytes"
    "os"
    "path/filepath"
    "strings"
    "testing"
)

func TestProfileStoreImportListReadAndRemove(t *testing.T) {
    root:=t.TempDir()
    store,err:=NewProfileStore(root)
    if err!=nil {t.Fatal(err)}
    item,err:=store.Import("  Home VPN  ", []byte(testAWGConfig))
    if err!=nil {t.Fatal(err)}
    if item.Name!="Home VPN" || item.ID=="" || len(item.ID)!=32 {t.Fatalf("invalid profile metadata: %+v",item)}
    if strings.Contains(item.Name,"AQEBA") {t.Fatal("secret in name")}
    listed,err:=store.List()
    if err!=nil||len(listed)!=1||listed[0].ID!=item.ID {t.Fatalf("invalid list: %+v %v",listed,err)}
    raw,err:=store.Read(item.ID)
    if err!=nil||!bytes.Equal(raw,[]byte(testAWGConfig)) {t.Fatal("read must preserve exact imported .conf",err)}
    info,err:=os.Stat(filepath.Join(root,"awg-profiles",item.ID+".conf"))
    if err!=nil||info.Mode().Perm()!=0600 {t.Fatalf("profile secrets must be private: %v",err)}
    if _,err=store.Import("Home VPN",[]byte(testAWGConfig));err!=nil {t.Fatal("idempotent reimport should work",err)}
    listed,err=store.List()
    if err!=nil||len(listed)!=1 {t.Fatal("duplicate import produced extra profile",err)}
    if err=store.Delete(item.ID);err!=nil {t.Fatal(err)}
    listed,err=store.List()
    if err!=nil||len(listed)!=0 {t.Fatal("profile still exists",err)}
}

func TestProfileStoreRejectsInvalidIDAndScriptDirectives(t *testing.T) {
    store,err:=NewProfileStore(t.TempDir())
    if err!=nil {t.Fatal(err)}
    if _,err=store.Import("invalid",[]byte(strings.Replace(testAWGConfig,"MTU = 1280","PostUp = touch /tmp/bad",1)));err==nil {t.Fatal("shell directives accepted")}
    if _,err=store.Import("",[]byte(testAWGConfig));err==nil {t.Fatal("empty name accepted")}
    for _,id:=range []string{"../../secret","../../../etc/passwd","unexpected","aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa.conf"} {
        if _,err=store.Read(id);err==nil {t.Fatalf("unsafe read %q",id)}
        if err=store.Delete(id);err==nil {t.Fatalf("unsafe delete %q",id)}
    }
}

func TestProfileStoreRefusesSymlinkedProfileDirectory(t *testing.T) {
    root:=t.TempDir()
    target:=t.TempDir()
    if err:=os.Symlink(target,filepath.Join(root,"awg-profiles"));err!=nil {t.Fatal(err)}
    if _,err:=NewProfileStore(root);err==nil {t.Fatal("symlinked secret directory accepted")}
}
