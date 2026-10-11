package web

import (
 "net/http"

 "github.com/jarymor-ux/kee-route-manager/internal/auth"
 "github.com/jarymor-ux/kee-route-manager/internal/components"
)

func (s *Server) componentStatus(w http.ResponseWriter,r *http.Request,_ auth.Session) {
 if s.components==nil {jsonError(w,http.StatusNotImplemented,"component manager unavailable");return}
 snapshot,err:=s.components.Check(r.Context(),false)
 if err!=nil {jsonError(w,http.StatusServiceUnavailable,"component status unavailable");return}
 w.Header().Set("Cache-Control","no-store")
 writeJSON(w,http.StatusOK,snapshot)
}

// An authenticated POST overrides the periodic refresh interval, but cannot install anything.
func (s *Server) componentCheck(w http.ResponseWriter,r *http.Request,_ auth.Session) {
 if s.components==nil {jsonError(w,http.StatusNotImplemented,"component manager unavailable");return}
 snapshot,err:=s.components.Check(r.Context(),true)
 if err!=nil {jsonError(w,http.StatusBadGateway,"component update check failed");return}
 w.Header().Set("Cache-Control","no-store")
 writeJSON(w,http.StatusOK,snapshot)
}

// Component installation is an independent privileged POST, never a side effect of a KRM update.
func (s *Server) componentInstall(w http.ResponseWriter,r *http.Request,_ auth.Session) {
 if s.components==nil {jsonError(w,http.StatusNotImplemented,"component manager unavailable");return}
 var q struct { ID components.ID `json:"id"` }
 if err:=decodeBody(w,r,&q);err!=nil{return}
 if q.ID!=components.Xray&&q.ID!=components.XKeen&&q.ID!=components.AmneziaWG {
  jsonError(w,http.StatusBadRequest,"unknown component")
  return
 }
 item,err:=s.components.Install(r.Context(),q.ID)
 if err!=nil {jsonError(w,http.StatusConflict,"component installation refused or failed");return}
 writeJSON(w,http.StatusOK,item)
}
