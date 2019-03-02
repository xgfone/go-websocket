package vncproxy

import (
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/xgfone/logger"
	"github.com/xgfone/websocket"
)

type peer struct {
	source *websocket.Websocket
	target *net.TCPConn
	closed int32
	start  time.Time
}

func (p *peer) Close() {
	if atomic.CompareAndSwapInt32(&p.closed, 0, 1) {
		p.source.SendClose(websocket.CloseNormalClosure, "close")
		p.target.Close()
		logger.Info("Close VNC: source=%s, target=%s, duration=%s",
			p.source.RemoteAddr().String(), p.target.RemoteAddr().String(),
			time.Now().Sub(p.start).String())
	}
}

func newPeer(source *websocket.Websocket, target *net.TCPConn, now time.Time) *peer {
	return &peer{source: source, target: target, start: now}
}

// ProxyConfig is used to configure WsVncProxyHandler.
type ProxyConfig struct {
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

	handler := &WebsocketVncProxyHandler{
		peers:      make(map[*peer]struct{}, 1024),
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
		logger.Error("not websocket: client=%s, upgrade=%s",
			r.RemoteAddr, r.Header.Get("Upgrade"))
		return
	}

	backend, err := h.getBackend(r)
	if err != nil {
		w.Header().Set("Connection", "close")
		logger.Error("can't get backend: client=%s, url=%s, err=%s",
			r.RemoteAddr, r.RequestURI, err)
		return
	} else if backend == "" {
		w.Header().Set("Connection", "close")
		logger.Error("can't get backend: client=%s, url=%s",
			r.RemoteAddr, r.RequestURI)
		return
	}

	ws, err := h.upgrader.Upgrade(w, r, nil)
	if err != nil {
		w.Header().Set("Connection", "close")
		logger.Error("can't update to websocket: client=%s, err=%s",
			r.RemoteAddr, err)
		return
	}

	logger.Info("new websocket connection: client=%s, url=%s", r.RemoteAddr, r.RequestURI)
	logger.Info("connecting to the backend VNC: addr=%s", backend)

	c, err := net.DialTimeout("tcp", backend, h.timeout)
	if err != nil {
		logger.Error("can't connect to VNC: addr=%s, err=%s", backend, err)
		ws.SendClose(websocket.CloseAbnormalClosure, "cannot connect to the backend")
		return
	}
	logger.Info("connected to VNC: source=%s, target=%s, cost=%s",
		r.RemoteAddr, backend, time.Now().Sub(starttime).String())

	h.incConnection()
	defer h.decConnection()

	peer := newPeer(ws, c.(*net.TCPConn), starttime)
	h.addPeer(peer)
	defer h.delPeer(peer)

	go h.readSource(peer)
	h.readTarget(peer)
}

func (h *WebsocketVncProxyHandler) readSource(p *peer) {
	for {
		msgs, err := p.source.RecvMsg()
		if err != nil {
			logger.Error("can't read from websocket: addr=%s, err=%s",
				p.source.RemoteAddr().String(), err)
			p.Close()
			return
		}

		for _, msg := range msgs {
			_, err := p.target.Write(msg.Data)
			if err != nil {
				logger.Error("can't send data to VNC: addr=%s, err=%s",
					p.target.RemoteAddr().String(), err)
			}
		}
	}
}

func (h *WebsocketVncProxyHandler) readTarget(p *peer) {
	for {
		buf := make([]byte, 2048)
		n, err := p.target.Read(buf)
		if err != nil {
			logger.Error("can't read from VNC: addr=%s, err=%s",
				p.target.RemoteAddr().String(), err)
			p.Close()
			return
		}

		if err = p.source.SendBinaryMsg(buf[:n]); err != nil {
			logger.Error("can't send data to websocket: addr=%s, err=%s",
				p.source.RemoteAddr().String(), err)
			p.Close()
			return
		}
	}
}
