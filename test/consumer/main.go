package main

import (
	"context"
	"fmt"
	"github.com/Getsidecar/kube-balance"
	"io/ioutil"
	"net/http"
	"sync"
	"time"
)

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	root, err := balancer.New()
	if err != nil {
		panic(err)
	}
	pool := root.Pool(ctx, &balancer.Config{Interval: time.Second * 5, Selector: balancer.Selector{Namespace: "default", Service: "kube-balance"}})

	responses := make([]string, 10*100)
	wg := &sync.WaitGroup{}

	for j := 0; j < 10; j++ {
		wg.Add(1)

		go func(j int) {
			for i := 0; i < 100; i++ {
				req, _ := http.NewRequest("GET", "/", nil)
				res, err := pool.Do(req)
				if err != nil {
					panic(err)
				}

				b, err := ioutil.ReadAll(res.Body)
				responses[j*100+i] = string(b)
			}

			wg.Done()
		}(j)
	}

	wg.Wait()
	for i := range responses {
		fmt.Println(responses[i])
	}
}
