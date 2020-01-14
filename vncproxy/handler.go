package vncproxy

import (
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/xgfone/websocket"
)

type peer struct {
	logger Logger
	source *websocket.Websocket
	target *net.TCPConn
	closed int32
	start  time.Time
}

func (p *peer) Close(addr string, err error) {
	if atomic.CompareAndSwapInt32(&p.closed, 0, 1) {
		p.target.Close()
		p.source.SendClose(websocket.CloseNormalClosure, "close")
		p.logger.Infof("close VNC: source=%s, target=%s, duration=%s, from=%s, err=%s",
			p.source.RemoteAddr().String(), p.target.RemoteAddr().String(),
			time.Now().Sub(p.start).String(), addr, err.Error())
	}
}

func newPeer(source *websocket.Websocket, target *net.TCPConn, now time.Time, logger Logger) *peer {
	return &peer{source: source, target: target, start: now, logger: logger}
}

// Logger represents a logger interface.
type Logger interface {
	Debugf(format string, args ...interface{})
	Infof(format string, args ...interface{})
	Warnf(format string, args ...interface{})
	Errorf(format string, args ...interface{})
}

// NewNoopLogger returns a Noop Logger, which does nothing.
func NewNoopLogger() Logger { return noopLogger{} }

type noopLogger struct{}

func (n noopLogger) Debugf(format string, args ...interface{}) {}
func (n noopLogger) Infof(format string, args ...interface{})  {}
func (n noopLogger) Warnf(format string, args ...interface{})  {}
func (n noopLogger) Errorf(format string, args ...interface{}) {}

// ProxyConfig is used to configure WsVncProxyHandler.
type ProxyConfig struct {
	Logger Logger

	// MaxMsgSize is used to control the size of the websocket message.
	// The default is 64KB.
	MaxMsgSize int

	// The timeout is used to dial and the interval time of Ping-Pong.
	// The default is 10s.
	Timeout time.Duration

	// Check whether the origin is allowed.
	//
	// The default is that the origin is allowed only when the header Origin
	// does not exist, or exists and is equal to the requesting host.
	CheckOrigin func(r *http.Request) bool

	// Return a backend address to connect to by the request.
	//
	// It's required.
	GetBackend func(r *http.Request) (addr string, err error)
}

// WebsocketVncProxyHandler is a VNC proxy handler based on websocket.
type WebsocketVncProxyHandler struct {
	connection int64
	peers      map[*peer]struct{}
	lock       sync.RWMutex

	logger     Logger
	timeout    time.Duration
	upgrader   websocket.Upgrader
	getBackend func(*http.Request) (string, error)
}

// NewWebsocketVncProxyHandler returns a new WebsocketVncProxyHandler.
//
// Notice: it only supports the binary protocol.
func NewWebsocketVncProxyHandler(conf ProxyConfig) *WebsocketVncProxyHandler {
	if conf.GetBackend == nil {
		panic(fmt.Errorf("GetBackend is required"))
	}
	if conf.MaxMsgSize <= 0 {
		conf.MaxMsgSize = 65535
	}
	if conf.Timeout <= 0 {
		conf.Timeout = time.Second * 10
	}
	if conf.Logger == nil {
		conf.Logger = NewNoopLogger()
	}

	handler := &WebsocketVncProxyHandler{
		peers:      make(map[*peer]struct{}, 1024),
		logger:     conf.Logger,
		timeout:    conf.Timeout,
		getBackend: conf.GetBackend,
		upgrader: websocket.Upgrader{
			MaxMsgSize:   conf.MaxMsgSize,
			Timeout:      conf.Timeout,
			Subprotocols: []string{"binary"},
			CheckOrigin:  conf.CheckOrigin,
		},
	}
	go handler.tick()
	return handler
}

// Connections returns the number of the current websockets.
func (h *WebsocketVncProxyHandler) Connections() int64 {
	return atomic.LoadInt64(&h.connection)
}

