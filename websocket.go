// Copyright 2023 xgfone
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Package websocket is websocket library implemented by Go, which is inspired
// by websockify in noVNC.
package websocket

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"math/rand"
	"net"
	"sync/atomic"
	"time"
	"unicode/utf8"
)

var (
	pingPayload = []byte("ping")
	pongPayload = []byte("pong")
)

// GUID is a UUID defined in RFC6455 1.2.
const GUID = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"

// Predefine message types, that's, opcode defined in RFC6455 section 5.2.
const (
	MsgTypeContinue = 0x0
	MsgTypeText     = 0x1
	MsgTypeBinary   = 0x2
	MsgTypeClose    = 0x8
	MsgTypePing     = 0x9
	MsgTypePong     = 0xa
)

// Predefine closure status code
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

// Message represents a websocket message.
type Message struct {
	// The type of the message, MsgTypeText or MsgTypeBinary
	Type int

	// The message body.
	//
	// For the fragmented message, it contains the datas of all the fragmented.
	Data []byte
}

// Websocket implements a websocket interface.
//
// Notice:
//  1. It does not support the extension.
//  2. The sending functions SendXxx can be called concurrently with the
//     unfragmented messages, but the receiving functions, RecvMsg or Run,
//     cannot be done like that.
type Websocket struct {
	conn net.Conn

	timeout     time.Duration
	isClient    bool
	bufferSize  int
	maxMsgSize  int
	subprotocol string
	closeNotice func()
	handlePing  func(*Websocket, []byte)
	handlePong  func(*Websocket, []byte)

	opcode     int
	closed     int32
	recvBuffer *bytes.Buffer
	msgBuffer  *bytes.Buffer
}

