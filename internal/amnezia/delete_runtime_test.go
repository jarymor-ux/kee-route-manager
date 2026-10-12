package amnezia

import(
 "context"
 "os"
 "path/filepath"
 "testing"
)

func TestAWGRuntimeRemoveAtomicallyTearsDownTunAndProfile(t *testing.T){
 root:=t.TempDir()
 profiles,_:=NewProfileStore(root)
 item,_:=profiles.Import("Disposable",[]byte(testAWGConfig))
 fake:=&fakeCommands{}
 runtime,_:=NewRuntime(profiles,filepath.Join(root,"run"),fake)
 if _,_,err:=runtime.Ensure(context.Background(),item.ID);err!=nil{t.Fatal(err)}
 if err:=runtime.DeleteProfile(context.Background(),item.ID);err!=nil{t.Fatal(err)}
 if _,err:=profiles.Read(item.ID);!os.IsNotExist(err){t.Fatalf("profile remains: %v",err)}
 if _,_,err:=runtime.Ensure(context.Background(),item.ID);err==nil{t.Fatal("deleted profile was started")}
}
