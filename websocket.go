// Package websocket is a go translation of websockify in noVNC.
package websocket

import (
	"container/list"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"io"
	"math/rand"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"
)

var (
	pingPayload = []byte("ping")
	pongPayload = []byte("pong")
)

// GUID is a UUID defined in RFC6455 1.2.
const GUID = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"

// Predefined message types, that's, opcode defined in RFC6455 section 5.2.
var (
	MsgTypeContinue = 0x0
	MsgTypeText     = 0x1
	MsgTypeBinary   = 0x2
	MsgTypeClose    = 0x8
	MsgTypePing     = 0x9
	MsgTypePong     = 0xa
)

// Predefined closure status code
var (
	CloseNormalClosure           = 1000
	CloseGoingAway               = 1001
	CloseProtocolError           = 1002
	CloseUnsupportedData         = 1003
	CloseNoStatusReceived        = 1005
	CloseAbnormalClosure         = 1006
	CloseInvalidFramePayloadData = 1007
	ClosePolicyViolation         = 1008
	CloseMessageTooBig           = 1009
	CloseMandatoryExtension      = 1010
	CloseInternalServerErr       = 1011
	CloseTLSHandshake            = 1015
)

// CloseStatus represents a close status.
type CloseStatus struct {
	Code   int
	Reason string
}

func (c CloseStatus) String() string {
	if c.Reason == "" {
		return fmt.Sprintf("[%d]", c.Code)
	}
	return fmt.Sprintf("[%d] %s", c.Code, c.Reason)
}

func (c CloseStatus) Error() string {
	return c.String()
}

// Option is used to set the Websocket.
type Option func(*Websocket)

// SetClient sets the websocket is a client.
func SetClient() Option {
	return func(ws *Websocket) {
		ws.isClient = true
	}
}

// SetMaxMessageSize sets the max message size of websocket.
func SetMaxMessageSize(size int) Option {
	return func(ws *Websocket) {
		ws.maxMsgSize = size
	}
}

// SetBufferSize sets the buffer size of websocket.
func SetBufferSize(size int) Option {
	return func(ws *Websocket) {
		ws.bufferSize = size + 16
	}
}

// SetSubprotocol sets the subprotocol of websocket.
func SetSubprotocol(protocol string) Option {
	return func(ws *Websocket) {
		ws.subprotocol = protocol
	}
}

// SetPingHander sets the ping handler.
func SetPingHander(h func(data []byte)) Option {
	return func(ws *Websocket) {
		ws.handlePing = h
	}
}

// SetPongHander sets the pong handler.
func SetPongHander(h func(data []byte)) Option {
	return func(ws *Websocket) {
		ws.handlePong = h
	}
}

// SetTimeout sets the timeout of the recv and send of websocket.
func SetTimeout(timeout time.Duration) Option {
	return func(ws *Websocket) {
		ws.timeout = timeout
	}
}

// Websocket implements a websocket interface.
//
// Notice: it does not support the extension.
type Websocket struct {
	conn net.Conn

	timeout     time.Duration
	isClient    bool
	maxMsgSize  int
	bufferSize  int
	subprotocol string

	opcode     int
	recvBuf    []byte
	recvQueue  *list.List
	recvBuffer []byte
	partBuffer []byte
	sendBuffer []byte

	isClose       bool
	sentClose     bool
	receivedClose bool
	closeStatus   CloseStatus

	handlePing func([]byte)
	handlePong func([]byte)
}

