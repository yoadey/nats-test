package main

import (
	"log"
	"math/rand"
	"os"
	"sync/atomic"
	"time"

	"github.com/joho/godotenv"
	"github.com/nats-io/nats.go"
	"golang.org/x/exp/slices"
)

func env(k string) string {
	v := os.Getenv(k)
	if len(v) == 0 {
		log.Fatalf("env [%s] not found", k)
	}
	return v
}

func connect() nats.JetStreamContext {
	err := godotenv.Load()
	if err != nil {
		log.Printf("Error loading .env file")
	}
	url := env("NATS_URL")
	log.Printf("connect to %s ...", url)

	natsUser, userPresent := os.LookupEnv("NATS_USER")
	natsPass, passPresent := os.LookupEnv("NATS_PASSWORD")
	var nc *nats.Conn
	if userPresent && passPresent {
		nc, err = nats.Connect(url, nats.MaxReconnects(-1), nats.UserInfo(natsUser, natsPass), nats.ErrorHandler(func(_ *nats.Conn, _ *nats.Subscription, err error) {
			log.Printf("NATS-Error: %v", err)
		}), nats.DiscoveredServersHandler(func(nc *nats.Conn) {
			log.Printf("NATS-Known servers: %v\n", nc.Servers())
			log.Printf("NATS-Discovered servers: %v\n", nc.DiscoveredServers())
		}))
		if err != nil {
			log.Fatal(err)
		}
	} else {
		nc, err = nats.Connect(url, nats.MaxReconnects(-1), nats.ErrorHandler(func(_ *nats.Conn, _ *nats.Subscription, err error) {
			log.Printf("NATS-Error: %v", err)
		}), nats.DiscoveredServersHandler(func(nc *nats.Conn) {
			log.Printf("NATS-Known servers: %v\n", nc.Servers())
			log.Printf("NATS-Discovered servers: %v\n", nc.DiscoveredServers())
		}))
		if err != nil {
			log.Fatal(err)
		}
	}
	js, err := nc.JetStream()
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("connected to %s!", url)
	return js
}

var kvname = env("NATS_KVSTORE_NAME")

const letterBytes = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

func createData(n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = letterBytes[rand.Intn(len(letterBytes))]
	}
	return b
}

var counter int64
var errorCounter int64

func keyUpdater(key string) {
	js := connect()
	kv, err := js.KeyValue(kvname)
	if err != nil {
		log.Fatalf("[%s]:%v", kvname, err)
	}
	_, err = kv.Create(key, createData(160))
	if err != nil {
		log.Printf("Could not create key %s, it may already exist. Ignoring, error was: %s", key, err)
	}

	log.Print("create data...")

	log.Printf("run updater for %s", key)
	var lastData []byte
	var revision uint64
	for {
		var k nats.KeyValueEntry
		for i := 0; i < 5; i++ {
			nextk, err := kv.Get(key)
			if err != nil {
				log.Printf("get-error:[%s] %v", key, err)
				atomic.AddInt64(&errorCounter, 1)
				if err == nats.ErrKeyNotFound {
					log.Printf("KEY NOT FOUND: [%s] - [%s]", key, err)
				}
			} else {
				if k != nil && k.Revision() > nextk.Revision() {
					log.Printf("get-revision-error:[%s] [%d] [%d]", key, k.Revision(), nextk.Revision())
				}
				k = nextk
			}
		}
		k, err := kv.Get(key)
		if err != nil {
			log.Printf("get-error:[%s] %v", key, err)
			atomic.AddInt64(&errorCounter, 1)
		} else {
			if revision != 0 && k.Revision() < revision {
				log.Printf("revision-error: [%s] is:[%d] expected:[%d]", key, k.Revision(), revision)
			}
			if lastData != nil && k.Revision() == revision && slices.Compare(lastData, k.Value()) != 0 {
				log.Printf("data loss [%s][rev:%d] expected:[%v] is:[%v]", key, revision, string(lastData), string(k.Value()))
			}

			newData := createData(160)
			revision, err = kv.Update(key, newData, k.Revision())
			if err != nil {
				log.Printf("update-error [%s][rev:%d/delta:%d]: %v", key, k.Revision(), k.Delta(), err)
				atomic.AddInt64(&errorCounter, 1)
			} else {
				lastData = newData
			}
			atomic.AddInt64(&counter, 1)
		}
	}
}

func main() {
	log.SetFlags(log.Lmicroseconds | log.Lshortfile)
	log.Print("nats-test-app ...")

	start := time.Now()
	key := "Key1"

	go keyUpdater(key)

	for {
		log.Printf("writes: %d errors: %d (%s)", atomic.LoadInt64(&counter), atomic.LoadInt64(&errorCounter), time.Since(start).Truncate(time.Second))
		time.Sleep(10 * time.Second)
	}
}
