package main

import (
	"net/http"

	"github.com/go-redis/redis"
	"github.com/xgfone/gconf/v4"
	"github.com/xgfone/klog/v3"
	"github.com/xgfone/ship/v2"
	"github.com/xgfone/websocket/vncproxy"
)

// Config is used to configure the app.
type Config struct {
	LogFile  gconf.StringOptField `default:"" help:"The path of the log file."`
	LogLevel gconf.StringOptField `default:"debug" help:"The level of logging, such as debug, info, etc."`

	ListenAddr  gconf.StringOptField `default:":5900" help:"The address that VNC proxy listens to."`
	ManagerAddr gconf.StringOptField `default:"" help:"The address that the manager listens to. It is disabled by default."`

	KeyFile  gconf.StringOptField `default:"" help:"The path of the key file."`
	CertFile gconf.StringOptField `default:"" help:"The path of cert file."`
	RedisURL gconf.StringOptField `default:"redis://localhost:6379/0" help:"The url to connect to redis."`

	Expiration gconf.DurationOptField `default:"0s" help:"The expiration time of the token."`
}

func main() {
	// Initialize the config.
	var conf Config
	gconf.RegisterStruct(&conf)
	gconf.SetStringVersion("1.8.0")
	gconf.SetErrHandler(gconf.ErrorHandler(func(err error) { klog.Errorf("%s", err) }))
	gconf.AddAndParseOptFlag(gconf.Conf)
	gconf.LoadSource(gconf.NewFlagSource())
	gconf.LoadSource(gconf.NewFileSource(gconf.GetString(gconf.ConfigFileOpt.Name)))

	// Initialize the logging.
	wc, err := klog.FileWriter(conf.LogFile.Get(), "100M", 100)
	if err != nil {
		klog.Error("failed to create log file", klog.E(err))
		return
	}
	defer wc.Close()
	klog.GetEncoder().SetWriter(wc)
	klog.SetLevel(klog.NameToLevel(conf.LogLevel.Get()))
	logger := klog.ToFmtLogger(klog.GetDefaultLogger())

	// Handle the redis client.
	redisOpt, err := redis.ParseURL(conf.RedisURL.Get())
	if err != nil {
		klog.Error("can't parse redis URL", klog.F("url", conf.RedisURL.Get()), klog.E(err))
		return
	}
	redisClient := redis.NewClient(redisOpt)
	defer redisClient.Close()

	handler := vncproxy.NewWebsocketVncProxyHandler(vncproxy.ProxyConfig{
		Logger:      logger,
		CheckOrigin: func(r *http.Request) bool { return true },
		GetBackend: func(r *http.Request) (string, error) {
			if vs := r.URL.Query()["token"]; len(vs) > 0 {
				token, err := redisClient.Get(vs[0]).Result()
				if err != nil && err != redis.Nil {
					klog.Error("redis GET error", klog.E(err))
				}
				return token, nil
			}
			return "", nil
		},
	})

	router1 := ship.New()
	router1.Runner.Name = "VNC Proxy"
	router1.SetLogger(logger)
	router1.Route("/*").GET(func(ctx *ship.Context) error {
		handler.ServeHTTP(ctx.Response(), ctx.Request())
		return nil
	})

	router2 := router1.Clone()
	router2.Runner.Name = "VNC Manager"
	router1.RegisterOnShutdown(router2.Stop)
	router2.RegisterOnShutdown(router1.Stop)
	router2.Route("/connections").GET(func(c *ship.Context) error {
		return c.Text(http.StatusOK, "%d", handler.Connections())
	})
	router2.Route("/token").POST(func(ctx *ship.Context) error {
		token := ctx.QueryParam("token")
		addr := ctx.QueryParam("addr")
		if token == "" || addr == "" {
			return ship.ErrBadRequest.NewMsg("missing token or addr")
		}
		if err := redisClient.Set(token, addr, conf.Expiration.Get()).Err(); err != nil {
			return ship.ErrInternalServerError.NewError(err)
		}
		return nil
	})

	if managerAddr := conf.ManagerAddr.Get(); managerAddr != "" {
		go router2.Start(managerAddr)
	}
	router1.Start(conf.ListenAddr.Get(), conf.CertFile.Get(), conf.KeyFile.Get()).Wait()
}