// NewWebsocket creates an websocket.
//
// Notice: the websocket handshake must have finished.
func NewWebsocket(conn net.Conn, opts ...Option) *Websocket {
	if conn == nil {
		panic(fmt.Errorf("the connection is nil"))
	}

	ws := Websocket{
		conn: conn,

		maxMsgSize: 65535,
		bufferSize: 2048,
		recvQueue:  list.New(),
	}

	for _, opt := range opts {
		opt(&ws)
	}

	ws.recvBuf = make([]byte, ws.bufferSize)
	ws.recvBuffer = make([]byte, 0, ws.bufferSize)
	ws.partBuffer = make([]byte, 0, ws.bufferSize)
	ws.sendBuffer = make([]byte, 0, ws.bufferSize)
	if ws.handlePing == nil {
		ws.handlePing = func(data []byte) {
			ws.Pong(data...)
			if ws.timeout > 0 {
				ws.SetDeadline(time.Now().Add(ws.timeout))
			}
		}
	}
	if ws.handlePong == nil {
		ws.handlePong = func(data []byte) {
			if ws.timeout > 0 {
				ws.SetDeadline(time.Now().Add(ws.timeout))
			}
		}
	}
	if ws.timeout > 0 {
		ws.conn.SetDeadline(time.Now().Add(ws.timeout))
	}

	return &ws
}

// LocalAddr returns the local network address.
func (ws *Websocket) LocalAddr() net.Addr {
	return ws.conn.LocalAddr()
}

// RemoteAddr returns the remote network address.
func (ws *Websocket) RemoteAddr() net.Addr {
	return ws.conn.RemoteAddr()
}

// SetDeadline sets the read and write deadlines associated
// with the connection. It is equivalent to calling both
// SetReadDeadline and SetWriteDeadline.
//
// A deadline is an absolute time after which I/O operations
// fail with a timeout (see type Error) instead of
// blocking. The deadline applies to all future and pending
// I/O, not just the immediately following call to Read or
// Write. After a deadline has been exceeded, the connection
// can be refreshed by setting a deadline in the future.
//
// An idle timeout can be implemented by repeatedly extending
// the deadline after successful Read or Write calls.
//
// A zero value for t means I/O operations will not time out.
func (ws *Websocket) SetDeadline(t time.Time) error {
	return ws.conn.SetDeadline(t)
}

// SetReadDeadline sets the deadline for future Read calls
// and any currently-blocked Read call.
// A zero value for t means Read will not time out.
func (ws *Websocket) SetReadDeadline(t time.Time) error {
	return ws.conn.SetReadDeadline(t)
}

// SetWriteDeadline sets the deadline for future Write calls
// and any currently-blocked Write call.
// Even if write times out, it may return n > 0, indicating that
// some of the data was successfully written.
// A zero value for t means Write will not time out.
func (ws *Websocket) SetWriteDeadline(t time.Time) error {
	return ws.conn.SetWriteDeadline(t)
}

// SetPingHander sets the ping handler.
func (ws *Websocket) SetPingHander(h func(data []byte)) {
	ws.handlePing = h
}

// SetPongHander sets the pong handler.
func (ws *Websocket) SetPongHander(h func(data []byte)) {
	ws.handlePong = h
}

// Ping sends a ping control message.
func (ws *Websocket) Ping(data ...byte) error {
	if len(data) == 0 {
		data = pingPayload
	}
	return ws.SendMsg(MsgTypePing, data)
}

// Pong sends a pong control message.
func (ws *Websocket) Pong(data ...byte) error {
	if len(data) == 0 {
		data = pongPayload
	}
	return ws.SendMsg(MsgTypePong, data)
}

// Close termiates the websocket connection gracefully, which will send
// a close frame to the peer then close the underlying connection.
func (ws *Websocket) Close(code int, reason string) error {
	if ws.sentClose {
		return ws.flush()
	}
	ws.sentClose = true

	if ws.closeStatus.Code == 0 {
		ws.closeStatus.Code = code
		ws.closeStatus.Reason = reason
	}

	var msg []byte
	if code > 0 {
		msg = make([]byte, len(reason)+2)
		binary.BigEndian.PutUint16(msg, uint16(code))
		if len(reason) > 0 {
			copy(msg[2:], reason)
		}
	}
	return ws.sendMsg(MsgTypeClose, msg, true)
}

// SendTextMsg sends a text message.
//
// If fin is false, the message will be fragmented.
//
// Notice: when sending fragmented message, you should not send another message
// until this fragmented message is sent totally.
func (ws *Websocket) SendTextMsg(message []byte, fin ...bool) error {
	return ws.SendMsg(MsgTypeText, message, fin...)
}

