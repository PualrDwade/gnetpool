package gnetpool

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestPoolGet(t *testing.T) {
	pool := NewPool()
	opt := GetOption{
		Host:     "localhost",
		Port:     "3306",
		Protocol: "tcp",
		Timeout:  time.Second,
	}
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
}

func BenchmarkPoolGet(b *testing.B) {
	pool := NewPool()
	opt := GetOption{
		Host:     "localhost",
		Port:     "3306",
		Protocol: "tcp",
		Timeout:  time.Second,
	}
	for i := 0; i < b.N; i++ {
		conn, err := pool.Get(opt)
		assert.Nil(b, err)
		_ = conn.Close()
	}
}
