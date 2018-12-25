package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/go-redis/redis"
	"github.com/xgfone/logger"
	"github.com/xgfone/ship"
	"github.com/xgfone/websocket/vncproxy"
)

var versionS = "1.0.0"

var (
	version     bool
	logFile     string
	logLevel    string
	listenAddr  string
	redisURL    string
	certFile    string
	keyFile     string
	managerAddr string
	expiration  int
)

func init() {
	flag.BoolVar(&version, "version", false, "Print the version and exit")
	flag.StringVar(&logFile, "logfile", "", "The path of the log file")
	flag.StringVar(&logLevel, "loglevel", "debug", "The level of the log, such as debug, info, etc")
	flag.StringVar(&listenAddr, "addr", ":5900", "The listen address")
	flag.StringVar(&redisURL, "redis", "redis://localhost:6379/0", "The redis connection url")
	flag.StringVar(&certFile, "cert", "", "The path of the cert file")
	flag.StringVar(&keyFile, "key", "", "The path of the key file")
	flag.StringVar(&managerAddr, "manager_addr", "127.0.0.1:9999", "The address to be used to manage")
	flag.IntVar(&expiration, "expire", 0, "The expiration time(s) of the token")
}

func main() {
	flag.Parse()
	if version {
		fmt.Println(versionS)
		return
	}
	tokenExpiration := time.Duration(expiration) * time.Second

	// Handle the logging
	_logger, closer, err := logger.SimpleLogger(logLevel, logFile)
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	defer closer.Close()
	logger.SetGlobalLogger(_logger)

	// Handle the redis client
	redisOpt, err := redis.ParseURL(redisURL)
	if err != nil {
		logger.Error("can't parse redis URL: url=%s, err=%s", redisURL, err)
		return
	}
	redisClient := redis.NewClient(redisOpt)

	wsconf := vncproxy.ProxyConfig{
		GetBackend: func(r *http.Request) string {
			if vs := r.URL.Query()["token"]; len(vs) > 0 {
				token, err := redisClient.Get(vs[0]).Result()
				if err != nil && err != redis.Nil {
					logger.Error("redis GET error: %s", err)
				}
				return token
			}
			return ""
		},
		CheckOrigin: func(r *http.Request) bool {
			return true
		},
	}

	handler := vncproxy.NewWebsocketVncProxyHandler(wsconf)
	rlogger := logger.ToLoggerWithoutError(logger.GetGlobalLogger())

	router1 := ship.New(ship.Config{Logger: rlogger, Signals: []os.Signal{}})
	router2 := ship.New(ship.Config{Logger: rlogger})
	router2.RegisterOnShutdown(func() { redisClient.Close() })

	router1.Route("/connections").GET(func(ctx ship.Context) error {
		return ctx.String(http.StatusOK, fmt.Sprintf("%d", handler.Connections()))
	})
	router1.Route("/token").POST(func(ctx ship.Context) error {
		token := ctx.QueryParam("token")
		addr := ctx.QueryParam("addr")
		if token == "" || addr == "" {
			return ctx.String(http.StatusBadRequest, "missing token or addr")
		}
		if err := redisClient.Set(token, addr, tokenExpiration).Err(); err != nil {
			return ship.ErrInternalServerError.SetInnerError(err)
		}
		return nil
	})
	router2.Route("/*").CONNECT(func(ctx ship.Context) error {
		handler.ServeHTTP(ctx.Response(), ctx.Request())
		return nil
	})

	go func() {
		logger.Info("manager listening on %s", managerAddr)
		if err := router1.Start(managerAddr); err != nil {
			logger.Error("Manager ListenAndServe: %s", err)
			router2.Shutdown(context.Background())
		}
	}()

	if err := router2.Start(listenAddr, certFile, keyFile); err != nil {
		logger.Error("%s", err)
	}
}
