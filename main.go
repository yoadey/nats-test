package main

import (
	"fmt"
	"log"
	"math/rand"
	"os"
	"strconv"
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

const letterBytes = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

func createData(n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = letterBytes[rand.Intn(len(letterBytes))]
	}
	return b
}

func getOrCreateKvStore(kvname string) (nats.KeyValue, error) {
	js := connect()

	kvExists := false
	existingKvnames := js.KeyValueStoreNames()
	for existingKvname := range existingKvnames {
		if existingKvname == kvname {
			kvExists = true
			break
		}
	}
	if !kvExists {
		log.Printf("Creating kv store %s", kvname)
		return js.CreateKeyValue(&nats.KeyValueConfig{
			Bucket:   kvname,
			Replicas: 3,
			Storage:  nats.FileStorage,
		})
	} else {
		return js.KeyValue(kvname)
	}
}

func Abs(x int64) int64 {
	if x < 0 {
		return -x
	}
	return x
}

var counter int64
var errorCounter int64

func keyUpdater(kvname string, numKeys int) {

	kv, err := getOrCreateKvStore(kvname)
	if err != nil {
		log.Fatalf("[%s]:%v", kvname, err)
	}

	for i := 0; i < numKeys; i++ {
		key := fmt.Sprintf("key-%d", i)
		kv.Create(key, createData(160))
	}

	log.Printf("run updater for bucket %s", kvname)
	lastData := make(map[string][]byte)
	revisions := make(map[string]uint64)
	for {
		r := rand.Intn(numKeys)
		key := fmt.Sprintf("key-%d", r)

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
				if k != nil && Abs(int64(k.Revision())-int64(nextk.Revision())) > 2 {
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
			if revisions[key] != 0 && Abs(int64(k.Revision())-int64(revisions[key])) > 2 {
				log.Printf("revision-error: [%s/%s] is:[%d] expected:[%d]", kvname, key, k.Revision(), revisions[key])
			} else {
				lastDataVal, ok := lastData[key]
				if ok && k.Revision() == revisions[key] && slices.Compare(lastDataVal, k.Value()) != 0 {
					log.Printf("data loss [%s/%s][rev:%d] expected:[%v] is:[%v]", kvname, key, revisions[key], string(lastDataVal), string(k.Value()))
				}
			}

			newData := createData(160)
			revisions[key], err = kv.Update(key, newData, k.Revision())
			if err != nil {
				log.Printf("update-error [%s/%s][rev:%d/delta:%d]: %v", kvname, key, k.Revision(), k.Delta(), err)
				atomic.AddInt64(&errorCounter, 1)
			} else {
				lastData[key] = newData
			}
			atomic.AddInt64(&counter, 1)
		}
	}
}

func envOrDefault(envName string, defaultVal int) int {
	envValue, streamCountPresent := os.LookupEnv(envName)
	if streamCountPresent {
		i64, err := strconv.ParseInt(envValue, 10, 32)
		if err != nil {
			panic(err)
		}
		return int(i64)
	} else {
		return defaultVal
	}
}

func main() {
	log.SetFlags(log.Lmicroseconds | log.Lshortfile)
	log.Print("nats-test-app ...")

	start := time.Now()

	streamPrefix := env("STREAM_PREFIX")

	streamCount := envOrDefault("NUM_STREAMS", 100)
	keysCount := envOrDefault("NUM_KEYS", 100)

	for i := 0; i < streamCount; i++ {
		go keyUpdater(fmt.Sprintf("%s-%d", streamPrefix, i), keysCount)
	}

	for {
		log.Printf("writes: %d errors: %d (%s)", atomic.LoadInt64(&counter), atomic.LoadInt64(&errorCounter), time.Since(start).Truncate(time.Second))
		time.Sleep(10 * time.Second)
	}
}
