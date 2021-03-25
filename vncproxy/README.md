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
	"net/http"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/xgfone/goapp"
	"github.com/xgfone/goapp/log"
	"github.com/xgfone/goapp/router"
	"github.com/xgfone/ship/v3"
	"github.com/xgfone/websocket/vncproxy"
)

// Config is used to configure the app.
type Config struct {
	ListenAddr  string `default:":5900" help:"The address that VNC proxy listens to."`
	ManagerAddr string `default:"" help:"The address that the manager listens to. It is disabled by default."`

	KeyFile  string `default:"" help:"The path of the key file."`
	CertFile string `default:"" help:"The path of cert file."`
	RedisURL string `default:"redis://localhost:6379/0" help:"The url to connect to redis."`

	Expiration time.Duration `default:"0s" help:"The expiration time of the token."`
}

func main() {
	// Initialize and parse the config.
	var conf Config
	goapp.Init("", &conf)

	// Handle the redis client.
	redisOpt, err := redis.ParseURL(conf.RedisURL)
	if err != nil {
		log.Error("can't parse redis URL", log.F("url", conf.RedisURL), log.E(err))
		return
	}
	redisClient := redis.NewClient(redisOpt)
	defer redisClient.Close()

	// Create the HTTP handler of the VNC proxy.
	handler := vncproxy.NewWebsocketVncProxyHandler(vncproxy.ProxyConfig{
		Logger:      log.GetDefaultLogger(),
		CheckOrigin: func(r *http.Request) bool { return true },
		GetBackend: func(r *http.Request) (string, error) {
			if vs := r.URL.Query()["token"]; len(vs) > 0 {
				token, err := redisClient.Get(context.TODO(), vs[0]).Result()
				if err != nil && err != redis.Nil {
					log.Ef(err, "redis GET error")
				}
				return token, nil
			}
			return "", nil
		},
	})

	proxy := router.InitRouter()
	proxy.Name = "VNC Proxy"
	proxy.Route("/*").GET(ship.FromHTTPHandler(handler))

	manager := proxy.Clone()
	manager.Name = "VNC Manager"
	manager.Link(proxy.Runner)
	manager.Route("/connections").GET(func(c *ship.Context) error {
		return c.Text(http.StatusOK, "%d", handler.Connections())
	})
	manager.Route("/token").POST(func(ctx *ship.Context) error {
		token := ctx.QueryParam("token")
		addr := ctx.QueryParam("addr")
		if token == "" || addr == "" {
			return ship.ErrBadRequest.NewMsg("missing token or addr")
		}

		if err := redisClient.Set(context.TODO(), token, addr, conf.Expiration).Err(); err != nil {
			return ship.ErrInternalServerError.NewError(err)
		}
		return nil
	})

	if conf.ManagerAddr != "" {
		go manager.Start(conf.ManagerAddr)
	}
	proxy.Start(conf.ListenAddr, conf.CertFile, conf.KeyFile).Wait()
}
```

```shell
$ cat > go.mod <<EOF
module vncproxy

require (
	github.com/go-redis/redis/v8 v8.8.0
	github.com/xgfone/goapp v0.18.0
	github.com/xgfone/ship/v3 v3.13.0
	github.com/xgfone/websocket v1.9.0
)

go 1.11
EOF
$ go mod tidy && go build main.go
$ ./vncproxy --help
  --certfile string
        The path of cert file.
  --config-file string
        the config file path.
  --expiration duration
        The expiration time of the token.
  --keyfile string
        The path of the key file.
  --listenaddr string
        The address that VNC proxy listens to. (default ":5900")
  --logfile string
        The file path of the log. The default is stdout.
  --loglevel string
        The level of the log, such as debug, info (default "info")
  --manageraddr string
        The address that the manager listens to. It is disabled by default.
  --redisurl string
        The url to connect to redis. (default "redis://localhost:6379/0")
  --version bool
        Print the version and exit.
```