// SendBinaryMsg sends a binary message.
//
// If fin is false, the message will be fragmented.
//
// Notice: when sending fragmented message, you should not send another message
// until this fragmented message is sent totally.
func (ws *Websocket) SendBinaryMsg(message []byte, fin ...bool) error {
	return ws.SendMsg(MsgTypeBinary, message, fin...)
}

// SendMsg sends the message with the message type.
//
// If the message is empty, it will be ignored, but only flush the sent cache.
//
// If fin is false, the message will be fragmented.
//
// Notice: when sending fragmented message, you should not send another message
// until this fragmented message is sent totally.
//
// Notice: This method cannot send the close message. Please use Close().
func (ws *Websocket) SendMsg(msgType int, message []byte, fin ...bool) (err error) {
	if msgType == MsgTypeClose {
		panic(fmt.Errorf("Cannot send close message"))
	}

	if !ws.sentClose && len(message) > 0 {
		if err = ws.sendMsg(msgType, message, false, fin...); err != nil {
			return
		}
	}
	return ws.flush()
}

func (ws *Websocket) sendMsg(msgType int, message []byte, close bool,
	fin ...bool) (err error) {

	_fin := true
	if len(fin) > 0 && !fin[0] {
		_fin = false
	}

	if msgType > 0x7 {
		// RCF 6455 Section 5.4
		if !_fin {
			panic(fmt.Errorf("the control frame message cannot be fragmented"))
		}

		// RCF 6455 Section 5.5
		if len(message) > 125 {
			panic(fmt.Errorf("the control frame message too big"))
		}
	}

	if !close && ws.sentClose {
		ws.flush()
		return ws.closeStatus
	}

	// Sends a standard data message
	var frame []byte
	if ws.isClient {
		var mask [4]byte
		mask[0] = byte(rand.Intn(256))
		mask[1] = byte(rand.Intn(256))
		mask[2] = byte(rand.Intn(256))
		mask[3] = byte(rand.Intn(256))
		frame = ws.encodeHyBi(msgType, message, mask[:], _fin)
	} else {
		frame = ws.encodeHyBi(msgType, message, nil, _fin)
	}

	ws.sendBuffer = append(ws.sendBuffer, frame...)
	return ws.flush()
}

// Flush flushes the send cache.
func (ws *Websocket) Flush() error {
	return ws.flush()
}

// flush writes pending data to the socket.
func (ws *Websocket) flush() (err error) {
	if len(ws.sendBuffer) == 0 {
		return
	}

	var n int
	if n, err = ws.conn.Write(ws.sendBuffer); err != nil {
		if err == io.ErrShortWrite {
			err = nil
		}
	}
	bs := ws.sendBuffer[n:]
	copy(ws.sendBuffer, bs)
	ws.sendBuffer = ws.sendBuffer[:len(bs)]

	if ws.receivedClose && ws.sentClose {
		ws.close()
	}
	return
}

// Pending reports whether any websocket data is pending.
//
// This method will return true as long as there are websocket frames
// that have yet been processed. A single RecvMsg() from the underlying socket
// may return multiple websocket frames and it is therefore important
// that a caller continues calling RecvMsg() as long as pending() returns true.
//
// Note that this function merely tells if there are raw websocket frames
// pending. Those frames may not contain any application data.
func (ws *Websocket) Pending() bool {
	return ws.recvQueue.Len() > 0
}

// RecvMsg reads a single message from the websocket.
//
// This will return a single websocket message from the socket, which will be
// the empty string if the peer sent an empty message. If the socket is closed
// then None will be returned. The reason for the close is found in the
// 'close_code' and 'close_reason' properties.
func (ws *Websocket) RecvMsg() (msgType int, message []byte, err error) {
	if ws.receivedClose {
		ws.flush()
		return 0, nil, ws.closeStatus
	}

	if ws.Pending() {
		if msgType, message, err = ws.recvMsg(); err != nil || msgType > 0 {
			return
		}
	}

GOON:
	// Nope, let's try to read a bit
	if err = ws.recvFrames(); err != nil {
		return 0, nil, err
	}

	// Anything queued now?
	if msgType, message, err = ws.recvMsg(); err != nil || msgType > 0 {
		return
	}
	goto GOON
}

