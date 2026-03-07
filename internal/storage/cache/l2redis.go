package cache

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"time"
)

type RedisCache struct {
	addr     string
	password string
	db       int
}

func NewRedisCache(addr, password string, db int) *RedisCache {
	return &RedisCache{addr: addr, password: password, db: db}
}

func (r *RedisCache) Get(ctx context.Context, key string) ([]byte, bool, error) {
	conn, rw, err := r.connect(ctx)
	if err != nil {
		return nil, false, err
	}
	defer conn.Close()

	if err := writeRESPArray(rw, "GET", key); err != nil {
		return nil, false, err
	}
	kind, payload, err := readRESP(rw)
	if err != nil {
		return nil, false, err
	}
	if kind == '_' {
		return nil, false, nil
	}
	if kind != '$' {
		return nil, false, errors.New("unexpected redis response")
	}
	return payload, true, nil
}

func (r *RedisCache) Set(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	conn, rw, err := r.connect(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	ttlSeconds := int(ttl.Seconds())
	if ttlSeconds <= 0 {
		ttlSeconds = 1
	}
	if err := writeRESPArray(rw, "SETEX", key, strconv.Itoa(ttlSeconds), string(value)); err != nil {
		return err
	}
	kind, _, err := readRESP(rw)
	if err != nil {
		return err
	}
	if kind != '+' {
		return errors.New("redis set failed")
	}
	return nil
}

func (r *RedisCache) Ping(ctx context.Context) error {
	conn, rw, err := r.connect(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	if err := writeRESPArray(rw, "PING"); err != nil {
		return err
	}
	kind, _, err := readRESP(rw)
	if err != nil {
		return err
	}
	if kind != '+' {
		return errors.New("redis ping failed")
	}
	return nil
}

func (r *RedisCache) connect(ctx context.Context) (net.Conn, *bufio.ReadWriter, error) {
	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", r.addr)
	if err != nil {
		return nil, nil, err
	}
	rw := bufio.NewReadWriter(bufio.NewReader(conn), bufio.NewWriter(conn))
	if r.password != "" {
		if err := writeRESPArray(rw, "AUTH", r.password); err != nil {
			conn.Close()
			return nil, nil, err
		}
		if kind, _, err := readRESP(rw); err != nil || kind != '+' {
			conn.Close()
			if err != nil {
				return nil, nil, err
			}
			return nil, nil, errors.New("redis auth failed")
		}
	}
	if r.db > 0 {
		if err := writeRESPArray(rw, "SELECT", strconv.Itoa(r.db)); err != nil {
			conn.Close()
			return nil, nil, err
		}
		if kind, _, err := readRESP(rw); err != nil || kind != '+' {
			conn.Close()
			if err != nil {
				return nil, nil, err
			}
			return nil, nil, errors.New("redis select failed")
		}
	}
	return conn, rw, nil
}

func writeRESPArray(rw *bufio.ReadWriter, args ...string) error {
	var b bytes.Buffer
	b.WriteString(fmt.Sprintf("*%d\r\n", len(args)))
	for _, a := range args {
		b.WriteString(fmt.Sprintf("$%d\r\n%s\r\n", len(a), a))
	}
	if _, err := rw.WriteString(b.String()); err != nil {
		return err
	}
	return rw.Flush()
}

func readRESP(rw *bufio.ReadWriter) (byte, []byte, error) {
	line, err := rw.ReadString('\n')
	if err != nil {
		return 0, nil, err
	}
	if len(line) < 3 {
		return 0, nil, errors.New("short redis response")
	}
	kind := line[0]
	content := strings.TrimSuffix(strings.TrimSuffix(line[1:], "\n"), "\r")
	switch kind {
	case '+', ':':
		return kind, []byte(content), nil
	case '-':
		return kind, nil, errors.New(content)
	case '$':
		n, err := strconv.Atoi(content)
		if err != nil {
			return 0, nil, err
		}
		if n == -1 {
			return '_', nil, nil
		}
		buf := make([]byte, n+2)
		if _, err := io.ReadFull(rw, buf); err != nil {
			return 0, nil, err
		}
		return kind, buf[:n], nil
	default:
		return 0, nil, errors.New("unsupported redis response")
	}
}

