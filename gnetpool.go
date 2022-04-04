package gnetpool

import (
	"container/list"
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"sync"
	"time"
)

const (
	idleTimeout   = time.Second * 60
	checkDuration = time.Second * 5
	maxLifecircle = time.Minute * 3
	dialTimeout   = time.Millisecond * 100
)

type GetOption struct {
	Host     string        // ip address
	Port     string        // target port
	Protocol string        // tcp, udp
	Timeout  time.Duration // io timeout
}

type connKey struct {
	host     string
	port     string
	protocol string
}

func (opt GetOption) connKey() connKey {
	return connKey{host: opt.Host, port: opt.Port, protocol: opt.Protocol}
}

type Pool interface {
	io.Closer
	Get(opt GetOption) (net.Conn, error)
}

type pool struct {
	mu     sync.Mutex
	ctx    context.Context
	cancel context.CancelFunc
	pools  map[connKey]*connManager
}

func NewPool() Pool {
	ctx, cancel := context.WithCancel(context.Background())
	return &pool{
		pools:  make(map[connKey]*connManager),
		ctx:    ctx,
		cancel: cancel,
	}
}

func (p *pool) Get(opt GetOption) (net.Conn, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	mgr, ok := p.pools[opt.connKey()]
	if !ok {
		mgr = &connManager{conns: list.New(), dial: func() (net.Conn, error) {
			conn, err := net.DialTimeout(opt.Protocol, fmt.Sprintf("%s:%s", opt.Host, opt.Port), dialTimeout)
			if err != nil {
				return nil, err
			}
			conn.SetDeadline(time.Now().Add(opt.Timeout))
			return conn, nil
		}}
		go mgr.check(p.ctx)
		p.pools[opt.connKey()] = mgr
	}
	return mgr.get()
}

func (p *pool) Close() error {
	p.cancel()
	return nil
}

type connManager struct {
	mu    sync.Mutex
	conns *list.List // use as stack of conn
	dial  func() (net.Conn, error)
}

func (mgr *connManager) get() (*poolConn, error) {
	mgr.mu.Lock()
	defer mgr.mu.Unlock()
	if back := mgr.conns.Back(); back != nil {
		mgr.conns.Remove(back)
		return back.Value.(*poolConn), nil
	}
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
	mgr.mu.Lock()
	defer mgr.mu.Unlock()
	pc.idledAt = time.Now()
	mgr.conns.PushBack(pc)
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

type poolConn struct {
	net.Conn
	manager   *connManager
	createdAt time.Time // first create time of connection
	idledAt   time.Time // current idle begin time of connection
}

func (pc *poolConn) Close() error {
	pc.manager.put(pc)
	return nil
}

func (pc *poolConn) release(elem *list.Element) {
	if err := pc.Conn.Close(); err != nil {
		log.Printf("close conn err: %v", err)
	}
	pc.manager.conns.Remove(elem)
}
