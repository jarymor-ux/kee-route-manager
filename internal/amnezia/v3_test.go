package amnezia

import (
 "strings"
 "testing"
)

func TestAWG31DeviceSectionPreservesHeaderProtectionAndTimings(t *testing.T){
 raw:=strings.Replace(testAWGConfig,"[Peer]", "[Device]\nHeaderProtectionKey = AQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQE=\nContentPaddingAddition = 10-20\nRekeyAfterTime = 30-40\n[Peer]",1)
 p,err:=ParseConfig([]byte(raw))
 if err!=nil {t.Fatalf("AWG3.1 [Device] fields rejected: %v",err)}
 if p.Interface.HeaderProtectionKey==""||p.Interface.Additional["contentpaddingaddition"]!="10-20" {t.Fatalf("device params lost: %+v",p.Interface)}
 sanitized,err:=isolatedConfig([]byte(raw))
 if err!=nil {t.Fatal(err)}
 if !strings.Contains(string(sanitized),"[Interface]\nTable = off\n") {t.Fatal("Table = off must belong to Interface section even with Device section")}
 if !strings.Contains(string(sanitized),"HeaderProtectionKey") {t.Fatal("v3.1 obfuscation silently discarded")}
}
