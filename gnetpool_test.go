package gnetpool

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestPoolGet(t *testing.T) {
	pool := NewPool()
	defer pool.Close()
	opt, cancel := MockServerOption()
	defer cancel()
	conn, err := pool.Get(opt)
	assert.Nil(t, err)
	addr := conn.LocalAddr().String() + conn.RemoteAddr().String()
	t.Log(addr)
	_ = conn.Close()

	conn, err = pool.Get(opt)
	assert.Nil(t, err)
	addr2 := conn.LocalAddr().String() + conn.RemoteAddr().String()
	t.Log(addr2)
	_ = conn.Close()

	assert.Equal(t, addr, addr2)
	assert.Equal(t, pool.pools[opt.connKey()].active.Load(), int32(0))
	assert.Equal(t, pool.pools[opt.connKey()].idle.Load(), int32(1))
}

func BenchmarkPoolGet(b *testing.B) {
	pool := NewPool()
	opt, cancel := MockServerOption()
	defer cancel()
	for i := 0; i < b.N; i++ {
		conn, err := pool.Get(opt)
		assert.Nil(b, err)
		_ = conn.Close()
	}
}

func TestPoolGetForMaxActive(t *testing.T) {
	pool := NewPool(WithMaxActive(1), WithIdleWaitTimeout(time.Millisecond*100))
	defer pool.Close()
	opt, cancel := MockServerOption()
	defer cancel()
	conn, err := pool.Get(opt)
	assert.Nil(t, err)
	go func() {
		time.Sleep(time.Millisecond * 100 * 2)
		_ = conn.Close()
	}()
	conn, err = pool.Get(opt)
	assert.Equal(t, err, err)
	assert.Equal(t, pool.pools[opt.connKey()].active.Load(), int32(1))
	assert.Equal(t, pool.pools[opt.connKey()].idle.Load(), int32(0))
}

func TestPoolGetForMaxIdle(t *testing.T) {
	pool := NewPool(WithMaxIdle(1))
	defer pool.Close()
	opt, cancel := MockServerOption()
	defer cancel()

	conn1, err1 := pool.Get(opt)
	assert.Nil(t, err1)

	conn2, err2 := pool.Get(opt)
	assert.Nil(t, err2)

	_ = conn1.Close()
	_ = conn2.Close()

	assert.Equal(t, pool.pools[opt.connKey()].active.Load(), int32(0))
	assert.Equal(t, pool.pools[opt.connKey()].idle.Load(), int32(1))

	// one conn is release, then get 2 conn is diff
	newConn1, newErr1 := pool.Get(opt)
	assert.Nil(t, newErr1, newErr1)
	newConn2, newErr2 := pool.Get(opt)
	assert.Nil(t, newErr2, newErr2)

	assert.NotEqual(t, newConn1.LocalAddr().String(), newConn2.LocalAddr().String())
}

func MockServerOption() (GetOption, func()) {
	s := httptest.NewServer(http.DefaultServeMux)
	url, err := url.Parse(s.URL)
	if err != nil {
		panic(err)
	}
	return GetOption{
		Host:     url.Hostname(),
		Port:     url.Port(),
		Protocol: "tcp",
	}, func() { s.Close() }
}
