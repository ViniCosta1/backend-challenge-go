package httpserver

import (
	"context"
	"net/http"
	"testing"
	"time"
)

func TestServerStartsAndStops(t *testing.T) {
	server, err := New(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }),
		Settings{Address: "127.0.0.1:0", ReadHeaderTimeout: time.Second, ReadTimeout: time.Second,
			WriteTimeout: time.Second, IdleTimeout: time.Second}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := server.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !server.Running() {
		t.Fatal("server should be running")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := server.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	if server.Running() {
		t.Fatal("server should be stopped")
	}
}
