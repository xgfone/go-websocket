# websocket

This is a websocket implementation, which is inspired by [websockify](https://github.com/novnc/websockify) and [websocket](https://github.com/gorilla/websocket).

It has no any dependencies.

#### Difference from `gorilla/websocket`
- `gorilla/websocket` only has a goroutine to read from websocket and a goroutine to write to websocket, that's, read or write can be done concurrently.
- Though this library cannot read from websocket concurrently, it's able to write to websocket concurrently. Moreover, it will be enhanced to read concurrently.

## Install

```shell
$ go get -u github.com/xgfone/websocket
```

## API

See [GoDoc](https://godoc.org/github.com/xgfone/websocket)


## VNC Proxy on WebSocket

The sub-package [vncproxy](https://github.com/xgfone/websocket/tree/master/vncproxy) supplies a HTTP handler about VNC Proxy on Websocket.
