package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"github.com/gorilla/websocket"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"
)

type Server struct {
	mu      *sync.Mutex
	clients map[*websocket.Conn]struct{}

	upg     websocket.Upgrader
	httpSrv *http.Server

	maxConnections int
}

func newServer(addr string, maxConn int) *Server {
	s := &Server{
		mu:      &sync.Mutex{},
		clients: make(map[*websocket.Conn]struct{}),
		upg: websocket.Upgrader{
			ReadBufferSize:  1024,
			WriteBufferSize: 1024,
		},
		httpSrv: &http.Server{
			Addr:    addr,
			Handler: http.DefaultServeMux,
		},
		maxConnections: maxConn,
	}

	http.HandleFunc("/ws", s.wsHandler)
	http.HandleFunc("/push", s.pushHandler)
	http.HandleFunc("/clients", s.clientsHandler)

	return s
}

var (
	appAddr = flag.String("addr", ":8080", "address to listen")
	maxConn = flag.Int("maxconn", 60000, "max number of connections to accept")
)

func main() {
	flag.Parse()

	addr := *appAddr
	if addr == "" {
		addr = ":8080"
	}

	mc := 0
	if *maxConn > 0 {
		mc = *maxConn
	} else {
		mc = 60000
	}

	s := newServer(addr, mc)

	log.Printf("starting server on %s\n", addr)

	go func() {
		err := s.httpSrv.ListenAndServe()
		if !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("failed to start HTTP server: %v\n", err)
		}
	}()

	log.Printf("server started on %s\n", addr)

	// graceful shutdown
	c := make(chan os.Signal)
	signal.Notify(c, syscall.SIGINT, syscall.SIGTERM)
	<-c

	err := s.httpSrv.Shutdown(context.Background())
	if err != nil {
		log.Printf("failed to shutdown HTTP server: %v\n", err)
	}
}

func (s *Server) wsHandler(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if len(s.clients) >= s.maxConnections {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}

	conn, err := s.upg.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("failed to upgrade WS connection: %v\n", err)
	}

	s.clients[conn] = struct{}{}
	go s.clientReadLoop(conn)
}

func (s *Server) pushHandler(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		log.Printf("failed to read body: %v\n", err)
		return
	}

	defer r.Body.Close()

	s.pushAll(body)
}

func (s *Server) clientsHandler(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()

	w.WriteHeader(http.StatusOK)
	w.Write([]byte(fmt.Sprintf("clients: %d", len(s.clients))))
}

func (s *Server) clientReadLoop(conn *websocket.Conn) {
	for {
		_, _, err := conn.NextReader()
		if err != nil {
			s.deleteConn(conn)
			return
		}
	}
}

func (s *Server) deleteConn(conn *websocket.Conn) {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.clients, conn)
}

func (s *Server) pushAll(data []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var failedClients []*websocket.Conn

	start := time.Now()
	log.Printf("starting push at %v\n", start)
	for conn, _ := range s.clients {
		w, err := conn.NextWriter(websocket.TextMessage)
		if err != nil {
			failedClients = append(failedClients, conn)
			continue
		}

		_, err = w.Write(data)
		if err != nil {
			log.Printf("failed to send msg to client %v: %v\n", conn.RemoteAddr(), err)
			failedClients = append(failedClients, conn)
		}

		err = w.Close()
		if err != nil {
			log.Printf("failed to close client %v writer: %v\n", conn.RemoteAddr(), err)
		}
	}

	if len(failedClients) > 0 {
		for _, client := range failedClients {
			client.Close()
			delete(s.clients, client)
		}
	}

	end := time.Now()
	log.Printf("push took %d\n", end.Sub(start).Milliseconds())
}
