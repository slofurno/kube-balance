package balancer

import (
	"context"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"sync"
	"testing"
	"time"
)

type podRefresher struct {
	endpoints [][]*target
	k         int
}

func (s *podRefresher) ListEndpoints(namespace, service string) ([]*target, error) {
	ret := s.endpoints[s.k]
	s.k++

	if s.k >= len(s.endpoints) {
		s.k = len(s.endpoints) - 1
	}

	return ret, nil
}

type readCloser struct{}

func (rc *readCloser) Read(p []byte) (int, error) {
	return 0, io.EOF
}

func (rc *readCloser) Close() error { return nil }

type printClient struct{}

func (s *printClient) Do(req *http.Request) (*http.Response, error) {
	fmt.Printf("%#v\n", req)
	time.Sleep(time.Second)
	return &http.Response{Body: &readCloser{}}, nil
}

func TestBalancer(t *testing.T) {
	refresher := &podRefresher{
		endpoints: [][]*target{
			[]*target{
				&target{
					ip:   "1.1.1.1",
					port: 80,
					key:  key{name: "one", uid: "111"},
				},
				&target{
					ip:   "1.1.1.2",
					port: 80,
					key:  key{name: "two", uid: "222"},
				},
				&target{
					ip:   "1.1.1.3",
					port: 80,
					key:  key{name: "three", uid: "222"},
				},
			},
			[]*target{
				&target{
					ip:   "1.1.1.1",
					port: 80,
					key:  key{name: "one", uid: "111"},
				},
				&target{
					ip:   "1.1.1.2",
					port: 80,
					key:  key{name: "two", uid: "222"},
				},
				&target{
					ip:   "1.1.1.3",
					port: 80,
					key:  key{name: "three", uid: "222"},
				},
			},

			[]*target{
				&target{
					ip:   "1.1.1.2",
					port: 80,
					key:  key{name: "two", uid: "222"},
				},
				&target{
					ip:   "1.1.1.3",
					port: 80,
					key:  key{name: "three", uid: "222"},
				},
			},

			[]*target{
				&target{
					ip:   "1.1.1.4",
					port: 80,
					key:  key{name: "four", uid: "444"},
				},
				&target{
					ip:   "1.1.1.2",
					port: 80,
					key:  key{name: "two", uid: "222"},
				},
				&target{
					ip:   "1.1.1.3",
					port: 80,
					key:  key{name: "three", uid: "222"},
				},
			},
		},
	}

	client := &printClient{}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*5)
	defer cancel()

	balancer := newBalancer(ctx, client, &Config{Interval: time.Second, Selector: Selector{}}, refresher)

	wait := sync.WaitGroup{}
	wait.Add(10)
	for i := 0; i < 10; i++ {
		go func() {
			fmt.Println("starting...")
			req, err := http.NewRequest("GET", "/test", nil)
			if err != nil {
				t.Fatal(err)
			}
			res, err := balancer.Do(req)
			if err != nil {
				fmt.Println(err)
				t.Fatal(err)
			}

			defer res.Body.Close()
			_, err = ioutil.ReadAll(res.Body)
			if err != nil {
				t.Fatal(err)
			}

			wait.Done()
		}()
	}

	wait.Wait()
}
