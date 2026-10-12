package components

import (
 "context"
 "encoding/json"
 "errors"
 "fmt"
 "io"
 "net/http"
 "os"
 "os/exec"
 "path/filepath"
 "regexp"
 "strings"
 "time"

 "github.com/jarymor-ux/kee-route-manager/internal/config"
)

var versionRegexp=regexp.MustCompile(`[vV]?([0-9]+(?:\.[0-9]+){1,3}(?:-[a-zA-Z0-9.]+)?)`)

// SystemBackend reads only allowlisted runtime binaries and trusted release metadata.
// Installing binaries is intentionally unsupported until signatures and rollback are implemented.
type SystemBackend struct {
 cfg config.Config
 client *http.Client
}

func NewSystemBackend(c config.Config, client *http.Client) *SystemBackend {
 if client==nil {client=&http.Client{Timeout:10*time.Second}}
 return &SystemBackend{cfg:c,client:client}
}
func (b *SystemBackend) binary(id ID) string {
 switch id {
 case Xray:return b.cfg.Xray.Binary
 case XKeen:return b.cfg.Platform.Keenetic.XKeenBinary
 case AmneziaWG:
  if b.cfg.Platform.Kind=="keenetic" {return "/opt/bin/amneziawg-go"}
  if b.cfg.Platform.Kind=="openwrt" {return "/usr/bin/amneziawg-go"}
  return "/usr/local/bin/amneziawg-go"
 default:return ""
 }
}

func (b *SystemBackend) Installed(ctx context.Context,id ID)(string,error){
 binary:=b.binary(id)
 if binary==""||!filepath.IsAbs(binary){return "",errors.New("unknown runtime executable")}
 info,err:=os.Stat(binary)
 if errors.Is(err,os.ErrNotExist){return "",nil}
 if err!=nil{return "",err}
 if !info.Mode().IsRegular() || info.Mode().Perm()&0111==0 {
  return "",errors.New("runtime binary is not an executable regular file")
 }
 arg:="version"
 if id==XKeen {arg="-v"}
 if id==AmneziaWG {return "present",nil} // AWG has no portable version subcommand.
 probeCtx,cancel:=context.WithTimeout(ctx,3*time.Second)
 defer cancel()
 output,err:=exec.CommandContext(probeCtx,binary,arg).CombinedOutput()
 if err!=nil{return "present",nil}
 found:=versionRegexp.FindStringSubmatch(string(output))
 if len(found)<2{return "present",nil}
 return found[1],nil
}

func (b *SystemBackend) Latest(ctx context.Context,id ID)(string,error){
 var path string
 switch id{
 case Xray:path="/repos/XTLS/Xray-core/releases/latest"
 case XKeen:path="/repos/Corvus-Malus/XKeen/releases/latest"
 case AmneziaWG:path="/repos/amnezia-vpn/amneziawg-go/releases/latest"
 default:return "",errors.New("unsupported component")
 }
 req,err:=http.NewRequestWithContext(ctx,http.MethodGet,"https://api.github.com"+path,nil)
 if err!=nil{return "",err}
 req.Header.Set("Accept","application/vnd.github+json")
 req.Header.Set("User-Agent","Kee-Route-Manager")
 res,err:=b.client.Do(req)
 if err!=nil{return "",err}
 defer res.Body.Close()
 if res.StatusCode!=http.StatusOK {return "",fmt.Errorf("upstream release lookup HTTP %d",res.StatusCode)}
 data,err:=io.ReadAll(io.LimitReader(res.Body,16*1024))
 if err!=nil{return "",err}
 var release struct {Tag string `json:"tag_name"`}
 if err=json.Unmarshal(data,&release);err!=nil{return "",err}
 version:=strings.TrimPrefix(release.Tag,"v")
 if !versionRegexp.MatchString(version)||len(version)>80{return "",errors.New("invalid upstream version")}
 return version,nil
}

func (*SystemBackend) Install(context.Context,ID,string)error{
 return errors.New("automatic runtime installation unavailable: verified installers and rollback not yet implemented")
}
