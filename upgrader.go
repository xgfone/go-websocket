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

package websocket

import (
	"crypto/sha1"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"
)

func headerContainValue(header http.Header, key string, value string) bool {
	for _, v := range header[key] {
		for _, s := range strings.Split(v, ",") {
			if strings.ToLower(strings.TrimSpace(s)) == value {
				return true
			}
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
	BufferSize   int // The default is 2048KB.
	MaxMsgSize   int // The default is no limit.
	Subprotocols []string
	Timeout      time.Duration // The default is no timeout.

	CheckOrigin  func(*http.Request) bool
	Authenticate func(*http.Request) bool
}

func (u Upgrader) handleError(w http.ResponseWriter, r *http.Request, code int,
	reason string) (*Websocket, error) {
	w.WriteHeader(code)
	fmt.Fprint(w, reason)
	return nil, errors.New(reason)
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
	for _, serverProtocol := range u.Subprotocols {
		for _, clientProtocol := range protocols {
			if clientProtocol == serverProtocol {
				return clientProtocol
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
	responseHeader http.Header) (*Websocket, error) {

	if r.Method != "GET" {
		return u.handleError(w, r, http.StatusMethodNotAllowed,
			"websocket: request method is not GET")
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
				"websocket: invalid protocol")
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
		return nil, errors.New("websocket: client sent data before handshake is complete")
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
	if u.Timeout > 0 {
		conn.SetDeadline(time.Now().Add(u.Timeout))
	} else {
		conn.SetDeadline(time.Time{})
	}
	if _, err = conn.Write(buf); err != nil {
		conn.Close()
		return nil, err
	}

	maxMsgSize := u.MaxMsgSize
	if maxMsgSize < 0 {
		maxMsgSize = 0
	}
	bufSize := u.BufferSize
	if bufSize < 1024 {
		bufSize = 2048
	}

	ws := NewWebsocket(conn)
	ws.SetBufferSize(bufSize).SetMaxMsgSize(maxMsgSize).SetTimeout(u.Timeout).
		SetProtocol(subprotocol).SetDeadlineByDuration(u.Timeout)
	return ws, nil
}