func (h *WebsocketVncProxyHandler) incConnection() {
	atomic.AddInt64(&h.connection, 1)
}

func (h *WebsocketVncProxyHandler) decConnection() {
	atomic.AddInt64(&h.connection, -1)
}

func (h *WebsocketVncProxyHandler) addPeer(p *peer) {
	h.lock.Lock()
	h.peers[p] = struct{}{}
	h.lock.Unlock()
}

func (h *WebsocketVncProxyHandler) delPeer(p *peer) {
	h.lock.Lock()
	delete(h.peers, p)
	h.lock.Unlock()
}

func (h *WebsocketVncProxyHandler) tick() {
	ticker := time.NewTicker(h.timeout * 8 / 10)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			h.lock.RLock()
			for peer := range h.peers {
				peer.source.SendPing(nil)
			}
			h.lock.RUnlock()
		}
	}
}

// ServeHTTP implements http.Handler, but it won't return until the connection
// has closed.
//
// Notice: for each connection, it will open other two goroutine,
// except itself goroutine, for reading from websocket and the backend VNC.
func (h *WebsocketVncProxyHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	starttime := time.Now()

	if strings.ToLower(r.Header.Get("Upgrade")) != "websocket" {
		w.Header().Set("Connection", "close")
		h.logger.Errorf("not websocket: client=%s, upgrade=%s", r.RemoteAddr, r.Header.Get("Upgrade"))
		return
	}

	backend, err := h.getBackend(r)
	if err != nil {
		w.Header().Set("Connection", "close")
		h.logger.Errorf("cannot get backend: client=%s, url=%s, err=%s", r.RemoteAddr, r.RequestURI, err)
		return
	} else if backend == "" {
		w.Header().Set("Connection", "close")
		h.logger.Errorf("no backend: client=%s, url=%s", r.RemoteAddr, r.RequestURI)
		return
	}

	ws, err := h.upgrader.Upgrade(w, r, nil)
	if err != nil {
		w.Header().Set("Connection", "close")
		h.logger.Errorf("cannot upgrade to websocket: client=%s, err=%s", r.RemoteAddr, err)
		return
	}

	h.logger.Infof("connecting to the VNC backend '%s' for '%s', url=%s", backend, r.RemoteAddr, r.RequestURI)

	c, err := net.DialTimeout("tcp", backend, h.timeout)
	if err != nil {
		h.logger.Errorf("cannot connect to the VNC backend '%s': %s", backend, err)
		ws.SendClose(websocket.CloseAbnormalClosure, "cannot connect to the backend")
		return
	}
	h.logger.Infof("connected to the VNC backend '%s' for '%s', cost=%s", backend,
		r.RemoteAddr, time.Now().Sub(starttime).String())

	h.incConnection()
	defer h.decConnection()

	peer := newPeer(ws, c.(*net.TCPConn), starttime, h.logger)
	h.addPeer(peer)
	defer h.delPeer(peer)

	go h.readSource(peer)
	h.readTarget(peer)
}

func (h *WebsocketVncProxyHandler) readSource(p *peer) {
	for {
		msgs, err := p.source.RecvMsg()
		if err != nil {
			p.Close(p.source.RemoteAddr().String(), err)
			return
		}

		for _, msg := range msgs {
			_, err = p.target.Write(msg.Data)
			// if err != nil {
			// 	p.Close(p.target.RemoteAddr().String(), err)
			// }
		}
	}
}

func (h *WebsocketVncProxyHandler) readTarget(p *peer) {
	for {
		buf := make([]byte, 2048)
		n, err := p.target.Read(buf)
		if err != nil {
			p.Close(p.target.RemoteAddr().String(), err)
			return
		}

		if err = p.source.SendBinaryMsg(buf[:n]); err != nil {
			p.Close(p.source.RemoteAddr().String(), err)
			return
		}
	}
}
