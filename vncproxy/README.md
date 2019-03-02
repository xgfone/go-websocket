# vncproxy

It provides a HTTP Handler to implement the VNC proxy over websocket.

you can use it easily as following:

```go
tokens := map[string]string {
	"token1": "host1:port1",
	"token2": "host2:port2",
	// ...
}
wsconf := vncproxy.ProxyConfig{
	GetBackend: func(r *http.Request) (string, error) {
		if vs := r.URL.Query()["token"]; len(vs) > 0 {
			return tokens[vs[0]], nil
		}
		return "", nil
	},
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}
handler := vncproxy.NewWebsocketVncProxyHandler(wsconf)
http.Handle("/websockify", handler)
http.ListenAndServe(":5900", nil)
```

Then, you can use [noVNC](https://github.com/novnc/noVNC) by the url `http://127.0.0.1:5900/websockify?token=token1` to connect to "host1:port1" over websocket.

**NOTICE:** The sub-package [cmd](https://github.com/xgfone/websocket/tree/master/vncproxy/cmd) implements the function above, but using the redis to store the mapping between `TOKEN` and `HOST:PORT`.
