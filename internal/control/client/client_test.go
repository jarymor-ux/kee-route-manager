package client

import (
	"context"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestClientUsesDaemonSocket(t *testing.T) {
	d, e := os.MkdirTemp("", "krm-cl-")
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = os.RemoveAll(d) })
	path := filepath.Join(d, "socket")
	listener, e := net.Listen("unix", path)
	if e != nil {
		t.Fatal(e)
	}
	if e = os.Chmod(path, 0600); e != nil {
		t.Fatal(e)
	}
	defer listener.Close()
	defer os.Remove(path)
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/api/v1/actions/benchmark" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"accepted":true,"operation_id":"op-daemon"}`))
	})}
	go srv.Serve(listener)
	defer srv.Close()
	c := New(path)
	defer c.Close()
	b, e := c.Do(context.Background(), "POST", "/api/v1/actions/benchmark", nil)
	if e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(string(b), "op-daemon") {
		t.Fatal(string(b))
	}
}

func TestClientRejectsExposedOrSpoofedSocket(t *testing.T) {
	d, e := os.MkdirTemp("", "krm-unsafe-")
	if e != nil {
		t.Fatal(e)
	}
	defer os.RemoveAll(d)
	path := filepath.Join(d, "socket")
	l, e := net.Listen("unix", path)
	if e != nil {
		t.Fatal(e)
	}
	defer l.Close()
	if e = os.Chmod(path, 0666); e != nil {
		t.Fatal(e)
	}
	if e = verifySocket(path); e == nil {
		t.Fatal("accepted exposed socket")
	}
	if e = os.Chmod(path, 0600); e != nil {
		t.Fatal(e)
	}
	if e = os.Chmod(d, 0755); e != nil {
		t.Fatal(e)
	}
	if e = verifySocket(path); e == nil {
		t.Fatal("accepted exposed parent")
	}
	if e = os.Chmod(d, 0700); e != nil {
		t.Fatal(e)
	}
	_ = l.Close()
	if e = os.WriteFile(path, []byte("fake"), 0600); e != nil {
		t.Fatal(e)
	}
	if e = verifySocket(path); e == nil {
		t.Fatal("accepted regular file")
	}
}
