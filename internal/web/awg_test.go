package web

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jarymor-ux/kee-route-manager/internal/amnezia"
	"github.com/jarymor-ux/kee-route-manager/internal/auth"
)

func TestAWGUploadListDeleteLifecycleAndNoSecretsInResponse(t *testing.T) {
	store, err := amnezia.NewProfileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{awgProfiles: store}
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	_ = mw.WriteField("name", "Test VPN")
	part, err := mw.CreateFormFile("config", "test.conf")
	if err != nil {
		t.Fatal(err)
	}
	raw := []byte(`[Interface]
PrivateKey = AQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQE=
Address = 10.8.0.2/32
Jc = 5
Jmin = 20
Jmax = 50
S1 = 12
S2 = 12
S3 = 12
S4 = 12
H1 = 101
H2 = 201
H3 = 301
H4 = 401
[Peer]
PublicKey = AgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgI=
Endpoint = vpn.example.org:51820
AllowedIPs = 0.0.0.0/0
`)
	_, _ = part.Write(raw)
	_ = mw.Close()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/awg/import", &body)
	request.Header.Set("Content-Type", mw.FormDataContentType())
	out := httptest.NewRecorder()
	s.awgImport(out, request, auth.Session{})
	if out.Code != http.StatusCreated {
		t.Fatalf("import status=%d %s", out.Code, out.Body)
	}
	if strings.Contains(out.Body.String(), "AQEBA") || strings.Contains(out.Body.String(), "AgICAg") {
		t.Fatal("key leaked in import response")
	}
	var result struct {
		Profile amnezia.ProfileInfo `json:"profile"`
	}
	if err = json.Unmarshal(out.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	out = httptest.NewRecorder()
	s.awgList(out, httptest.NewRequest(http.MethodGet, "/api/v1/awg/profiles", nil), auth.Session{})
	if out.Code != 200 || !strings.Contains(out.Body.String(), result.Profile.ID) || strings.Contains(out.Body.String(), "AQEBA") {
		t.Fatalf("list %d %s", out.Code, out.Body)
	}
	out = httptest.NewRecorder()
	s.awgDelete(out, httptest.NewRequest(http.MethodPost, "/api/v1/awg/delete", strings.NewReader(`{"id":"`+result.Profile.ID+`"}`)), auth.Session{})
	if out.Code != 200 {
		t.Fatalf("delete %d %s", out.Code, out.Body)
	}
	if _, err = store.Read(result.Profile.ID); !os.IsNotExist(err) {
		t.Fatalf("deleted profile remains: %v", err)
	}
}

func TestAWGUploadRejectsBadInputWithoutSecrets(t *testing.T) {
	store, _ := amnezia.NewProfileStore(t.TempDir())
	s := &Server{awgProfiles: store}
	for _, payload := range []string{`{"config":"PostUp = /bin/sh"}`, strings.Repeat("x", 270000)} {
		out := httptest.NewRecorder()
		s.awgImport(out, httptest.NewRequest(http.MethodPost, "/api/v1/awg/import", strings.NewReader(payload)), auth.Session{})
		if out.Code < 400 {
			t.Fatal("accepted untrusted upload")
		}
	}
	if path := filepath.Join(t.TempDir(), "dummy"); path == "" {
		t.Fatal("unreachable")
	}
	_ = io.EOF
}
