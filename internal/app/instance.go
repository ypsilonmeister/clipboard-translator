package app

import (
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"time"
)

const instanceBanner = "clipboard-translator\n"

// instanceListener manages the listening socket used to enforce a single application
// instance and to receive notifications from secondary instances.
type instanceListener struct {
	l       net.Listener
	done    chan struct{}
	mu      sync.Mutex
	onPing  func()
	pending bool
}

func newInstanceListener(l net.Listener, onPing func()) *instanceListener {
	inst := &instanceListener{
		l:      l,
		done:   make(chan struct{}),
		onPing: onPing,
	}
	go inst.serve()
	return inst
}

func (inst *instanceListener) serve() {
	defer close(inst.done)
	for {
		conn, err := inst.l.Accept()
		if err != nil {
			return
		}
		inst.handleConn(conn)
	}
}

func (inst *instanceListener) handleConn(conn net.Conn) {
	defer conn.Close()
	_ = conn.SetWriteDeadline(time.Now().Add(2 * time.Second))
	_, _ = conn.Write([]byte(instanceBanner))
	inst.triggerPing()
}

func (inst *instanceListener) triggerPing() {
	inst.mu.Lock()
	fn := inst.onPing
	if fn != nil {
		inst.mu.Unlock()
		fn()
		return
	}
	inst.pending = true
	inst.mu.Unlock()
}

func (inst *instanceListener) setOnPing(fn func()) {
	inst.mu.Lock()
	inst.onPing = fn
	pending := inst.pending
	inst.pending = false
	inst.mu.Unlock()
	if pending && fn != nil {
		fn()
	}
}

// Close closes the underlying network listener and waits for the accept loop to exit.
func (inst *instanceListener) Close() error {
	err := inst.l.Close()
	<-inst.done
	return err
}

// handshake connects to port on loopback, reads the instance banner, and returns
// whether it matches. Loopback only; no data is sent to the server.
func handshake(port int) (bool, error) {
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 2*time.Second)
	if err != nil {
		return false, err
	}
	defer conn.Close()

	_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, len(instanceBanner))
	if _, err := io.ReadFull(conn, buf); err != nil {
		return false, err
	}
	if string(buf) != instanceBanner {
		return false, errors.New("banner mismatch")
	}
	return true, nil
}

// checkInstance checks whether an instance is already running on port.
//
// If the port is free, it starts listening on loopback (127.0.0.1) and returns
// an instanceListener (the primary instance).
//
// If the port is in use and the handshake matches, it asks the primary instance
// to show itself (via the connection) and returns (nil, nil) so the caller can exit 0.
//
// If the port is held by another program or the banner does not match, it returns
// a clear, actionable error for the user.
func checkInstance(port int, onPing ...func()) (*instanceListener, error) {
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	l, err := net.Listen("tcp", addr)
	if err == nil {
		var fn func()
		if len(onPing) > 0 {
			fn = onPing[0]
		}
		return newInstanceListener(l, fn), nil
	}

	matched, _ := handshake(port)
	if matched {
		return nil, nil
	}
	return nil, fmt.Errorf("Port %d is used by another program. Set a different `port` in config.json.", port)
}
