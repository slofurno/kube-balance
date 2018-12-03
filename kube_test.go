package balancer

import (
	"context"
	"io"
	"io/ioutil"
	"net/http"
	"strings"
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

type readCloser struct {
	reader io.Reader
}

func (rc *readCloser) Read(p []byte) (int, error) {
	return rc.reader.Read(p)
}

func (rc *readCloser) Close() error { return nil }

type printClient struct {
	delay time.Duration
}

func (s *printClient) Do(req *http.Request) (*http.Response, error) {
	time.Sleep(s.delay)
	return &http.Response{Body: &readCloser{reader: strings.NewReader(req.URL.Host)}}, nil
}

func TestBalancer(t *testing.T) {
	refresher := &podRefresher{
		endpoints: [][]*target{
			[]*target{
				&target{
					ip:   "1.1.1.0",
					port: 80,
					key:  key{name: "zero", uid: "000"},
				},
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
					ip:   "1.1.1.0",
					port: 80,
					key:  key{name: "zero", uid: "000"},
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
			[]*target{
				&target{
					ip:   "1.1.1.0",
					port: 80,
					key:  key{name: "zero", uid: "000"},
				},
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

	client := &printClient{delay: time.Millisecond * 3}
	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond*1200)
	defer cancel()

	balancer := newBalancer(ctx, client, &Config{Interval: time.Millisecond * 100, Selector: Selector{}}, refresher)

	wait := sync.WaitGroup{}
	a := 3
	b := 47
	torun := ((a + b - 1) * (b - a)) >> 1
	hosts := make([]string, torun)
	ret := make([]int, torun)
	ran := 0

	for j := a; j < b; j++ {
		for i := 0; i < j; i++ {
			wait.Add(1)
			go func(n int) {
				defer wait.Done()
				req, err := http.NewRequest("GET", "/test", nil)
				if err != nil {
					t.Fatal(err)
					ret[n] = 1
					return
				}

				res, err := balancer.Do(req)
				if err != nil {
					if err != ErrBalancerShuttingDown {
						t.Fatal(err)
					}
					ret[n] = 1
					return
				}

				defer res.Body.Close()
				body, err := ioutil.ReadAll(res.Body)
				if err != nil {
					t.Fatal(err)
					ret[n] = 1
					return
				}

				hosts[n] = string(body)
				ret[n] = 2
			}(ran)
			ran++
		}
		time.Sleep(time.Millisecond * 2)
	}

	wait.Wait()

	if ran != torun {
		t.Errorf("expected %d request, got: %d\n", torun, ran)
	}

	completed := 0
	failed := 0
	for i := range ret {
		switch ret[i] {
		case 1:
			failed++
		case 2:
			completed++
		}

	}

	hostcount := map[string]int{}
	for i := range hosts {
		hostcount[hosts[i]] = hostcount[hosts[i]] + 1
	}

	t.Logf("ran: %d, completed %d, past deadline: %d\n", ran, completed, failed)

	for host, count := range hostcount {
		t.Logf("%s: %d\n", host, count)
	}

}
