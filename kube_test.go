package balancer

import (
	"context"
	"fmt"
	"net/http"
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

type printClient struct{}

func (s *printClient) Do(req *http.Request) (*http.Response, error) {
	fmt.Printf("%#v\n", req)
	return &http.Response{}, nil
}

func TestA(t *testing.T) {
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
	balancer := New(ctx, client, &Config{Interval: time.Second, Selector: Selector{}})

	for i := 0; i < 10; i++ {
		go func() {
			res, err := balancer.Do(http.NewRequest("GET", "/test", nil))
			if err != nil {
				t.Error(err)
			}
			defer res.Body.Close()
		}()
	}

}
