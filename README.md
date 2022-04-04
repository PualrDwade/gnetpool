# gnetpool

a simple connection pool write by golang

it is easy to use, and only support the basic scene for connection pool, such as idle timeout checking and max lifecircle checking.

if you want to extend more function, just fork it !

this is a simple benchmark data:

```
goos: linux
goarch: amd64
cpu: Intel(R) Xeon(R) Gold 6133 CPU @ 2.50GHz 8 core
mem: 16G
BenchmarkPoolGet-8   	 4218025	       285.1 ns/op	      48 B/op	       1 allocs/op
```