package xray

import (
 "testing"
 "github.com/jarymor-ux/kee-route-manager/internal/model"
)

func TestAWGNodeUsesExistingXrayPoolWithDeviceBoundFreedom(t *testing.T){
 node:=model.Node{Protocol:"amneziawg",AWGProfileID:"0123456789abcdef0123456789abcdef",AWGLocalIPv4:"10.66.66.2"}
 out,err:=Outbound(node,"krm-slot-0")
 if err!=nil{t.Fatal(err)}
 if out["protocol"]!="freedom"||out["sendThrough"]!="10.66.66.2" {t.Fatalf("AWG freedom node malformed: %+v",out)}
 stream,ok:=out["streamSettings"].(map[string]any)
 if !ok {t.Fatal("AWG outbound has no socket settings")}
 sock,ok:=stream["sockopt"].(map[string]any)
 if !ok||sock["interface"]!="krmawg01234567" {t.Fatalf("unexpected AWG network binding: %+v",stream)}
 if _,err=Outbound(model.Node{Protocol:"amneziawg",AWGProfileID:"../../etc/passwd",AWGLocalIPv4:"10.66.66.2"},"probe");err==nil {t.Fatal("unsafe profile id accepted")}
}
