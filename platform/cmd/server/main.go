package main

import (
	"context"
	"errors"
	"flag"
	"github.com/gin-gonic/gin"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/von0000/dronealgo-ota/platform/cmd/server/docs"

	"github.com/von0000/dronealgo-ota/platform/cmd/server/controller"
	"github.com/von0000/dronealgo-ota/platform/cmd/server/router"
)

var addr = flag.String("addr", "127.0.0.1:1573", "server addr")

// @title DroneAlgo-OTA API
// @version 1.0
// @description OTA platform for drone avoidance algorithms.
// @BasePath /
func main() {
	gin.SetMode(gin.ReleaseMode)
	h2s := &http2.Server{}
	g := gin.Default()

	docs.SwaggerInfo.BasePath = "/"

	// 进程启动时尝试加载一次 store（见 file.go 中的 Export 函数）
	err := controller.InitStore()
	if err != nil {
		return
	}

	router.SetRouters(g)

	s := &http.Server{
		Addr:           *addr,
		Handler:        h2c.NewHandler(g, h2s),
		WriteTimeout:   15 * time.Second,
		ReadTimeout:    15 * time.Second,
		MaxHeaderBytes: 100 << 20,
	}
	go func() {
		log.Printf("server listening on %s", *addr)
		if err := s.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("listen error: %v", err)
		}
	}()

	// 优雅关停
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, os.Interrupt, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := s.Shutdown(ctx); err != nil {
		log.Printf("server shutdown: %v", err)
		_ = s.Close()
	}
	log.Println("server exited")
}
