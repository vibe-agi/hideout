package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"
)

func main() {
	mode := flag.String("mode", "echo", "target mode: echo, http, devtools, auth-api")
	listen := flag.String("listen", "127.0.0.1:0", "listen address")
	path := flag.String("path", "/preview", "HTTP path")
	status := flag.Int("status", http.StatusNoContent, "HTTP status")
	browser := flag.String("browser", "FakeChrome/1.0", "DevTools browser name")
	flag.Parse()

	ln, err := net.Listen("tcp", *listen)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(ln.Addr().String())

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	errc := make(chan error, 1)
	switch *mode {
	case "echo":
		go func() { errc <- serveEcho(ctx, ln) }()
	case "http":
		go func() { errc <- serveHTTP(ctx, ln, httpHandler(*path, *status)) }()
	case "devtools":
		go func() { errc <- serveHTTP(ctx, ln, devtoolsHandler(ln.Addr().String(), *browser)) }()
	case "auth-api":
		go func() { errc <- serveHTTP(ctx, ln, authAPIHandler(*path)) }()
	default:
		log.Fatalf("unsupported mode %q", *mode)
	}

	select {
	case <-ctx.Done():
		_ = ln.Close()
	case err := <-errc:
		if err != nil {
			log.Fatal(err)
		}
	}
}

func serveEcho(ctx context.Context, ln net.Listener) error {
	go func() {
		<-ctx.Done()
		_ = ln.Close()
	}()
	for {
		conn, err := ln.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				return nil
			default:
				return err
			}
		}
		go handleEcho(conn)
	}
}

func handleEcho(conn net.Conn) {
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	line, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil {
		return
	}
	_, _ = fmt.Fprintf(conn, "echo:%s", line)
}

func serveHTTP(ctx context.Context, ln net.Listener, handler http.Handler) error {
	server := &http.Server{Handler: handler}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()
	if err := server.Serve(ln); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}

func httpHandler(path string, status int) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != path {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte("hideout-lab-target\n"))
	})
}

func devtoolsHandler(addr, browser string) http.Handler {
	_, port, _ := net.SplitHostPort(addr)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/json/version" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		payload := map[string]string{
			"Browser":              browser,
			"webSocketDebuggerUrl": "ws://127.0.0.1:" + port + "/devtools/browser/fake-secret-" + strconv.Itoa(os.Getpid()),
		}
		_ = json.NewEncoder(w).Encode(payload)
	})
}

func authAPIHandler(path string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != path {
			http.NotFound(w, r)
			return
		}
		auth := r.Header.Get("Authorization")
		if !strings.HasPrefix(auth, "Bearer ") || strings.TrimSpace(strings.TrimPrefix(auth, "Bearer ")) == "" {
			http.Error(w, "missing bearer token", http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("auth-ok\n"))
	})
}
