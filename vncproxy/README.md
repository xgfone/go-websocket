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

Here is a complete example program.
```go
// main.go
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/xgfone/go-websocket/vncproxy"
)

var (
	listenAddr  string
	managerAddr string
	keyFile     string
	certFile    string
	redisURL    string
	expired     time.Duration
)

func init() {
	flag.StringVar(&listenAddr, "listenaddr", ":5900", "The address that VNC proxy listens to.")
	flag.StringVar(&managerAddr, "manageraddr", "", "The address that the manager listens to. It is disabled by default.")
	flag.StringVar(&keyFile, "keyfile", "", "The path of the key file.")
	flag.StringVar(&certFile, "certfile", "", "The path of cert file.")
	flag.StringVar(&redisURL, "redisurl", "redis://localhost:6379/0", "The url to connect to redis.")
	flag.DurationVar(&expired, "expired", 0, "The expiration time of the token.")
}

func main() {
	flag.Parse()

	redisOpt, err := redis.ParseURL(redisURL)
	if err != nil {
		log.Fatalf("can't parse redis URL '%s': %s", redisURL, err)
	}
	redisClient := redis.NewClient(redisOpt)
	defer redisClient.Close()

	// Create the HTTP handler of the VNC proxy.
	handler := vncproxy.NewWebsocketVncProxyHandler(vncproxy.ProxyConfig{
		CheckOrigin: func(r *http.Request) bool { return true },
		GetBackend: func(r *http.Request) (string, error) {
			if vs := r.URL.Query()["token"]; len(vs) > 0 {
				token, err := redisClient.Get(context.TODO(), vs[0]).Result()
				if err != nil && err != redis.Nil {
					log.Printf("redis GET error: %s", err)
				}
				return token, nil
			}
			return "", nil
		},
	})

	wg := new(sync.WaitGroup)
	wg.Add(2)

	go func() {
		defer wg.Done()
		mux := http.NewServeMux()
		mux.Handle("/vnc", handler)
		if keyFile != "" && certFile != "" {
			http.ListenAndServeTLS(listenAddr, certFile, keyFile, mux)
		}
	}()

	go func() {
		defer wg.Done()
		if managerAddr == "" {
			return
		}

		mux := http.NewServeMux()
		mux.HandleFunc("/connections", func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprint(w, handler.Connections())
		})
		mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
			query := r.URL.Query()
			token := query.Get("token")
			addr := query.Get("addr")
			if token == "" || addr == "" {
				w.WriteHeader(400)
				fmt.Fprint(w, "missing token or addr")
				return
			}

			err := redisClient.Set(context.TODO(), token, addr, expired).Err()
			if err != nil {
				w.WriteHeader(500)
				fmt.Fprint(w, err.Error())
			}
		})
		http.ListenAndServe(managerAddr, mux)
	}()

	wg.Wait()
}
```

```shell
$ cat > go.mod <<EOF
module vncproxy

require (
	github.com/redis/go-redis/v9 v9.0.2
	github.com/xgfone/go-websocket v1.10.0
)

go 1.18
EOF
$ go mod tidy && go build
$ ./vncproxy --help
  -certfile string
        The path of cert file.
  -expired duration
        The expiration time of the token.
  -keyfile string
        The path of the key file.
  -listenaddr string
        The address that VNC proxy listens to. (default ":5900")
  -manageraddr string
        The address that the manager listens to. It is disabled by default.
  -redisurl string
        The url to connect to redis. (default "redis://localhost:6379/0")
```
