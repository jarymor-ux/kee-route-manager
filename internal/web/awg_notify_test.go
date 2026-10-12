package web

import (
 "bytes"
 "mime/multipart"
 "net/http"
 "net/http/httptest"
 "testing"
 "github.com/jarymor-ux/kee-route-manager/internal/amnezia"
 "github.com/jarymor-ux/kee-route-manager/internal/auth"
 "github.com/jarymor-ux/kee-route-manager/internal/model"
)

type testAWGNotifier struct{Controller; notifications int; nodes []model.NodeView}
func (n *testAWGNotifier) AWGProfilesChanged(){n.notifications++}
func (n *testAWGNotifier) Nodes()[]model.NodeView{return n.nodes}
func TestAWGImportQueuesRebenchmark(t *testing.T){
 store,_:=amnezia.NewProfileStore(t.TempDir())
 mgr:=&testAWGNotifier{}
 s:=&Server{awgProfiles:store,mgr:mgr}
 var body bytes.Buffer
 mp:=multipart.NewWriter(&body)
 _=mp.WriteField("name","Home")
 f,_:=mp.CreateFormFile("config","home.conf")
 _,_=f.Write([]byte(`[Interface]
PrivateKey = AQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQE=
Address = 10.66.66.2/32
[Peer]
PublicKey = AgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgI=
Endpoint = vpn.example.org:51820
AllowedIPs = 0.0.0.0/0
`))
 _=mp.Close()
 req:=httptest.NewRequest(http.MethodPost,"/api/v1/awg/import",&body)
 req.Header.Set("Content-Type",mp.FormDataContentType())
 w:=httptest.NewRecorder()
 s.awgImport(w,req,auth.Session{})
 if w.Code!=201||mgr.notifications!=1{t.Fatalf("import should queue retest: %d count=%d",w.Code,mgr.notifications)}
}