func (ws *Websocket) recvMsg() (int, []byte, error) {
	for ws.recvQueue.Len() > 0 {
		frame := ws.recvQueue.Remove(ws.recvQueue.Front()).(*frameT)
		// RFC 6455 Section 5.1: The server Must close the connection
		// upon receiving a frame that is not masked.
		if !ws.isClient && !frame.masked {
			ws.Close(CloseProtocolError, "Protocol error: Frame not masked")
			continue
		}

		// RFC 6455 Section 5.1: A client MUST close a connection
		// if it detects a masked frame.
		if ws.isClient && frame.masked {
			ws.Close(CloseProtocolError, "Protocol error: Frame masked")
			continue
		}

		if frame.opcode > 0x7 && len(frame.payload) > 125 {
			ws.Close(CloseMessageTooBig, "the control frame message too big.")
		}

		switch frame.opcode {
		case 0x0:
			if len(ws.partBuffer) == 0 || ws.opcode == 0 {
				ws.Close(CloseProtocolError, "Protocol error: Unexpected continuation frame")
				continue
			}
			ws.partBuffer = append(ws.partBuffer, frame.payload...)

			if frame.fin {
				opcode := ws.opcode
				msg := make([]byte, len(ws.partBuffer))
				copy(msg, ws.partBuffer)
				ws.partBuffer = ws.partBuffer[:0]
				ws.opcode = 0
				return opcode, msg, nil
			}
		case MsgTypeBinary, MsgTypeText:
			if len(ws.partBuffer) > 0 {
				ws.Close(CloseProtocolError, "Protocol error: Unexpected new frame")
				continue
			}
			if frame.fin {
				return frame.opcode, frame.payload, nil
			}
			if ws.opcode > 0 {
				ws.Close(CloseProtocolError, "Protocol error: more new fragmented")
				continue
			}
			ws.opcode = frame.opcode
			ws.partBuffer = append(ws.partBuffer, frame.payload...)
		case MsgTypeClose:
			if ws.receivedClose {
				continue
			}
			ws.receivedClose = true

			if ws.sentClose {
				ws.close()
				return 0, nil, ws.closeStatus
			}

			if !frame.fin {
				ws.Close(CloseUnsupportedData, "Unsupported: Fragmented close")
				continue
			}

			var code int
			var reason string
			if len(frame.payload) >= 2 {
				code = int(binary.BigEndian.Uint16(frame.payload[:2]))

				// RFC 6455 Section 8.1
				if len(frame.payload) > 2 {
					bs := frame.payload[2:]
					if !utf8.Valid(bs) {
						ws.Close(CloseProtocolError, "Protocol error: Invalid UTF-8 in close")
						continue
					}
					reason = string(bs)
				}
			}
			if code == 0 {
				ws.closeStatus.Code = CloseNoStatusReceived
				ws.closeStatus.Reason = "No close status code specified by peer"
			} else {
				ws.closeStatus.Code = code
				if reason != "" {
					ws.closeStatus.Reason = reason
				}
			}
			ws.Close(ws.closeStatus.Code, ws.closeStatus.Reason)
			return 0, nil, ws.closeStatus
		case MsgTypePing:
			if !frame.fin {
				ws.Close(CloseUnsupportedData, "Unsupported: Fragmented ping")
				continue
			}

			ws.handlePing(frame.payload)
		case MsgTypePong:
			if !frame.fin {
				ws.Close(CloseUnsupportedData, "Unsupported: Fragmented pong")
				continue
			}

			ws.handlePong(frame.payload)
		default:
			ws.Close(CloseUnsupportedData,
				fmt.Sprintf("Unsupported: Unknown opcode 0x%02x", frame.opcode))
		}
	}

	return 0, nil, nil
}

