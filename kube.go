package kubepool

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"sync"
	"time"
)

type metadata struct {
	Name      string            `json:"name"`
	Namespace string            `json:"namespace"`
	UID       string            `json:"uid"`
	Labels    map[string]string `json:"labels"`
}

type port struct {
	Name string `json:"name"`
	Port int    `json:"port"`
}

type address struct {
	IP        string `json:"ip"`
	TargetRef struct {
		Name string `json:"name"`
		UID  string `json:"uid"`
	} `json:"targetRef"`
}

type subset struct {
	Addresses []address `json:"addresses"`
	Ports     []port    `json:"ports"`
}

type item struct {
	Metadata metadata `json:"metadata"`
	Subsets  []subset `json:"subsets"`
}

type response struct {
	Metadata metadata `json:"metadata"`
	Subsets  []subset `json:"subsets"`
	//Items []item `json:"items"`
}

type Selector struct {
	Namespace string
	Service   string
}

type Config struct {
	Selector Selector
	Interval time.Duration
}

type Pool struct {
	client   *http.Client
	interval time.Duration
	selector Selector

	mu      sync.Mutex
	waiting chan *pass
	busy    map[key]struct{}
	targets map[key]*target
}

type key struct {
	name string
	uid  string
}

type target struct {
	ip   string
	port int
	key  key
}

type pass struct {
	target chan *target
}

func newPass() *pass {
	return &pass{
		target: make(chan *target),
	}
}

func (s *Pool) refresh() error {
	path := fmt.Sprintf("https://kubernetes.default.svc.cluster.local/api/v1/namespaces/%s/endpoints/%s", s.selector.Namespace, s.selector.Service)
	req := http.NewRequest("GET", path, nil)

	token, err := ioutil.ReadFile("/var/run/secrets/kubernetes.io/serviceaccount/token")
	if err != nil {
		return err
	}
	req.Header.Add("authorization", "Bearer "+string(token))

	res, err := s.client.Do(req)
	if err != nil {
		return err
	}

	defer res.Body.Close()
	var r *response
	if err := json.NewDecoder(res.Body).Decode(&r); err != nil {
		return err
	}

	current := map[key]*target{}
	s.mu.Lock()

	for i := range r.Subsets {
		for j := range r.Subsets[i].Addresses {
			k := key{
				name: r.Subsets[i].Addresses[j].TargetRef.Name,
				uid:  r.Subsets[i].Addresses[j].TargetRef.UID,
			}

			current[k] = &target{
				ip:   r.Subsets[i].Addresses[j].IP,
				port: r.Subsets[i].Ports[0].Port,
				key:  k,
			}
		}
	}

	s.targets = current
	s.mu.Unlock()

	return nil
}

func (s *Pool) avail() (*target, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for k, v := range s.targets {
		if _, ok := s.busy[k]; !ok {
			s.busy[k] = struct{}{}
			return v, true
		}
	}

	return nil, false
}

func (s *Pool) serve() {
	for {
		select {
		case next := <-s.waiting:
			ta, ok := s.avail()
			if !ok {
				return
			}
			next.target <- ta

		default:
			return
		}
	}
}

func (s *Pool) poll(ctx context.Context) {
	ticker := time.NewTicker(pool.interval)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.refresh()
		}
	}
}

func New(ctx context.Context, client *http.Client, config *Config) *Pool {
	pool := &Pool{
		client:   client,
		interval: config.Interval,
		selector: config.Selector,
		waiting:  make(chan struct{}, 128),
	}

	go pool.poll(ctx)
	return pool
}

//https://kubernetes.default.svc.cluster.local/api/v1/namespaces/pla-structure/endpoints/pla-structure-worker" -H "authorization: Bearer $(cat /var/run/secrets/kubernetes.io/serviceaccount/token)"

type bodyReader struct {
	rc      io.ReadCloser
	onclose func()
}

func (s *bodyReader) Read(p []byte) (int, error) {
	return s.rc.Read(p)
}

func (s *bodyReader) Close() error {
	defer s.onclose()
	return s.Close()
}

func (s *Pool) Do(ireq *http.Request) (*http.Response, error) {
	p := newPass()
	//TODO: close waiting
	s.waiting <- p
	pod := <-p.target
	k := p.target.key

	path := fmt.Sprintf("%s:%d%s", pod.ip, pod.port, ireq.URL.Path)
	req := http.NewRequest(req.Method, path, nil)
	res, err := s.client.Do(req)

	res.Body = &bodyReader{
		rc: res.Body,
		onclose: func() {
			s.mu.Lock()
			defer s.mu.Unlock()
			delete(s.busy, k)
		},
	}
}
