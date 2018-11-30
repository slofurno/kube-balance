package balancer

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"net"
	"net/http"
	"net/url"
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
	Selector   Selector
	Interval   time.Duration
	MaxWaiting int
}

const defaultMaxWaiting int = 1024

type Pool struct {
	refresher Refresher
	client    HTTPClient
	interval  time.Duration
	selector  Selector

	mu       sync.Mutex
	waiting  chan *pass
	busy     map[key]struct{}
	targets  map[key]*target
	isClosed bool
	closed   chan struct{}
}

func (s *Pool) Stat() {
	s.mu.Lock()
	defer s.mu.Unlock()

	for k, target := range s.targets {
		fmt.Printf("%s %s:%d\n", k.name, target.ip, target.port)
	}
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
	s.mu.Lock()
	defer s.mu.Unlock()

	for {
		if s.isClosed {
			return
		}
		ta, ok := s.avail()
		if !ok {
			return
		}

		select {
		case next := <-s.waiting:
			s.busy[ta.key] = struct{}{}
			next.target <- ta

		default:
			return
		}
	}
}

func (s *Pool) poll(ctx context.Context) {
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			s.mu.Lock()
			close(s.closed)
			s.isClosed = true
			s.mu.Unlock()
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

type kubeClient struct {
	client *http.Client
}

func New() (*kubeClient, error) {
	cert, err := ioutil.ReadFile("/var/run/secrets/kubernetes.io/serviceaccount/ca.crt")
	if err != nil {
		return nil, err
	}

	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(cert) {
		return nil, fmt.Errorf("error appending cert to pool")
	}

	client := &http.Client{
		Transport: &http.Transport{
			Proxy: http.ProxyFromEnvironment,
			DialContext: (&net.Dialer{
				Timeout:   30 * time.Second,
				KeepAlive: 30 * time.Second,
				DualStack: true,
			}).DialContext,
			MaxIdleConns:          100,
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
			ExpectContinueTimeout: 1 * time.Second,
			TLSClientConfig: &tls.Config{
				MinVersion: tls.VersionTLS12,
				RootCAs:    pool,
			},
		},
	}

	return &kubeClient{client: client}, nil
}

func (s *kubeClient) Pool(ctx context.Context, config *Config) *Pool {
	return newBalancer(ctx, s.client, config, &endpointRefresher{client: s.client})
}

func newBalancer(ctx context.Context, client HTTPClient, config *Config, refresher Refresher) *Pool {
	maxWaiting := defaultMaxWaiting
	if config.MaxWaiting > 0 {
		maxWaiting = config.MaxWaiting
	}
	pool := &Pool{
		client:    client,
		interval:  config.Interval,
		selector:  config.Selector,
		waiting:   make(chan *pass, maxWaiting),
		refresher: refresher,
		busy:      map[key]struct{}{},
		closed:    make(chan struct{}),
	}

	go pool.poll(ctx)
	return pool
}

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

var ErrBalancerShuttingDown = errors.New("balancer shutting down")
var ErrOverfilledWaitBucket = errors.New("overfilled wait bucket")

func (s *Pool) Do(ireq *http.Request) (*http.Response, error) {
	p := newPass()

	select {
	case s.waiting <- p:

	default:
		return nil, ErrOverfilledWaitBucket
	}
	var pod *target

	select {
	case pod = <-p.target:

	case <-s.closed:
		return nil, ErrBalancerShuttingDown
	}

	onerror := func() {
		s.mu.Lock()
		delete(s.busy, pod.key)
		s.mu.Unlock()
		s.serve()
	}

	path := fmt.Sprintf("http://%s:%d%s", pod.ip, pod.port, ireq.URL.Path)
	req, err := replacePath(ireq, path)
	if err != nil {
		onerror()
		return nil, err
	}

	res, err := s.client.Do(req)
	if err != nil {
		onerror()
		return nil, err
	}

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

func replacePath(r *http.Request, path string) (*http.Request, error) {
	// shallow copy of the struct
	r2 := new(http.Request)
	*r2 = *r
	// deep copy of the Header
	r2.Header = make(http.Header, len(r.Header))
	for k, s := range r.Header {
		r2.Header[k] = append([]string(nil), s...)
	}

	u, err := url.Parse(path)
	if err != nil {
		return nil, err
	}
	r2.URL = u
	r2.Host = u.Host
	return r2, nil
}