// recvFrames fetches more data from the socket to the buffer then decodes it.
func (ws *Websocket) recvFrames() error {
	read := true
	for read {
		n, err := ws.conn.Read(ws.recvBuf)
		if err != nil {
			ws.receivedClose = true
			ws.sentClose = true
			if ws.closeStatus.Code == 0 {
				ws.closeStatus.Code = CloseAbnormalClosure
				ws.closeStatus.Reason = err.Error()
			}
			ws.close()
			return ws.closeStatus
		}

		ws.recvBuffer = append(ws.recvBuffer, ws.recvBuf[:n]...)
		for {
			frame := ws.decodeHyBi(ws.recvBuffer)
			if frame == nil {
				if len(ws.recvBuffer) > ws.maxMsgSize {
					ws.Close(CloseMessageTooBig, "message too big")
					return ws.closeStatus
				}
				break
			}
			read = false
			bs := ws.recvBuffer[frame.length:]
			copy(ws.recvBuffer, bs)
			ws.recvBuffer = ws.recvBuffer[:len(bs)]
			ws.recvQueue.PushBack(frame)
		}
	}
	return nil
}

// close closes the underlying socket.
func (ws *Websocket) close() (err error) {
	if !ws.isClose {
		ws.isClose = true
		err = ws.conn.Close()
	}
	return
}

// mask a frame.
func (ws *Websocket) mask(buf, mask []byte) []byte {
	return ws.unmask(buf, mask)
}

// unmask a frame.
func (ws *Websocket) unmask(buf, mask []byte) []byte {
	if len(mask) != 4 {
		panic(fmt.Errorf("the length of mask is not 4"))
	}
	for i := range buf {
		buf[i] ^= mask[i%4]
	}
	return buf
}

// encodeHybi encodes a HyBi style WebSocket frame.
//
// Optional opcode:
//    0x0 - continuation
//    0x1 - text frame
//    0x2 - binary frame
//    0x8 - connection close
//    0x9 - ping
//    0xA - pong
func (ws *Websocket) encodeHyBi(opcode int, buf, mask []byte, fin bool) []byte {
	b1 := opcode & 0x0f
	if fin {
		b1 |= 0x80
	}

	maskBit := 0
	if len(mask) > 0 {
		maskBit = 0x80
		ws.mask(buf, mask)
	}

	var header [16]byte
	var headerLen int
	payloadLen := len(buf)
	if payloadLen <= 125 {
		headerLen = 2
		header[0] = byte(b1)
		header[1] = byte(payloadLen | maskBit)
	} else if payloadLen < 65536 {
		headerLen = 4
		header[0] = byte(b1)
		header[1] = byte(126 | maskBit)
		binary.BigEndian.PutUint16(header[2:], uint16(payloadLen))
	} else {
		headerLen = 10
		header[0] = byte(b1)
		header[1] = byte(127 | maskBit)
		binary.BigEndian.PutUint16(header[2:], uint16(payloadLen))
	}
	headers := header[:headerLen]

	bs := make([]byte, 0, len(headers)+len(mask)+len(buf))
	bs = append(bs, headers...)
	bs = append(bs, mask...)
	bs = append(bs, buf...)
	return bs
}

// decodeHyBi decodes HyBi style WebSocket packets.
func (ws *Websocket) decodeHyBi(buf []byte) *frameT {
	blen := len(buf)
	hlen := 2
	if blen < hlen {
		return nil
	}

	b1 := int(buf[0])
	b2 := int(buf[1])

	var frame frameT
	frame.opcode = b1 & 0x0f
	frame.fin = b1&0x80 > 0
	frame.masked = b2&0x80 > 0

	if frame.masked {
		hlen += 4
		if blen < hlen {
			return nil
		}
	}

	length := b2 & 0x7f
	if length == 126 {
		hlen += 2
		if blen < hlen {
			return nil
		}
		length = int(binary.BigEndian.Uint16(buf[2:4]))
	} else if length == 127 {
		hlen += 8
		if blen < hlen {
			return nil
		}
		length = int(binary.BigEndian.Uint64(buf[2:10]))
	}
	frame.length = hlen + length

	if blen < frame.length {
		return nil
	}

	if frame.masked {
		// unmask payload
		frame.payload = ws.unmask(buf[hlen:hlen+length], buf[hlen-4:hlen])
	} else {
		frame.payload = buf[hlen : hlen+length]
	}
	return &frame
}

