package main

import (
	"context"
	"fmt"
	"math/rand"
	"net/http"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"
	"time"
)

type Handler struct {
	handled  uint64
	hostname string
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	v := atomic.AddUint64(&h.handled, 1)
	a := rand.Intn(50)

	if a == 0 {
		time.Sleep(time.Second * 10)
	} else {
		time.Sleep(time.Millisecond * 5)
	}

	fmt.Fprintf(w, "%s request: %d", h.hostname, v)
}

func main() {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGTERM, syscall.SIGUSR2)

	handler := &Handler{
		hostname: os.Getenv("HOSTNAME"),
	}
	server := http.Server{Addr: ":3007", Handler: handler}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*2)
	defer cancel()

	go func() {
		for s := range ch {
			switch s {
			case syscall.SIGTERM:
				server.Shutdown(ctx)
			}
		}

	}()

	server.ListenAndServe()
	fmt.Printf("served %d requests\n", handler.handled)
}
