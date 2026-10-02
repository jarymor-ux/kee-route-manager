package control

import (
	"context"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/jarymor-ux/kee-route-manager/internal/control/client"
)

func TestServeHTTPRetainsOwnershipUntilCanceledHandlerReturns(t *testing.T) {
	c := isolatedDaemonConfig(t)
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	defer unblock()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(entered)
		// Model an admitted operation which still owns data while a dependency
		// ignores cancellation. Closing its HTTP connection does not join it.
		<-release
		w.WriteHeader(http.StatusNoContent)
	})
	listening := make(chan struct{})
	go func() {
		_, err := serveHTTP(ctx, c, handler, handler, false, nil, func() { close(listening) })
		result <- err
	}()
	select {
	case <-listening:
	case err := <-result:
		t.Fatalf("server startup failed: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("listener did not start")
	}
	cl := client.New(c.API.UnixSocket)
	defer cl.Close()
	requestDone := make(chan struct{})
	go func() { defer close(requestDone); _, _ = cl.Do(context.Background(), "GET", "/blocking", nil) }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("handler did not start")
	}
	cancel()
	// Exceed the actual 10-second graceful shutdown deadline. A timeout must
	// close sockets but must not relinquish ownership to another daemon phase.
	select {
	case err := <-result:
		t.Fatalf("returned while admitted handler was still running: %v", err)
	case <-time.After(11 * time.Second):
	}
	if competing, err := ListenUnix(c.API.UnixSocket); err == nil {
		_ = competing.Close()
		t.Fatal("a second listener acquired ownership during handler drain")
	}
	unblock()
	select {
	case err := <-result:
		if err == nil {
			t.Fatal("shutdown timeout was not reported")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("server did not finish after handler returned")
	}
	<-requestDone
}