type frameT struct {
	fin     bool
	masked  bool
	opcode  int
	length  int
	payload []byte
}

func headerContainValue(header http.Header, key string, value string) bool {
	for _, s := range header[key] {
		if strings.ToLower(s) == value {
			return true
		}
	}
	return false
}

// equalASCIIFold returns true if s is equal to t with ASCII case folding as
// defined in RFC 4790.
func equalASCIIFold(s, t string) bool {
	for s != "" && t != "" {
		sr, size := utf8.DecodeRuneInString(s)
		s = s[size:]
		tr, size := utf8.DecodeRuneInString(t)
		t = t[size:]
		if sr == tr {
			continue
		}
		if 'A' <= sr && sr <= 'Z' {
			sr = sr + 'a' - 'A'
		}
		if 'A' <= tr && tr <= 'Z' {
			tr = tr + 'a' - 'A'
		}
		if sr != tr {
			return false
		}
	}
	return s == t
}

func computeAcceptKey(challengeKey string) string {
	h := sha1.New()
	h.Write([]byte(challengeKey))
	h.Write([]byte(GUID))
	return base64.StdEncoding.EncodeToString(h.Sum(nil))
}

// Upgrader is used to upgrade HTTP connection to websocket.
type Upgrader struct {
	BufferSize     int
	MaxMessageSize int
	Subprotocols   []string
	Timeout        time.Duration

	CheckOrigin  func(*http.Request) bool
	Authenticate func(*http.Request) bool
}

func (u Upgrader) handleError(w http.ResponseWriter, r *http.Request, code int,
	reason string) (*Websocket, error) {
	w.WriteHeader(code)
	fmt.Fprintf(w, reason)
	return nil, fmt.Errorf(reason)
}

// subprotocols returns the subprotocols requested by the client in the
// Sec-Websocket-Protocol header.
func (u Upgrader) subprotocols(r *http.Request) []string {
	h := strings.TrimSpace(r.Header.Get("Sec-Websocket-Protocol"))
	if h == "" {
		return nil
	}
	protocols := strings.Split(h, ",")
	for i := range protocols {
		protocols[i] = strings.TrimSpace(protocols[i])
	}
	return protocols
}

func (u Upgrader) selectSubprotocol(protocols []string) string {
	if len(u.Subprotocols) > 0 {
		for _, serverProtocol := range u.Subprotocols {
			for _, clientProtocol := range protocols {
				if clientProtocol == serverProtocol {
					return clientProtocol
				}
			}
		}
	}
	return ""
}

func (u Upgrader) checkSameOrigin(r *http.Request) bool {
	origin := r.Header["Origin"]
	if len(origin) == 0 {
		return true
	}
	_u, err := url.Parse(origin[0])
	if err != nil {
		return false
	}
	return equalASCIIFold(_u.Host, r.Host)
}

