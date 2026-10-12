package web

import (
 "io"
 "net/http"
 "strings"
 "github.com/jarymor-ux/kee-route-manager/internal/auth"
)

const maxAWGUploadBytes = 256 << 10

func (s *Server) awgImport(w http.ResponseWriter,r *http.Request,_ auth.Session){
 if s.awgProfiles==nil {jsonError(w,http.StatusNotImplemented,"AmneziaWG profiles unavailable");return}
 r.Body=http.MaxBytesReader(w,r.Body,maxAWGUploadBytes+4096)
 if err:=r.ParseMultipartForm(maxAWGUploadBytes+1024);err!=nil {
  jsonError(w,http.StatusBadRequest,"invalid or oversized AWG form");return
 }
 if r.MultipartForm!=nil {defer r.MultipartForm.RemoveAll()}
 name:=strings.TrimSpace(r.FormValue("name"))
 file,head,err:=r.FormFile("config")
 if err!=nil {jsonError(w,http.StatusBadRequest,"missing .conf file");return}
 defer file.Close()
 if head.Size>maxAWGUploadBytes {jsonError(w,http.StatusBadRequest,"AWG profile oversized");return}
 if name=="" {jsonError(w,http.StatusBadRequest,"profile name required");return}
 data,err:=io.ReadAll(io.LimitReader(file,maxAWGUploadBytes+1))
 if err!=nil||len(data)==0||len(data)>maxAWGUploadBytes {
  jsonError(w,http.StatusBadRequest,"invalid AWG profile size");return
 }
 profile,err:=s.awgProfiles.Import(name,data)
 if err!=nil {jsonError(w,http.StatusBadRequest,"invalid AmneziaWG configuration");return}
 if n,ok:=s.mgr.(interface{AWGProfilesChanged()});ok {n.AWGProfilesChanged()}
 w.Header().Set("Cache-Control","no-store")
 writeJSON(w,http.StatusCreated,map[string]any{"profile":profile})
}
func (s *Server) awgList(w http.ResponseWriter,r *http.Request,_ auth.Session){
 if s.awgProfiles==nil {jsonError(w,http.StatusNotImplemented,"AmneziaWG profiles unavailable");return}
 items,err:=s.awgProfiles.List()
 if err!=nil {jsonError(w,http.StatusServiceUnavailable,"AWG profile store unavailable");return}
 w.Header().Set("Cache-Control","no-store")
 writeJSON(w,http.StatusOK,map[string]any{"profiles":items})
}
func (s *Server) awgDelete(w http.ResponseWriter,r *http.Request,_ auth.Session){
 if s.awgProfiles==nil {jsonError(w,http.StatusNotImplemented,"AmneziaWG profiles unavailable");return}
 var q struct { ID string `json:"id"` }
 if err:=decodeBody(w,r,&q);err!=nil{return}
 if s.mgr!=nil {
	for _,node:=range s.mgr.Nodes() {
		if node.ID==q.ID && node.InPool {jsonError(w,http.StatusConflict,"profile is used by the active VPN pool");return}
	}
 }
 var deleteErr error
 if s.awgRuntime!=nil {
  deleteErr=s.awgRuntime.DeleteProfile(r.Context(),q.ID)
 }else{
  deleteErr=s.awgProfiles.Delete(q.ID)
 }
 if deleteErr!=nil {jsonError(w,http.StatusConflict,"AWG profile removal rejected");return}
 if n,ok:=s.mgr.(interface{AWGProfilesChanged()});ok {n.AWGProfilesChanged()}
 writeJSON(w,http.StatusOK,map[string]bool{"ok":true})
}
