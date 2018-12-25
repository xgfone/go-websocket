package vncproxy

import (
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/xgfone/logger"
	"github.com/xgfone/websocket"
)

type peer struct {
	source *websocket.Websocket
	target *net.TCPConn

	sendSource chan []byte
	sendTarget chan []byte

	closed int32
}

func (p *peer) Close() error {
	if atomic.CompareAndSwapInt32(&p.closed, 0, 1) {
		p.source.Close(websocket.CloseNormalClosure, "close")
		p.target.Close()

		close(p.sendSource)
		close(p.sendTarget)

		logger.Info("Close Peer", "source", p.source.RemoteAddr().String(),
			"target", p.target.RemoteAddr().String())
	}
	return nil
}

func newPeer(source *websocket.Websocket, target *net.TCPConn) *peer {
	return &peer{
		source: source,
		target: target,

		sendSource: make(chan []byte, 256),
		sendTarget: make(chan []byte, 256),
	}
}

// ProxyConfig is used to configure WsVncProxyHandler.
type ProxyConfig struct {
	// MaxMsgSize is used to control the size of the websocket message.
	// The default is 65535.
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
	GetBackend func(r *http.Request) (addr string)
}

// WebsocketVncProxyHandler is a VNC proxy handler based on websocket.
type WebsocketVncProxyHandler struct {
	connection *int64
	maxMsgSize int64
	timeout    time.Duration
	upgrader   *websocket.Upgrader
	getBackend func(*http.Request) string
}

// NewWebsocketVncProxyHandler returns a new WebsocketVncProxyHandler.
//
// Notice: it only supports the binary protocol.
func NewWebsocketVncProxyHandler(conf ProxyConfig) WebsocketVncProxyHandler {
	if conf.GetBackend == nil {
		panic(fmt.Errorf("GetBackend is required"))
	}

	h := WebsocketVncProxyHandler{
		timeout:    conf.Timeout,
		getBackend: conf.GetBackend,
		maxMsgSize: int64(conf.MaxMsgSize),
		connection: new(int64),
	}

	if h.maxMsgSize < 1 {
		h.maxMsgSize = 65536
	}
	if h.timeout <= 0 {
		h.timeout = time.Second * 10
	}

	h.upgrader = &websocket.Upgrader{
		Timeout:      h.timeout,
		Subprotocols: []string{"binary"},
		CheckOrigin:  conf.CheckOrigin,
	}

	return h
}

// Connections returns the number of the current websockets.
func (h WebsocketVncProxyHandler) Connections() int64 {
	return atomic.LoadInt64(h.connection)
}

func (h WebsocketVncProxyHandler) incConnection() {
	atomic.AddInt64(h.connection, 1)
}

func (h WebsocketVncProxyHandler) decConnection() {
	atomic.AddInt64(h.connection, -1)
}

// ServeHTTP implements http.Handler, but it won't return until the connection
// has closed.
//
// Notice: for each connection, it will open other two goroutine,
// except itself goroutine, for reading from websocket and the backend VNC.
func (h WebsocketVncProxyHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	starttime := time.Now()

	if strings.ToLower(r.Header.Get("Upgrade")) != "websocket" {
		w.Header().Set("Connection", "close")
		logger.Error("not websocket: client=%s, upgrade=%s", r.RemoteAddr, r.Header.Get("Upgrade"))
		return
	}

	backend := h.getBackend(r)
	if backend == "" {
		w.Header().Set("Connection", "close")
		logger.Error("can't get backend: client=%s, url=%s", r.RemoteAddr, r.RequestURI)
		return
	}

	conn, err := h.upgrader.Upgrade(w, r, nil)
	if err != nil {
		w.Header().Set("Connection", "close")
		logger.Error("can't update to websocket: client=%s, err=%s", r.RemoteAddr, err)
		return
	}

	logger.Info("new websocket connection: client=%s, url=%s", r.RemoteAddr, r.RequestURI)
	logger.Info("connecting to the backend VNC: addr=%s", backend)

	c, err := net.DialTimeout("tcp", backend, h.timeout)
	if err != nil {
		logger.Error("can't connect to VNC: addr=%s, err=%s", backend, err)
		conn.Close(websocket.CloseAbnormalClosure, "cannot connect to the backend")
		return
	}
	logger.Info("connected to VNC: source=%s, target=%s, cost=%s", r.RemoteAddr, backend, time.Now().Sub(starttime).String())

	peer := newPeer(conn, c.(*net.TCPConn))

	go h.readSource(peer)
	go h.readTarget(peer)

	ticker := time.NewTicker(h.timeout * 8 / 10)
	defer ticker.Stop()

	h.incConnection()
	defer h.decConnection()

	for {
		select {
		case bs, ok := <-peer.sendSource:
			if !ok {
				return
			}
			err := peer.source.SendBinaryMsg(bs)
			if err != nil {
				logger.Error("can't send data to websocket: addr=%s, err=%s", r.RemoteAddr, err)
			}
		case bs, ok := <-peer.sendTarget:
			if !ok {
				return
			}

			_, err := peer.target.Write(bs)
			if err != nil {
				logger.Error("can't send data to VNC: addr=%s, err=%s", backend, err)
			}
		case <-ticker.C:
			err := peer.source.Ping()
			if err != nil {
				logger.Error("can't send Ping: addr=%s, err=%s", r.RemoteAddr, err)
			}
		}
	}
}

func (h WebsocketVncProxyHandler) readSource(p *peer) {
	addr := p.source.RemoteAddr().String()

	for {
		_, msg, err := p.source.RecvMsg()
		if err != nil {
			logger.Error("can't read from websocket: addr=%s, err=%s", addr, err)
			p.Close()
			return
		}
		p.sendTarget <- msg
	}
}

func (h WebsocketVncProxyHandler) readTarget(p *peer) {
	addr := p.target.RemoteAddr().String()

	for {
		buf := make([]byte, 2048)
		n, err := p.target.Read(buf)
		if err != nil {
			logger.Error("can't read from VNC: addr=%s, err=%s", addr, err)
			p.Close()
			return
		}

		p.sendSource <- buf[:n]
	}
}
