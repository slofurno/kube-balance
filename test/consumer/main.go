package main

import (
	"context"
	"fmt"
	balancer "github.com/Getsidecar/kube-balancer"
	"io/ioutil"
	"net/http"
	"sync"
	"time"
)

func consume(pool *balancer.Pool) ([]byte, error) {
	var err error
	var res *http.Response
	for i := 0; i < 3; i++ {
		req, _ := http.NewRequest("GET", "/", nil)
		ctx, cancel := context.WithTimeout(context.Background(), time.Second*2)
		req = req.WithContext(ctx)
		res, err = pool.Do(req)
		cancel()

		if err == nil {
			b, err := ioutil.ReadAll(res.Body)
			res.Body.Close()
			return b, err
		}

		if res != nil && res.Body != nil {
			res.Body.Close()
		}
	}

	return nil, err
}

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	root, err := balancer.New()
	if err != nil {
		panic(err)
	}
	pool := root.Pool(ctx, &balancer.Config{Interval: time.Second * 1, Selector: balancer.Selector{Namespace: "default", Service: "kube-balance"}})

	responses := make([]string, 10*100)
	wg := &sync.WaitGroup{}

	for j := 0; j < 10; j++ {
		wg.Add(1)

		go func(j int) {
			for i := 0; i < 100; i++ {
				b, err := consume(pool)
				if err != nil {
					panic(err)
				}
				responses[j*100+i] = string(b)
			}

			wg.Done()
		}(j)
	}

	wg.Wait()
	for i := range responses {
		fmt.Println(responses[i])
	}

	time.Sleep(time.Minute)
}
