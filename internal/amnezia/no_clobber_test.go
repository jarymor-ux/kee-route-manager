package amnezia

import (
 "context"
 "path/filepath"
 "strings"
 "testing"
)

func TestAWGNeverReplacesForeignRoutingTable(t *testing.T) {
 root:=t.TempDir()
 store,err:=NewProfileStore(root)
 if err!=nil {t.Fatal(err)}
 item,err:=store.Import("Safe",[]byte(testAWGConfig))
 if err!=nil {t.Fatal(err)}
 runner:=&fakeCommands{}
 rt,err:=NewRuntime(store,filepath.Join(root,"runtime"),runner)
 if err!=nil {t.Fatal(err)}
 if _,_,err=rt.Ensure(context.Background(),item.ID);err!=nil{t.Fatal(err)}
 for _,cmd:=range runner.calls {
  if strings.Contains(cmd,"route replace") {t.Fatalf("overwrite of foreign route allowed: %s",cmd)}
 }
}
