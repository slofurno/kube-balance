package balancer

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
	refresher Refresher
	client    HTTPClient
	interval  time.Duration
	selector  Selector

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

type Refresher interface {
	ListEndpoints(namespace, service string) ([]*target, error)
}

type endpointRefresher struct {
	client HTTPClient
}

func (s *endpointRefresher) ListEndpoints(namespace, service string) ([]*target, error) {
	path := fmt.Sprintf("https://kubernetes.default.svc.cluster.local/api/v1/namespaces/%s/endpoints/%s", namespace, service)
	req, _ := http.NewRequest("GET", path, nil)

	token, err := ioutil.ReadFile("/var/run/secrets/kubernetes.io/serviceaccount/token")
	if err != nil {
		return nil, err
	}
	req.Header.Add("authorization", "Bearer "+string(token))

	res, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}

	defer res.Body.Close()
	var r *response
	if err := json.NewDecoder(res.Body).Decode(&r); err != nil {
		return nil, err
	}

	var endpoints []*target

	for i := range r.Subsets {
		for j := range r.Subsets[i].Addresses {
			k := key{
				name: r.Subsets[i].Addresses[j].TargetRef.Name,
				uid:  r.Subsets[i].Addresses[j].TargetRef.UID,
			}

			endpoints = append(endpoints, &target{
				ip:   r.Subsets[i].Addresses[j].IP,
				port: r.Subsets[i].Ports[0].Port,
				key:  k,
			})
		}
	}

	return endpoints, nil

}

func (s *Pool) refresh() error {
	fmt.Println("refresh")
	endpoints, err := s.refresher.ListEndpoints(s.selector.Namespace, s.selector.Service)
	if err != nil {
		return err
	}

	current := map[key]*target{}
	for i := range endpoints {
		current[endpoints[i].key] = endpoints[i]
	}

	s.mu.Lock()
	s.targets = current
	s.mu.Unlock()

	return nil
}

func (s *Pool) avail() (*target, bool) {
	for k, v := range s.targets {
		if _, ok := s.busy[k]; !ok {
			return v, true
		}
	}

	return nil, false
}

func (s *Pool) serve() {
	fmt.Println("serve")
	s.mu.Lock()
	defer s.mu.Unlock()

	for {
		ta, ok := s.avail()
		if !ok {
			return
		}

		fmt.Println("one avail")

		select {
		case next := <-s.waiting:
			fmt.Println("serving to waiter")
			s.busy[ta.key] = struct{}{}
			next.target <- ta

		default:
			return
		}
	}
}

func (s *Pool) poll(ctx context.Context) {
	ticker := time.NewTicker(s.interval)
	for {
		select {
		case <-ctx.Done():
			fmt.Println("cancel called")
			return
		case <-ticker.C:
			s.refresh()
			s.serve()
		}
	}
}

type HTTPClient interface {
	Do(req *http.Request) (*http.Response, error)
}

func New(ctx context.Context, client HTTPClient, config *Config) *Pool {
	return newBalancer(ctx, client, config, &endpointRefresher{client: client})
}

func newBalancer(ctx context.Context, client HTTPClient, config *Config, refresher Refresher) *Pool {
	pool := &Pool{
		client:    client,
		interval:  config.Interval,
		selector:  config.Selector,
		waiting:   make(chan *pass, 128),
		refresher: refresher,
		busy:      map[key]struct{}{},
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
	n, err := s.rc.Read(p)
	if err == io.EOF {
		s.callonclose()
	}
	return n, err
}

func (s *bodyReader) Close() error {
	err := s.rc.Close()
	s.callonclose()
	return err
}

func (s *bodyReader) callonclose() {
	if fn := s.onclose; fn != nil {
		s.onclose = nil
		fn()
	}
}

func (s *Pool) Do(ireq *http.Request) (*http.Response, error) {
	p := newPass()
	//TODO: close waiting
	s.waiting <- p
	fmt.Println("added to waiting")
	pod := <-p.target

	fmt.Println("got pod")

	path := fmt.Sprintf("http://%s:%d%s", pod.ip, pod.port, ireq.URL.Path)
	req, err := http.NewRequest(ireq.Method, path, nil)
	fmt.Println(req, err)
	if err != nil {
		return nil, err
	}

	res, err := s.client.Do(req)

	res.Body = &bodyReader{
		rc: res.Body,
		onclose: func() {
			s.mu.Lock()
			delete(s.busy, pod.key)
			s.mu.Unlock()
			s.serve()
		},
	}

	return res, err
}
