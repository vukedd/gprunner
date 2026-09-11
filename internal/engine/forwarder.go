package engine

import (
	"fmt"
	"io"
	"log/slog"
	"net"
	"sync"
	"time"
)

const brokerDialTimeout = 10 * time.Second

// Forwarder extends guest connections on the broker bridge to the broker itself.
type Forwarder struct {
	ln     net.Listener
	target string
	l      *slog.Logger

	mu     sync.Mutex
	conns  map[net.Conn]struct{}
	closed bool

	wg sync.WaitGroup
}

// StartForwarder binds listen and relays to target. It must be running before
// any layer boots, or the first guest to dial MQ_ADDR is refused.
func StartForwarder(l *slog.Logger, gatewayAddr, brokerAddr string) (*Forwarder, error) {
	ln, err := net.Listen("tcp", gatewayAddr)
	if err != nil {
		return nil, fmt.Errorf("listening on %s: %w", gatewayAddr, err)
	}

	f := &Forwarder{
		ln:     ln,
		target: brokerAddr,
		l:      l,
		conns:  make(map[net.Conn]struct{}),
	}

	f.wg.Add(1)
	go f.accept()

	l.Info("broker forwarder started", "listen", gatewayAddr, "broker", brokerAddr)
	return f, nil
}

func (f *Forwarder) accept() {
	defer f.wg.Done()

	for {
		// waits here
		guest, err := f.ln.Accept()
		if err != nil {
			// accept fails permanently once the listener is closed; any other
			// error belongs to that one connection, so keep serving
			if f.isClosed() {
				return
			}

			f.l.Warn("broker forwarder accept", "err", err)
			continue
		}

		f.wg.Add(1)
		go f.relay(guest)
	}
}

func (f *Forwarder) relay(guest net.Conn) {
	defer f.wg.Done()

	if !f.track(guest) { // shutting down; nothing to serve this connection with
		guest.Close()
		return
	}
	defer f.release(guest)

	broker, err := net.DialTimeout("tcp", f.target, brokerDialTimeout)
	if err != nil {
		f.l.Warn("broker forwarder dial", "broker", f.target, "err", err)
		return
	}

	if !f.track(broker) {
		broker.Close()
		return
	}
	defer f.release(broker)

	// wait for whichever side hangs up first; closing both then unblocks the
	// copy still running
	done := make(chan struct{}, 2)

	go func() {
		io.Copy(broker, guest)
		done <- struct{}{}
	}()

	go func() {
		io.Copy(guest, broker)
		done <- struct{}{}
	}()

	<-done
}

// track registers a connection so shutdown can tear it down, reporting false
// when the Forwarder is already closing.
func (f *Forwarder) track(c net.Conn) bool {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.closed {
		return false
	}

	f.conns[c] = struct{}{}
	return true
}

func (f *Forwarder) release(c net.Conn) {
	f.mu.Lock()
	delete(f.conns, c)
	f.mu.Unlock()

	c.Close()
}

func (f *Forwarder) isClosed() bool {
	f.mu.Lock()
	defer f.mu.Unlock()

	return f.closed
}

// Close stops accepting and tears down connections in flight. Waiting for
// clients to disconnect on their own is not an option: a subscriber holds its
// connection open for the lifetime of the layer.
func (f *Forwarder) Close() error {
	f.mu.Lock()
	if f.closed {
		f.mu.Unlock()
		return nil
	}
	f.closed = true

	err := f.ln.Close()
	for c := range f.conns {
		c.Close()
	}
	f.mu.Unlock()

	f.wg.Wait()

	return err
}
