package balancer

import (
	"context"
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
	time.Sleep(time.Millisecond * 24)
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
	ran := 0
	errs := make(chan error)
	go func() {
		for err := range errs {
			t.Fatal(err)
		}
	}()
	for j := 4; j < 24; j++ {
		for i := 0; i < j; i++ {
			ran++
			wait.Add(1)
			go func() {
				defer wait.Done()

				req, err := http.NewRequest("GET", "/test", nil)
				if err != nil {
					errs <- err
					return
				}
				res, err := balancer.Do(req)
				if err != nil {
					errs <- err
					return
				}

				defer res.Body.Close()
				_, err = ioutil.ReadAll(res.Body)
				if err != nil {
					errs <- err
					return
				}

			}()
		}
		time.Sleep(time.Millisecond * 20)
	}

	wait.Wait()
	close(errs)
	t.Logf("completed %d\n", ran)
}