// Upgrade upgrades the HTTP connection to websocket.
func (u Upgrader) Upgrade(w http.ResponseWriter, r *http.Request,
	responseHeader http.Header) (ws *Websocket, err error) {

	if r.Method != "GET" {
		return u.handleError(w, r, http.StatusMethodNotAllowed, "websocket: request method is not GET")
	}

	if !headerContainValue(r.Header, "Connection", "upgrade") {
		return u.handleError(w, r, http.StatusBadRequest,
			"websocket: missing or incorrect Connection header")
	}

	if !headerContainValue(r.Header, "Upgrade", "websocket") {
		return u.handleError(w, r, http.StatusBadRequest,
			"websocket: missing or incorrect Upgrader header")
	}

	if !headerContainValue(r.Header, "Sec-Websocket-Version", "13") {
		w.Header().Set("Sec-Websocket-Version", "13")
		return u.handleError(w, r, http.StatusUpgradeRequired,
			"websocket: only support protocol version 13")
	}

	// if len(r.Header.Get("Sec-WebSocket-Extensions")) > 0 {
	// 	return u.handleError(w, r, http.StatusBadRequest,
	// 		"websocket: not support extensions")
	// }

	if u.Authenticate != nil && !u.Authenticate(r) {
		return u.handleError(w, r, http.StatusUnauthorized,
			"websocket: authenticate failed")
	}

	challengeKey := r.Header.Get("Sec-Websocket-Key")
	if challengeKey == "" {
		return u.handleError(w, r, http.StatusBadRequest,
			"websocket: missing Sec-Websocket-Key header")
	}
	challengeKey = computeAcceptKey(challengeKey)

	var subprotocol string
	protocols := u.subprotocols(r)
	if len(protocols) > 0 {
		if subprotocol = u.selectSubprotocol(protocols); subprotocol == "" {
			return u.handleError(w, r, http.StatusBadRequest,
				"websocket: invalid protocol selected")
		}
	}

	checkOrigin := u.CheckOrigin
	if checkOrigin == nil {
		checkOrigin = u.checkSameOrigin
	}
	if !checkOrigin(r) {
		return u.handleError(w, r, http.StatusForbidden,
			"websocket: request origin not allowed")
	}

	h, ok := w.(http.Hijacker)
	if !ok {
		return u.handleError(w, r, http.StatusInternalServerError,
			"websocket: response does not implement http.Hijacker")
	}
	conn, brw, err := h.Hijack()
	if err != nil {
		return u.handleError(w, r, http.StatusInternalServerError, err.Error())
	}
	if brw.Reader.Buffered() > 0 {
		conn.Close()
		return nil, fmt.Errorf("websocket: client sent data before handshake is complete")
	}

	var cache [512]byte
	buf := cache[:0]
	buf = append(buf, "HTTP/1.1 101 Switching Protocols\r\n"...)
	buf = append(buf, "Upgrade: websocket\r\n"...)
	buf = append(buf, "Connection: Upgrade\r\n"...)
	buf = append(buf, "Sec-WebSocket-Accept: "...)
	buf = append(buf, challengeKey...)
	buf = append(buf, "\r\n"...)
	if subprotocol != "" {
		buf = append(buf, "Sec-WebSocket-Protocol: "...)
		buf = append(buf, subprotocol...)
		buf = append(buf, "\r\n"...)
	}
	for k, vs := range responseHeader {
		if k == "Sec-Websocket-Protocol" {
			continue
		}
		for _, v := range vs {
			buf = append(buf, k...)
			buf = append(buf, ": "...)
			for i := 0; i < len(v); i++ {
				b := v[i]
				if b <= 31 {
					// prevent response splitting.
					b = ' '
				}
				buf = append(buf, b)
			}
			buf = append(buf, "\r\n"...)
		}
	}
	buf = append(buf, "\r\n"...)

	// Clear deadlines set by HTTP server.
	conn.SetDeadline(time.Time{})
	if u.Timeout > 0 {
		conn.SetDeadline(time.Now().Add(u.Timeout))
	}
	if _, err = conn.Write(buf); err != nil {
		conn.Close()
		return nil, err
	}
	if u.Timeout > 0 {
		conn.SetDeadline(time.Time{})
	}

	maxMsgSize := u.MaxMessageSize
	if maxMsgSize < 1 {
		maxMsgSize = 65535
	}
	bufSize := u.BufferSize
	if bufSize < 1024 {
		bufSize = 1024
	}
	ws = NewWebsocket(conn, SetMaxMessageSize(maxMsgSize), SetTimeout(u.Timeout),
		SetBufferSize(bufSize), SetSubprotocol(subprotocol))
	return
}
