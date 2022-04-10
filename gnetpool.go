package gnetpool

import (
	"container/list"
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"sync"
	"time"

	"go.uber.org/atomic"
)

const (
	idleTimeout   = time.Second * 60
	checkDuration = time.Second * 5
	maxLifecircle = time.Minute * 3
	dialTimeout   = time.Millisecond * 200
)

const (
	defaultIdleWaitTimeout = time.Second * 3
	defaultMaxIdle         = 60000
	defaultMaxActive       = 60000
)

type GetOption struct {
	Host     string // ip address
	Port     string // target port
	Protocol string // tcp, udp
}

type connKey struct {
	host     string
	port     string
	protocol string
}

func (opt GetOption) connKey() connKey {
	return connKey{host: opt.Host, port: opt.Port, protocol: opt.Protocol}
}

type Pool struct {
	mu      sync.Mutex
	ctx     context.Context
	cancel  context.CancelFunc
	options poolOptions
	pools   map[connKey]*connManager
}

type poolOptions struct {
	idleWaitTimeout time.Duration
	maxIdle         int32
	maxActive       int32
}

type PoolOption func(opt *poolOptions)

func WithIdleWaitTimeout(t time.Duration) PoolOption {
	return func(opt *poolOptions) {
		opt.idleWaitTimeout = t
	}
}

func WithMaxIdle(n int32) PoolOption {
	return func(opt *poolOptions) {
		opt.maxIdle = n
	}
}

func WithMaxActive(n int32) PoolOption {
	return func(opt *poolOptions) {
		opt.maxActive = n
	}
}

func NewPool(opts ...PoolOption) *Pool {
	ctx, cancel := context.WithCancel(context.Background())
	opt := poolOptions{
		idleWaitTimeout: defaultIdleWaitTimeout,
		maxIdle:         defaultMaxIdle,
		maxActive:       defaultMaxActive,
	}
	for _, o := range opts {
		o(&opt)
	}
	return &Pool{
		pools:   make(map[connKey]*connManager),
		ctx:     ctx,
		cancel:  cancel,
		options: opt,
	}
}

func (p *Pool) Get(opt GetOption) (net.Conn, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	mgr, ok := p.pools[opt.connKey()]
	if !ok {
		mgr = &connManager{
			conns:    list.New(),
			options:  p.options,
			dial:     p.getDialFunc(opt),
			active:   atomic.NewInt32(0),
			activeCh: make(chan struct{}),
			idle:     atomic.NewInt32(0),
		}
		mgr.initConns()
		go mgr.check(p.ctx)
		p.pools[opt.connKey()] = mgr
	}
	return mgr.get()
}

func (p *Pool) getDialFunc(opt GetOption) func() (net.Conn, error) {
	return func() (net.Conn, error) {
		conn, err := net.DialTimeout(opt.Protocol, fmt.Sprintf("%s:%s", opt.Host, opt.Port), dialTimeout)
		if err != nil {
			return nil, err
		}
		return conn, nil
	}
}

func (p *Pool) Close() error {
	p.cancel()
	return nil
}

type poolConn struct {
	net.Conn
	manager   *connManager
	createdAt time.Time // first create time of connection
	idledAt   time.Time // current idle begin time of connection
}

func (pc *poolConn) Close() error {
	pc.SetDeadline(time.Time{})
	pc.manager.put(pc)
	return nil
}

func (pc *poolConn) release(elem *list.Element) {
	if err := pc.Conn.Close(); err != nil {
		log.Printf("close conn err: %v", err)
	}
	pc.manager.conns.Remove(elem)
}

type connManager struct {
	mu      sync.Mutex
	conns   *list.List // use as stack of conn
	options poolOptions
	dial    func() (net.Conn, error)

	active   *atomic.Int32 // active connection num
	activeCh chan struct{} // use to sync the max active conn
	idle     *atomic.Int32 // idle connection num
}

func (mgr *connManager) initConns() {
	if mgr.options.maxActive > 0 {
		go func() {
			for i := int32(0); i < mgr.options.maxActive; i++ {
				mgr.activeCh <- struct{}{}
			}
		}()
	}
}

var ErrWaitActiveTimeout = errors.New("wait active connection timeout")

func (mgr *connManager) get() (*poolConn, error) {
	opts := mgr.options
	if opts.maxActive > 0 {
		select {
		case <-time.After(opts.idleWaitTimeout):
			return nil, ErrWaitActiveTimeout
		case <-mgr.activeCh:
			mgr.active.Add(1)
		}
	}
	if conn, ok := mgr.getIdleConn(); ok {
		mgr.idle.Sub(1)
		return conn, nil
	}
	return mgr.getNewConn()
}

func (mgr *connManager) getIdleConn() (*poolConn, bool) {
	mgr.mu.Lock()
	defer mgr.mu.Unlock()
	if back := mgr.conns.Back(); back != nil {
		mgr.conns.Remove(back)
		return back.Value.(*poolConn), true
	}
	return nil, false
}

func (mgr *connManager) getNewConn() (*poolConn, error) {
	newConn, err := mgr.dial()
	if err != nil {
		return nil, err
	}
	return &poolConn{
		Conn:      newConn,
		manager:   mgr,
		createdAt: time.Now(),
	}, nil
}

func (mgr *connManager) put(pc *poolConn) error {
	mgr.active.Sub(1)
	if mgr.options.maxActive > 0 {
		go func() { mgr.activeCh <- struct{}{} }()
	}
	maxIdle := mgr.options.maxIdle
	if maxIdle > 0 && mgr.idle.Load() >= maxIdle {
		// max idle conn limit, just close this connection
		return pc.Conn.Close()
	}
	mgr.mu.Lock()
	defer mgr.mu.Unlock()
	pc.idledAt = time.Now()
	mgr.conns.PushBack(pc)
	mgr.idle.Add(1)
	return nil
}

func (mgr *connManager) check(ctx context.Context) {
	ticker := time.NewTicker(checkDuration)
	defer ticker.Stop()
	checkIdle := func() {
		mgr.mu.Lock()
		defer mgr.mu.Unlock()
		for cur := mgr.conns.Front(); cur != nil; cur = cur.Next() {
			pc := cur.Value.(*poolConn)
			if pc.idledAt.Add(idleTimeout).Before(time.Now()) ||
				pc.createdAt.Add(maxLifecircle).Before(time.Now()) {
				pc.release(cur)
			}
		}
	}
	for {
		select {
		case <-ctx.Done():
			log.Printf("check ctx done, reason: %v", ctx.Err())
			return
		case <-ticker.C:
			checkIdle()
		}
	}
}
