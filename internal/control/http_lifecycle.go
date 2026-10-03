package control

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/jarymor-ux/kee-route-manager/internal/config"
	"github.com/jarymor-ux/kee-route-manager/internal/tlsutil"
)

// Closing sockets does not join net/http handlers. Fence admission separately
// so shutdown cannot release controller ownership while a handler still runs.
type requestDrain struct {
	mu      sync.Mutex
	closing bool
	wg      sync.WaitGroup
}

func (d *requestDrain) wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		d.mu.Lock()
		if d.closing {
			d.mu.Unlock()
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		d.wg.Add(1)
		d.mu.Unlock()
		defer d.wg.Done()
		next.ServeHTTP(w, r)
	})
}

func (d *requestDrain) closeAdmission() { d.mu.Lock(); d.closing = true; d.mu.Unlock() }

// serveHTTP joins all listeners and handlers before returning. Trial and active
// phases share the outer daemon ownership locks but never overlap HTTP servers.
func serveHTTP(ctx context.Context, c config.Config, local, public http.Handler, generateTLS bool, activate <-chan struct{}, beforeServe func()) (bool, error) {
	var cert tls.Certificate
	var err error
	if c.API.Enabled && c.API.TLS.Enabled {
		if generateTLS {
			if err = tlsutil.EnsureTLS(c.API.TLS, c.API.Listen); err != nil {
				return false, err
			}
		}
		cert, err = tls.LoadX509KeyPair(c.API.TLS.CertFile, c.API.TLS.KeyFile)
		if err != nil {
			return false, err
		}
	}
	unix, err := ListenUnix(c.API.UnixSocket)
	if err != nil {
		return false, err
	}
	defer unix.Close()
	// net/http closes its listener before active handlers drain. Give it only
	// the socket, retaining the ownership lock until this function returns.
	listeners := []net.Listener{unix.(*lockedListener).Listener}
	handlers := []http.Handler{local}
	if c.API.Enabled {
		ln, err := net.Listen("tcp", c.API.Listen)
		if err != nil {
			return false, err
		}
		if c.API.TLS.Enabled {
			ln = tls.NewListener(ln, &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12})
		}
		defer ln.Close()
		listeners = append(listeners, ln)
		handlers = append(handlers, public)
	}
	if beforeServe != nil {
		beforeServe()
	}
	requestCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	results := make(chan error, len(listeners))
	servers := make([]*http.Server, len(listeners))
	var drain requestDrain
	for i, ln := range listeners {
		srv := &http.Server{Handler: drain.wrap(handlers[i]), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 5 * time.Minute, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 32 << 10,
			BaseContext: func(net.Listener) context.Context { return requestCtx }}
		servers[i] = srv
		go func() { results <- srv.Serve(ln) }()
	}
	activated := false
	received := 0
	select {
	case <-ctx.Done():
	case <-activate:
		activated = true
	case err = <-results:
		received++
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
	}
	drain.closeAdmission()
	// Let the activation response finish before closing its request context.
	if !activated {
		cancel()
	}
	shutdown, done := context.WithTimeout(context.Background(), 10*time.Second)
	defer done()
	for _, srv := range servers {
		if shutdownErr := srv.Shutdown(shutdown); shutdownErr != nil {
			_ = srv.Close()
			err = errors.Join(err, shutdownErr)
		}
	}
	cancel()
	for received < len(servers) {
		<-results
		received++
	}
	drain.wg.Wait()
	return activated && err == nil, err
}
