package main

import (
	"fmt"
	"log"
	"math/rand"
	"os"
	"strconv"
	"strings"
	"sync"
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

var l sync.Mutex
var allKvStoreNames []*string

func getKeyStores(js nats.JetStreamContext) []*string {
	l.Lock()
	if len(allKvStoreNames) == 0 {
		kvStoreNames := js.KeyValueStoreNames()
		i := 0
		for existingKvname := range kvStoreNames {
			allKvStoreNames = append(allKvStoreNames, &existingKvname)
			i = i + 1
		}
	}
	l.Unlock()
	return allKvStoreNames
}

func getOrCreateKvStore(kvname string) (nats.KeyValue, error) {
	js := connect()

	kvExists := false
	existingKvnames := getKeyStores(js)
	for _, existingKvname := range existingKvnames {
		if *existingKvname == kvname {
			kvExists = true
			break
		}
	}
	if !kvExists {
		log.Printf("Creating kv store %s", kvname)
		for retries := range 3 {
			kv, err := js.CreateKeyValue(&nats.KeyValueConfig{
				Bucket:   kvname,
				Replicas: 3,
				Storage:  nats.FileStorage,
			})
			if err == nil {
				return kv, nil
			} else {
				log.Printf("Creating kv store %s failed, retrying...", kvname)
			}

			log.Printf("Creating kv store %s failed, retrying %d/3...", kvname, retries)
		}
		return nil, fmt.Errorf("Could not create kvstore %s!", kvname)
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
	defer log.Printf("Thread for kvstore [%s] had an error and quit", kvname)

	kv, err := getOrCreateKvStore(kvname)
	if err != nil {
		log.Fatalf("[%s]:%v", kvname, err)
	}

creation:
	for i := 0; i < numKeys; i++ {
		key := fmt.Sprintf("key-%d", i)

		for retries := range 3 {
			_, err := kv.Create(key, createData(160))
			if err != nil && !strings.Contains(err.Error(), "key exists") {
				log.Printf("Creating entry %s %s failed, retrying %d/3...", kvname, key, retries)
			} else {
				continue creation
			}
		}
	}

	log.Printf("run updater for bucket %s", kvname)
	lastData := make(map[string]struct {
		revision uint64
		data     []byte
		updated  time.Time
	})
	for {
		r := rand.Intn(numKeys)
		key := fmt.Sprintf("key-%d", r)

		var k nats.KeyValueEntry
		for i := 0; i < 5; i++ {
			nextk, err := kv.Get(key)
			if err != nil {
				log.Printf("GET ERROR: [%s %s] %v", kvname, key, err)
				atomic.AddInt64(&errorCounter, 1)
				if err == nats.ErrKeyNotFound {
					log.Printf("KEY NOT FOUND: [%s %s] - [%s]", kvname, key, err)
				}
			} else {
				if k != nil && Abs(int64(k.Revision())-int64(nextk.Revision())) > 2 {
					var lastUpdated int64;
					if k.Created().After(nextk.Created()) {
						lastUpdated = time.Now().Sub(k.Created()).Milliseconds();
					} else {
						lastUpdated = time.Now().Sub(nextk.Created()).Milliseconds();
					} 
					log.Printf("COMPARE REVISIONS ERROR: [%s %s][%dms old] [%d] [%d], dataDiff: %t",
						kvname, key, lastUpdated, k.Revision(), nextk.Revision(), slices.Compare(k.Value(), nextk.Value()) != 0)
				}
				k = nextk
			}
		}
		k, err := kv.Get(key)
		if err != nil {
			log.Printf("GET ERROR:[%s] %v", key, err)
			atomic.AddInt64(&errorCounter, 1)
		} else {
			lastDataVal, ok := lastData[key]
			if ok && k.Revision() != lastDataVal.revision {
				log.Printf("REVISION ERROR: [%s %s][%dms old] is:[%d] expected:[%d], dataDiff: %t",
					kvname, key, time.Now().Sub(lastDataVal.updated).Milliseconds(), k.Revision(), lastDataVal.revision, slices.Compare(lastDataVal.data, k.Value()) != 0)
			} else if ok && slices.Compare(lastDataVal.data, k.Value()) != 0 {
				log.Printf("DATA LOSS [%s/%s][rev:%d][%dms old] is:[%v] expected:[%v]",
					kvname, key, time.Now().Sub(lastDataVal.updated).Milliseconds(), lastDataVal.revision, string(k.Value()), string(lastDataVal.data))
			}

			newData := createData(160)
			time.Now()
			newRevision, err := kv.Update(key, newData, k.Revision())
			if err != nil {
				log.Printf("UPDATE ERROR: [%s/%s][rev:%d/delta:%d]: %v", kvname, key, k.Revision(), k.Delta(), err)
				atomic.AddInt64(&errorCounter, 1)
			} else {
				lastData[key] = struct {
					revision uint64
					data     []byte
					updated  time.Time
				}{
					revision: newRevision,
					data:     newData,
					updated:  time.Now(),
				}
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
