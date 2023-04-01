# websocket [![GoDoc](https://pkg.go.dev/badge/github.com/xgfone/go-websocket)](https://pkg.go.dev/github.com/xgfone/go-websocket) [![License](https://img.shields.io/badge/License-Apache%202.0-blue.svg?style=flat-square)](https://raw.githubusercontent.com/xgfone/go-websocket/master/LICENSE)

This is a websocket implementation supporting `Go1.7+`, which is inspired by [websockify](https://github.com/novnc/websockify) and [websocket](https://github.com/gorilla/websocket).

#### Difference from `gorilla/websocket`
- `gorilla/websocket` only has a goroutine to read from websocket and a goroutine to write to websocket, that's, read or write can be done concurrently.
- Though this library cannot read from websocket concurrently, it's able to write to websocket concurrently. Moreover, it will be enhanced to read concurrently.

## Install

```shell
$ go get -u github.com/xgfone/go-websocket
```

## VNC Proxy on WebSocket

The sub-package [vncproxy](https://github.com/xgfone/go-websocket/tree/master/vncproxy) provides a HTTP handler about VNC Proxy on Websocket.

## Example

### Websocket Server Example
```go
package main

import (
	"log"
	"net/http"
	"time"

	"github.com/xgfone/go-websocket"
)

var upgrader = websocket.Upgrader{
	MaxMsgSize:  1024,
	Timeout:     time.Second * 30,
	CheckOrigin: func(r *http.Request) bool { return true },
}

func websocketHandler(rw http.ResponseWriter, r *http.Request) {
	ws, err := upgrader.Upgrade(rw, r, nil)
	if err != nil {
		log.Printf("failed to upgrade to websocket: %s\n", err)
		return
	}

	ws.Run(func(msgType int, message []byte) {
		switch msgType {
		case websocket.MsgTypeBinary:
			ws.SendBinaryMsg(message)
		case websocket.MsgTypeText:
			ws.SendTextMsg(message)
		}
	})
}

func main() {
	http.ListenAndServe(":80", http.HandlerFunc(websocketHandler))
}
```

### Websocket Client Example
```go
package main

import (
	"fmt"
	"time"

	"github.com/xgfone/go-websocket"
)

func main() {
	ws, err := websocket.NewClientWebsocket("ws://127.0.0.1/")
	if err == nil {
		go func() {
			tick := time.NewTicker(time.Second * 10)
			defer tick.Stop()
			for {
				select {
				case now := <-tick.C:
					if err := ws.SendTextMsg([]byte(now.String())); err != nil {
						fmt.Println(err)
						return
					}
				}
			}
		}()

		err = ws.Run(func(msgType int, msg []byte) {
			fmt.Printf("Receive: %s\n", string(msg))
		})
	}
	fmt.Println(err)
}
```

The client will output like this:
```
Receive: 2019-07-13 18:33:47.951688 +0800 CST m=+10.007139340
Receive: 2019-07-13 18:33:57.951479 +0800 CST m=+20.006605995
Receive: 2019-07-13 18:34:07.948628 +0800 CST m=+30.003442484
Receive: 2019-07-13 18:34:17.949763 +0800 CST m=+40.004270178
Receive: 2019-07-13 18:34:27.947877 +0800 CST m=+50.002081112
Receive: 2019-07-13 18:34:37.949986 +0800 CST m=+60.003888082
...
```
