package websocket

import (
	"bufio"
	"bytes"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"io"
	"math/rand"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// ClientOption is used to configure the client websocket.
//
// Notice: all the options are optional.
type ClientOption struct {
	// MaxLine represents the number of the characters of the longest line
	// which is 1024 by default.
	MaxLine int

	// Origin is used to set the Origin header.
	Origin string

	// Protocal is used to set the Sec-Websocket-Protocol header.
	Protocol []string

	// Header is the additional header to do websocket handshake.
	Header http.Header

	// Config is used by the default DialTLS to open a TCP/TLS connection.
	Config *tls.Config

	// Dial is used to open a TCP connection to addr, which is by default
	//   net.Dial("tcp", addr)
	Dial func(addr string) (net.Conn, error)

	// DialTLS is used to open a TCP/TLS connection to addr, which is by default
	//   tls.Dial("tcp", addr, ClientOption.Config)
	DialTLS func(addr string) (net.Conn, error)

	// GenerateHandshakeChallenge is used to generate a challenge
	// for websocket handshake.
	//
	// If missing, it will use the default implementation.
	GenerateHandshakeChallenge func() []byte
}

func defaultDial(addr string) (net.Conn, error) {
	return net.Dial("tcp", addr)
}

func defaultGenerateHandshakeChallenge() []byte {
	var bs [16]byte
	for i := range bs {
		bs[i] = byte(rand.Intn(256))
	}
	return bs[:]
}

func init() {
	rand.Seed(time.Now().UnixNano())
}

// NewClientWebsocket returns a new client websocket to connect to wsurl.
func NewClientWebsocket(wsurl string, option ...ClientOption) (ws *Websocket, err error) {
	u, err := url.Parse(wsurl)
	if err != nil {
		return nil, err
	}

	var opt ClientOption
	if len(option) > 0 {
		opt = option[0]
	}
	if opt.MaxLine < 1 {
		opt.MaxLine = 1024
	}

	var conn net.Conn
	switch u.Scheme {
	case "ws":
		if opt.Dial != nil {
			conn, err = opt.Dial(u.Host)
		} else {
			conn, err = defaultDial(u.Host)
		}
	case "wss":
		if opt.DialTLS != nil {
			conn, err = opt.DialTLS(u.Host)
		} else if opt.Config != nil {
			conn, err = tls.Dial("tcp", u.Host, opt.Config)
		} else {
			conn, err = tls.Dial("tcp", u.Host, &tls.Config{})
		}
	default:
		return nil, fmt.Errorf("the websocket scheme must be ws or wss")
	}
	if err != nil {
		return nil, err
	}

	defer func() {
		if err != nil && conn != nil {
			conn.Close()
		}
	}()

	switch u.Path {
	case "":
		u.Path = "/"
	case "//":
		u.Path = u.Path[1:]
	}

	genkey := opt.GenerateHandshakeChallenge
	if genkey == nil {
		genkey = defaultGenerateHandshakeChallenge
	}

	// Send the websocket handshake.
	buf := bytes.NewBuffer(nil)
	buf.Grow(512)

	// Write the request line
	buf.WriteString("GET ")
	if u.Path == "" {
		buf.WriteByte('/')
	} else {
		buf.WriteString(u.Path)
	}
	if u.RawQuery != "" {
		buf.WriteByte('?')
		buf.WriteString(u.RawQuery)
	}
	buf.WriteString(" HTTP/1.1\r\n")

	// Write the request header
	challenge := base64.StdEncoding.EncodeToString(genkey())
	fmt.Fprintf(buf, "Host: %s\r\n", u.Host)
	buf.WriteString("Connection: Upgrade\r\n")
	buf.WriteString("Upgrade: websocket\r\n")
	fmt.Fprintf(buf, "Sec-WebSocket-Version: 13\r\n")
	fmt.Fprintf(buf, "Sec-WebSocket-Key: %s\r\n", challenge)
	if len(opt.Protocol) > 0 {
		fmt.Fprintf(buf, "Sec-Websocket-Protocol: %s\r\n", strings.Join(opt.Protocol, ", "))
	}
	if opt.Origin != "" {
		fmt.Fprintf(buf, "Origin: %s\r\n", opt.Origin)
	}
	for key, value := range opt.Header {
		fmt.Fprintf(buf, "%s: %s\r\n", key, strings.Join(value, ", "))
	}

	// Write the header end
	buf.WriteString("\r\n")

	// Send the websocket handshake request
	if n, err := conn.Write(buf.Bytes()); err != nil {
		return nil, err
	} else if n != buf.Len() {
		return nil, fmt.Errorf("failed to send the websocket handshake request")
	}

	// Handle the websocket handshake response
	reader := bufio.NewReaderSize(conn, opt.MaxLine)
	resp, err := http.ReadResponse(reader, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var body []byte
	if resp.ContentLength > 0 {
		buf := bytes.NewBuffer(nil)
		if resp.ContentLength < 1024 {
			buf.Grow(1024)
		} else {
			buf.Grow(8192)
		}

		if m, err := io.Copy(buf, io.LimitReader(resp.Body, resp.ContentLength)); err != nil {
			return nil, err
		} else if m < resp.ContentLength {
			return nil, fmt.Errorf("the websocket handshake response is too few")
		}
	}

	if resp.StatusCode != 101 {
		if len(body) == 0 {
			return nil, fmt.Errorf("code=%d", resp.StatusCode)
		}
		return nil, fmt.Errorf("code=%d: %s", resp.StatusCode, string(body))
	} else if strings.ToLower(resp.Header.Get("Connection")) != "upgrade" {
		return nil, fmt.Errorf("the Connection header is not upgrade")
	} else if strings.ToLower(resp.Header.Get("Upgrade")) != "websocket" {
		return nil, fmt.Errorf("the Upgrade header is not websocket")
	} else if accept := resp.Header.Get("Sec-WebSocket-Accept"); accept == "" {
		return nil, fmt.Errorf("missing Sec-WebSocket-Accept header")
	} else if accept != computeAcceptKey(challenge) {
		return nil, fmt.Errorf("invalid websocket challenge: %s", accept)
	}

	protocol := resp.Header.Get("Sec-WebSocket-Protocol")
	return NewWebsocket(conn).SetProtocol(protocol).SetClient(), nil
}
