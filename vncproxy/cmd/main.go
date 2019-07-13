package main

import (
	"fmt"
	"net/http"
	"time"

	"github.com/go-redis/redis"
	"github.com/xgfone/gconf"
	"github.com/xgfone/klog"
	"github.com/xgfone/ship"
	"github.com/xgfone/websocket/vncproxy"
)

// Config is used to configure the app.
type Config struct {
	LogFile  string `default:"" help:"The path of the log file."`
	LogLevel string `default:"debug" help:"The level of logging, such as debug, info, etc."`

	ListenAddr  string `default:":5900" help:"The address that VNC proxy listens to."`
	ManagerAddr string `default:"127.0.0.1:9999" help:"The address that the manager listens to."`

	KeyFile  string `default:"" help:"The path of the key file."`
	CertFile string `default:"" help:"The path of cert file."`
	RedisURL string `default:"redis://localhost:6379/0" help:"The url to connect to redis."`

	Expiration time.Duration `default:"0s" help:"The expiration time of the token."`
}

func main() {
	// Initialize the config.
	var conf Config
	gconf.RegisterStruct(&conf)
	gconf.SetStringVersion("1.3.0")
	gconf.SetErrHandler(gconf.ErrorHandler(func(err error) { klog.Errorf("%s", err) }))
	if err := gconf.AddAndParseOptFlag(gconf.Conf); err != nil {
		klog.E(err).Errorf("failed to parse the cli flags")
		return
	}

	log, err := klog.NewSimpleLogger(conf.LogLevel, conf.LogFile, "100M", 100)
	if err != nil {
		klog.E(err).Errorf("failed to create log file")
		return
	}
	defer log.GetWriter().Close()
	klog.Std = log

	// Handle the redis client
	redisOpt, err := redis.ParseURL(conf.RedisURL)
	if err != nil {
		log.K("url", conf.RedisURL).E(err).Errorf("can't parse redis URL")
		return
	}
	redisClient := redis.NewClient(redisOpt)
	defer redisClient.Close()

	handler := vncproxy.NewWebsocketVncProxyHandler(vncproxy.ProxyConfig{
		CheckOrigin: func(r *http.Request) bool { return true },
		GetBackend: func(r *http.Request) (string, error) {
			if vs := r.URL.Query()["token"]; len(vs) > 0 {
				token, err := redisClient.Get(vs[0]).Result()
				if err != nil && err != redis.Nil {
					log.E(err).Errorf("redis GET error")
				}
				return token, nil
			}
			return "", nil
		},
	})

	opts := []ship.Option{
		ship.SetName("VNC Proxy"),
		ship.SetLogger(klog.ToFmtLoggerError(log)),
	}
	router1 := ship.New(opts...)
	router1.Route("/*").GET(func(ctx *ship.Context) error {
		handler.ServeHTTP(ctx.Response(), ctx.Request())
		return nil
	})

	router2 := router1.Clone("VNC Manager").Link(router1)
	router2.Route("/connections").GET(func(ctx *ship.Context) error {
		return ctx.String(http.StatusOK, fmt.Sprintf("%d", handler.Connections()))
	})
	router2.Route("/token").POST(func(ctx *ship.Context) error {
		token := ctx.QueryParam("token")
		addr := ctx.QueryParam("addr")
		if token == "" || addr == "" {
			return ctx.String(http.StatusBadRequest, "missing token or addr")
		}
		if err := redisClient.Set(token, addr, conf.Expiration).Err(); err != nil {
			return ship.ErrInternalServerError.NewError(err)
		}
		return nil
	})

	go router2.Start(conf.ManagerAddr)
	router1.Start(conf.ListenAddr, conf.CertFile, conf.KeyFile).Wait()
}
