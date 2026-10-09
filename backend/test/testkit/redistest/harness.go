// Package redistest 管理测试运行拥有的 Redis 键及故障代理，不清理共享库。
package redistest

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"net"
	"os"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

type Harness struct {
	Client    *redis.Client
	Namespace string
	Address   string
	mu        sync.Mutex
	owned     map[string]struct{}
}

func New(t *testing.T) *Harness {
	t.Helper()
	address := os.Getenv("VELIS_TEST_REDIS_ADDRESS")
	if address == "" {
		t.Skip("未设置 VELIS_TEST_REDIS_ADDRESS，未执行真实 Redis 验收")
	}
	var token [16]byte
	if _, err := rand.Read(token[:]); err != nil {
		t.Fatal(err)
	}
	h := &Harness{Address: address, Namespace: "velis:test:" + hex.EncodeToString(token[:]), owned: map[string]struct{}{}}
	h.Client = redis.NewClient(&redis.Options{Addr: address, Username: os.Getenv("VELIS_TEST_REDIS_USERNAME"), Password: os.Getenv("VELIS_TEST_REDIS_PASSWORD"), MaxRetries: -1, ContextTimeoutEnabled: true, DialTimeout: time.Second, ReadTimeout: time.Second, WriteTimeout: time.Second})
	t.Cleanup(func() {
		if err := h.Clear(); err != nil {
			t.Errorf("清理本次 Redis 键失败: %v", err)
		}
		_ = h.Client.Close()
	})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := h.Client.Ping(ctx).Err(); err != nil {
		t.Fatalf("真实 Redis 连接失败: %v", err)
	}
	return h
}

func (h *Harness) Key(suffix string) string {
	key := h.Namespace + ":" + suffix
	if err := h.Track(key); err != nil {
		panic(err)
	}
	return key
}

// Track 只接受本次随机命名空间中的精确键；不进行 SCAN 或通配符删除。
func (h *Harness) Track(key string) error {
	if !strings.HasPrefix(key, h.Namespace+":") {
		return errors.New("禁止登记其他命名空间的 Redis 键")
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.owned[key] = struct{}{}
	return nil
}

// Keys 返回本次登记的精确键快照，用于定向损坏；不会向共享 Redis 扫描。
func (h *Harness) Keys() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	keys := make([]string, 0, len(h.owned))
	for key := range h.owned {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
func (h *Harness) Clear() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	keys := make([]string, 0, 100)
	flush := func() error {
		if len(keys) == 0 {
			return nil
		}
		err := h.Client.Del(ctx, keys...).Err()
		keys = keys[:0]
		return err
	}
	for key := range h.owned {
		keys = append(keys, key)
		if len(keys) == 100 {
			if err := flush(); err != nil {
				return err
			}
		}
	}
	if err := flush(); err != nil {
		return err
	}
	clear(h.owned)
	return nil
}

// Proxy 仅关闭本进程拥有的连接；Delay 延迟上游响应，Disconnect 模拟连接中断。
type Proxy struct {
	listener     net.Listener
	upstream     string
	delay        atomic.Int64
	disconnected atomic.Bool
	mu           sync.Mutex
	connections  map[net.Conn]struct{}
	closed       bool
	wg           sync.WaitGroup
}

func NewProxy(t *testing.T, upstream string) *Proxy {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	p := &Proxy{listener: listener, upstream: upstream, connections: map[net.Conn]struct{}{}}
	p.wg.Add(1)
	go p.accept()
	t.Cleanup(p.Close)
	return p
}
func (p *Proxy) Address() string       { return p.listener.Addr().String() }
func (p *Proxy) Delay(d time.Duration) { p.delay.Store(int64(d)) }
func (p *Proxy) Disconnect(on bool) {
	p.disconnected.Store(on)
	if on {
		p.mu.Lock()
		for c := range p.connections {
			_ = c.Close()
		}
		p.mu.Unlock()
	}
}
func (p *Proxy) accept() {
	defer p.wg.Done()
	for {
		down, err := p.listener.Accept()
		if err != nil {
			return
		}
		p.mu.Lock()
		if p.closed || p.disconnected.Load() {
			_ = down.Close()
			p.mu.Unlock()
			continue
		}
		p.connections[down] = struct{}{}
		p.wg.Add(1)
		p.mu.Unlock()
		go p.forward(down)
	}
}
func (p *Proxy) forward(down net.Conn) {
	defer p.wg.Done()
	defer func() { _ = down.Close(); p.mu.Lock(); delete(p.connections, down); p.mu.Unlock() }()
	up, err := net.DialTimeout("tcp", p.upstream, time.Second)
	if err != nil {
		return
	}
	p.mu.Lock()
	if p.closed || p.disconnected.Load() {
		p.mu.Unlock()
		_ = up.Close()
		return
	}
	p.connections[up] = struct{}{}
	p.mu.Unlock()
	defer func() { _ = up.Close(); p.mu.Lock(); delete(p.connections, up); p.mu.Unlock() }()
	done := make(chan struct{})
	go func() { _, _ = io.Copy(up, down); _ = up.Close(); close(done) }()
	buffer := make([]byte, 32*1024)
	for {
		n, e := up.Read(buffer)
		if n > 0 {
			d := time.Duration(p.delay.Load())
			if d > 0 {
				timer := time.NewTimer(d)
				select {
				case <-timer.C:
				case <-done:
					timer.Stop()
					return
				}
			}
			if _, err := down.Write(buffer[:n]); err != nil {
				break
			}
		}
		if e != nil {
			break
		}
	}
	_ = down.Close()
	_ = up.Close()
	<-done
}
func (p *Proxy) Close() {
	p.mu.Lock()
	p.closed = true
	_ = p.listener.Close()
	for c := range p.connections {
		_ = c.Close()
	}
	p.mu.Unlock()
	p.wg.Wait()
}
