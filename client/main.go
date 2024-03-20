package main

import (
	"flag"
	"github.com/gorilla/websocket"
	"log"
	"math/rand"
	"net/url"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"
)

var (
	appUrl  = flag.String("appurl", "", "application URL")
	maxConn = flag.Int("maxconn", 66000, "max connections count")
)

const (
	workersCount = 100
)

type Client struct {
	conn *websocket.Conn
}

type result struct {
	client *Client
	id     int
}

func main() {
	flag.Parse()
	if *appUrl == "" {
		panic("--appurl parameter is required")
	}

	maxConnectionsCount := *maxConn

	// Scheme: ws because we're connecting via plain WS
	u := url.URL{Scheme: "ws", Host: *appUrl, Path: "/ws"}

	var clients []*Client

	outputCh := make(chan *result, 100)
	var doneWg sync.WaitGroup
	doneWg.Add(1)

	countCh := make(chan time.Time, maxConnectionsCount)
	go aggregate(countCh)

	go func() {
		for res := range outputCh {
			if res.client != nil {
				clients = append(clients, res.client)
				go res.client.readLoop(countCh)
				if res.id%2000 == 0 {
					log.Printf("client #%d connected at %v\n", res.id, res.client.conn.LocalAddr())
				}
			}
		}

		doneWg.Done()
	}()

	inputCh := make(chan int, workersCount)

	var wg sync.WaitGroup
	wg.Add(workersCount)
	for i := 0; i < workersCount; i++ {
		go runWorker(&wg, inputCh, u, outputCh)
	}

	for i := 0; i < maxConnectionsCount; i++ {
		inputCh <- i
	}

	log.Println("closing input channel")
	close(inputCh)
	wg.Wait()
	close(outputCh)
	doneWg.Wait()

	log.Printf("total connected clients: %d\n", len(clients))

	log.Printf("waiting for exit signal\n")
	exitCh := make(chan os.Signal)
	signal.Notify(exitCh, syscall.SIGINT, syscall.SIGTERM)
	<-exitCh

	log.Println("exiting...")
	for _, client := range clients {
		err := client.close()
		if err != nil {
			log.Println(err)
		}
	}
}

func aggregate(ch chan time.Time) {
	got := 0
	var last = time.Now()
	var start time.Time

	go func() {
		for v := range ch {
			if time.Now().Sub(last).Seconds() > (time.Second * 60).Seconds() {
				start = time.Now()
			}

			last = v

			got++
		}
	}()

	for {
		<-time.After(time.Second * 5)
		log.Printf("STATS: got %d responses so far (start at %s, last at %s)\n", got, start.Format(time.RFC3339Nano), last.Format(time.RFC3339Nano))
	}
}

func runWorker(wg *sync.WaitGroup, inputCh chan int, u url.URL, outputCh chan *result) {
	defer wg.Done()

	r := rand.New(rand.NewSource(time.Now().Unix()))

	for clientID := range inputCh {
		attempt := 0
		waitTime := 0 * time.Millisecond

		for {
			if waitTime > 0 {
				time.Sleep(waitTime)
			}

			c, err := tryConnect(u, clientID)
			if err != nil {
				attempt++
				waitTime = time.Duration(r.Int31n(600)) * time.Millisecond

				if attempt > 5 {
					log.Printf("failed to connect after 5 retries: %v\n", err)
					return
				}

				continue
			}

			outputCh <- &result{
				id:     clientID,
				client: c,
			}

			break
		}
	}
}

func tryConnect(u url.URL, id int) (*Client, error) {
	conn, _, err := websocket.DefaultDialer.Dial(u.String(), nil)
	if err != nil {
		return nil, err
	}

	return &Client{
		conn: conn,
	}, nil
}

func (c *Client) close() error {
	return c.conn.Close()
}

func (c *Client) readLoop(countCh chan time.Time) {
	for {
		_, r, err := c.conn.NextReader()
		if err != nil {
			log.Printf("client %v: failed to get reader: %v\n", c.conn.LocalAddr(), err)
			return
		}

		p := make([]byte, 1024)
		n, err := r.Read(p)
		if err != nil {
			log.Printf("client %v: failed to read: %v\n", c.conn.LocalAddr(), err)
		}

		_ = string(p[:n])
		countCh <- time.Now()
	}
}