// NewWebsocket creates an websocket.
//
// Notice: the websocket handshake must have finished.
func NewWebsocket(conn net.Conn) *Websocket {
	if conn == nil {
		panic(fmt.Errorf("the connection is nil"))
	}

	wst := Websocket{conn: conn}
	wst.SetBufferSize(2048).
		SetPingHander(func(ws *Websocket, data []byte) {
			ws.SendPong(data)
			ws.SetReadDeadlineByDuration(ws.timeout)
		}).
		SetPongHander(func(ws *Websocket, data []byte) {
			ws.SetDeadlineByDuration(ws.timeout)
		})

	return &wst
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

// SetDeadlineByDuration is equal to SetDeadline(time.Now().Add(d)) if d > 0.
func (ws *Websocket) SetDeadlineByDuration(d time.Duration) error {
	if d == 0 {
		return nil
	}
	return ws.SetDeadline(time.Now().Add(d))
}

// SetReadDeadlineByDuration is equal to SetReadDeadline(time.Now().Add(d)) if d > 0.
func (ws *Websocket) SetReadDeadlineByDuration(d time.Duration) error {
	if d == 0 {
		return nil
	}
	return ws.SetReadDeadline(time.Now().Add(d))
}

// SetWriteDeadlineByDuration is equal to SetWriteDeadline(time.Now().Add(d)) if d > 0.
func (ws *Websocket) SetWriteDeadlineByDuration(d time.Duration) error {
	if d == 0 {
		return nil
	}
	return ws.SetWriteDeadline(time.Now().Add(d))
}

// SetPingHander sets the ping handler.
func (ws *Websocket) SetPingHander(h func(ws *Websocket, data []byte)) *Websocket {
	if h != nil {
		ws.handlePing = h
	}
	return ws
}

// SetPongHander sets the pong handler.
func (ws *Websocket) SetPongHander(h func(ws *Websocket, data []byte)) *Websocket {
	if h != nil {
		ws.handlePong = h
	}
	return ws
}

// SetCloseNotice sets the notice function that the connection is closed,
// that's, the callback cb will be called when the connection is closed.
func (ws *Websocket) SetCloseNotice(cb func()) *Websocket {
	ws.closeNotice = cb
	return ws
}

// SetClient sets the websocket is a client.
func (ws *Websocket) SetClient() *Websocket {
	ws.isClient = true
	return ws
}

// SetTimeout sets the timeout to receive and send the websocket message.
func (ws *Websocket) SetTimeout(timeout time.Duration) *Websocket {
	ws.timeout = timeout
	return ws
}

// SetMaxMsgSize sets the max message size of websocket.
//
// The default(0) is no limit.
func (ws *Websocket) SetMaxMsgSize(size int) *Websocket {
	if size >= 0 {
		ws.maxMsgSize = size
	}
	return ws
}

// SetBufferSize sets the buffer size of websocket.
//
// The default is 2048.
func (ws *Websocket) SetBufferSize(size int) *Websocket {
	if size > 0 {
		size += 16
		ws.bufferSize = size
		ws.recvBuffer = bytes.NewBuffer(nil)
		ws.recvBuffer.Grow(size)
		ws.msgBuffer = bytes.NewBuffer(nil)
		ws.msgBuffer.Grow(size)
	}
	return ws
}

// SetProtocol sets the selected protocol of websocket.
func (ws *Websocket) SetProtocol(protocol string) *Websocket {
	ws.subprotocol = protocol
	return ws
}

// Subprotocol returns the selected protocol.
func (ws *Websocket) Subprotocol() string {
	return ws.subprotocol
}

/////////////////////////////////////////////////////////////////////////////

// SendPing sends a ping control message.
func (ws *Websocket) SendPing(data []byte) error {
	if data == nil {
		data = pingPayload
	}
	err := ws.sendMsg(MsgTypePing, data)
	ws.SetReadDeadlineByDuration(ws.timeout)
	return err
}

// SendPong sends a pong control message.
func (ws *Websocket) SendPong(data []byte) error {
	if data == nil {
		data = pongPayload
	}
	return ws.sendMsg(MsgTypePong, data)
}

// SendClose termiates the websocket connection gracefully, which will send
// a close frame to the peer then close the underlying connection.
func (ws *Websocket) SendClose(code int, reason string) error {
	msg := make([]byte, len(reason)+2)
	binary.BigEndian.PutUint16(msg, uint16(code))
	if reason != "" {
		copy(msg[2:], reason)
	}

	err := ws.sendMsg(MsgTypeClose, msg)
	ws.close()
	return err
}

// SendTextMsg sends a text message.
//
// If fin is false, the message will be fragmented.
//
// Notice: when sending fragmented message, you should not send another message
// until this fragmented message is sent totally.
func (ws *Websocket) SendTextMsg(message []byte, fin ...bool) error {
	return ws.sendMsg(MsgTypeText, message, fin...)
}

// SendBinaryMsg sends a binary message.
//
// If fin is false, the message will be fragmented.
//
// Notice: when sending fragmented message, you should not send another message
// until this fragmented message is sent totally.
func (ws *Websocket) SendBinaryMsg(message []byte, fin ...bool) error {
	return ws.sendMsg(MsgTypeBinary, message, fin...)
}

// SendMsg sends the message with the message type.
//
// If the message is empty, it will be ignored.
//
// If fin is false, the message will be fragmented.
//
// Notice: when sending fragmented message, you should not send another message
// until this fragmented message is sent totally.
func (ws *Websocket) sendMsg(msgType int, message []byte, _fin ...bool) (err error) {
	if ws.IsClosed() {
		return errors.New("the connection has been closed")
	}

	fin := true
	if len(_fin) > 0 {
		fin = _fin[0]
	}

	if msgType > 0x7 {
		// RCF 6455 Section 5.4
		if !fin {
			panic(fmt.Errorf("the control frame message cannot be fragmented"))
		}

		// RCF 6455 Section 5.5
		if len(message) > 125 {
			panic(fmt.Errorf("the control frame message too big"))
		}
	}

	// Sends a standard data message
	var frame []byte
	if ws.isClient {
		var mask [4]byte
		mask[0] = byte(rand.Intn(256))
		mask[1] = byte(rand.Intn(256))
		mask[2] = byte(rand.Intn(256))
		mask[3] = byte(rand.Intn(256))
		frame = encodeHyBi(msgType, message, mask[:], fin)
	} else {
		frame = encodeHyBi(msgType, message, nil, fin)
	}

	n, _len := 0, len(frame)
	for {
		ws.SetWriteDeadlineByDuration(ws.timeout)
		if n, err = ws.conn.Write(frame); err != nil {
			ws.setClosed()
			return err
		} else if n < _len {
			_len -= n
			frame = frame[:n]
		} else {
			return nil
		}
	}
}

// Run runs forever until the connection is closed and returned the error.
//
// When receiving a websocket message, it will handle it by calling handle().
//
// Notice:
//  1. msgType be only MsgTypeText or MsgTypeBinary.
//  2. The message data must not be cached, such as putting it into channel.
func (ws *Websocket) Run(handle func(msgType int, message []byte)) error {
	for {
		messages, err := ws.RecvMsg()
		if err != nil {
			return err
		}
		for _, msg := range messages {
			handle(msg.Type, msg.Data)
		}
	}
}

func (ws *Websocket) errClose(code int, reason string) error {
	ws.SendClose(code, reason)
	return fmt.Errorf("[%d]%s", code, reason)
}

// RecvMsg receives and returns the websocket message.
//
// Notice:
//  1. If it returns an error, the underlying connection has been closed.
//  2. It only returns the text or binary message, not the control message.
//     So the message type be only MsgTypeText or MsgTypeBinary.
//  3. The message data must not be cached, such as putting it into channel.
func (ws *Websocket) RecvMsg() (messages []Message, err error) {
	frames, err := ws.recvFrames()
	if err != nil {
		return
	}

	for _, frame := range frames {
		// RFC 6455 Section 5.1: The server Must close the connection
		// upon receiving a frame that is not masked.
		if !ws.isClient && !frame.masked {
			return nil, ws.errClose(CloseProtocolError, "Protocol error: Frame not masked")
		}

		// RFC 6455 Section 5.1: A client MUST close a connection
		// if it detects a masked frame.
		if ws.isClient && frame.masked {
			return nil, ws.errClose(CloseProtocolError, "Protocol error: Frame masked")
		}

		if frame.opcode > 0x7 && len(frame.payload) > 125 {
			return nil, ws.errClose(CloseMessageTooBig, "The control frame message too big")
		}

		switch frame.opcode {
		case 0x0:
			if ws.msgBuffer.Len() == 0 || ws.opcode == 0 {
				return nil, ws.errClose(CloseProtocolError, "Protocol error: Unexpected continuation frame")
			}

			ws.msgBuffer.Write(frame.payload)
			if ws.maxMsgSize > 0 && ws.msgBuffer.Len() > ws.maxMsgSize {
				return nil, ws.errClose(CloseMessageTooBig, "The message is too big")
			}

			if frame.fin {
				messages = append(messages, Message{Type: ws.opcode, Data: ws.msgBuffer.Bytes()})
				ws.opcode = 0
				ws.msgBuffer.Reset()
			}
		case MsgTypeBinary, MsgTypeText:
			if ws.msgBuffer.Len() > 0 {
				return nil, ws.errClose(CloseProtocolError, "Protocol error: Unexpected new frame")
			}

			if frame.fin {
				messages = append(messages, Message{Type: frame.opcode, Data: frame.payload})
				continue
			}

			if ws.opcode > 0 {
				return nil, ws.errClose(CloseProtocolError, "Protocol error: more new fragmented")
			}
			ws.opcode = frame.opcode
			ws.msgBuffer.Write(frame.payload)
		case MsgTypeClose:
			if !frame.fin {
				return nil, ws.errClose(CloseUnsupportedData, "Unsupported: Fragmented close")
			}

			var code int
			var reason string
			if len(frame.payload) >= 2 {
				code = int(binary.BigEndian.Uint16(frame.payload[:2]))

				// RFC 6455 Section 8.1
				if len(frame.payload) > 2 {
					bs := frame.payload[2:]
					if !utf8.Valid(bs) {
						return nil, ws.errClose(CloseProtocolError, "Protocol error: Invalid UTF-8 in close")
					}
					reason = string(bs)
				}
			}

			if code == 0 {
				return nil, ws.errClose(CloseNoStatusReceived, "No close status code specified by peer")
			}
			return nil, ws.errClose(code, reason)
		case MsgTypePing:
			if !frame.fin {
				return nil, ws.errClose(CloseUnsupportedData, "Unsupported: Fragmented ping")
			}
			ws.handlePing(ws, frame.payload)
		case MsgTypePong:
			if !frame.fin {
				return nil, ws.errClose(CloseUnsupportedData, "Unsupported: Fragmented pong")
			}
			ws.handlePong(ws, frame.payload)
		default:
			return nil, ws.errClose(CloseUnsupportedData,
				fmt.Sprintf("Unsupported: Unknown opcode 0x%02x", frame.opcode))
		}
	}

	return
}

// recvFrames fetches more data from the socket to the buffer then decodes it.
func (ws *Websocket) recvFrames() (frames []frame, err error) {
	var n int
	for {
		if ws.IsClosed() {
			return nil, errors.New("the connection has been closed")
		}

		// Read the message.
		recvbuf := make([]byte, ws.bufferSize)
		ws.SetReadDeadlineByDuration(ws.timeout)
		if n, err = ws.conn.Read(recvbuf); err != nil {
			ws.close()
			return nil, err
		}
		ws.recvBuffer.Write(recvbuf[:n])

		// Check the message is too big.
		if _len := ws.recvBuffer.Len(); ws.maxMsgSize > 0 && _len > ws.maxMsgSize {
			ws.close()
			return nil, fmt.Errorf("the received message is too big: %d", _len)
		}

		// Decode the frame
		buffer := ws.recvBuffer.Bytes()
		for len(buffer) > 0 {
			var frame frame
			if decodeHyBi(&frame, buffer) {
				frames = append(frames, frame)
				buffer = buffer[frame.length:]
			} else {
				break
			}
		}

		if len(frames) > 0 {
			// Copy the frame data, don't use the buffer cache
			for i := range frames {
				data := make([]byte, len(frames[i].payload))
				copy(data, frames[i].payload)
				frames[i].payload = data
			}

			ws.recvBuffer.Reset()
			if len(buffer) > 0 {
				ws.recvBuffer.Write(buffer)
			}

			return
		}
	}
}

// IsClosed reports whether the underlying connection has been closed.
func (ws *Websocket) IsClosed() bool {
	return atomic.LoadInt32(&ws.closed) == 1
}

func (ws *Websocket) setClosed() {
	atomic.StoreInt32(&ws.closed, 1)
	if ws.closeNotice != nil {
		ws.closeNotice()
	}
}

// close closes the underlying socket.
func (ws *Websocket) close() {
	if !ws.IsClosed() {
		ws.setClosed()
		ws.conn.Close()
	}
}
