package faultlab

import (
	"errors"
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"time"
)

// ErrProcessTermination tells the HTTP host that the injector has armed a
// deliberate process-level crash.  Panicking in an HTTP handler only ends that
// handler goroutine because net/http recovers it; the host must exit instead.
var ErrProcessTermination = errors.New("fault-lab process termination requested")

// OOMInjector 分配内存直到容器被 cgroup OOM 杀死。
// Recover 释放内存（若进程尚未被杀）。
type OOMInjector struct {
	mu        sync.Mutex
	active    bool
	allocated atomic.Pointer[[]byte]
}

// Type 返回故障类型。
func (o *OOMInjector) Type() string { return "oom" }

// Inject 分配内存（逐步递增分配，使 Prometheus 时序能够观测到向 256Mi Limit 爬升的斜率，最终触发 cgroup OOM）。
func (o *OOMInjector) Inject(_ time.Duration) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.active {
		return nil
	}
	o.active = true
	go func() {
		var chunks [][]byte
		// 每 1.2 秒分配 28MiB，并在内存页中写入数据激活工作集（RSS/working_set），直至超过 256Mi 触发 cgroup OOMKilled
		for i := 0; i < 12; i++ {
			chunk := make([]byte, 28<<20)
			for j := 0; j < len(chunk); j += 4096 {
				chunk[j] = 1
			}
			chunks = append(chunks, chunk)
			time.Sleep(1200 * time.Millisecond)
		}
	}()
	return nil
}

// Recover 释放内存。
func (o *OOMInjector) Recover() error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if !o.active {
		return nil
	}
	o.allocated.Store(nil)
	o.active = false
	runtime.GC()
	return nil
}

// Status 返回状态。
func (o *OOMInjector) Status() string {
	if o.active {
		return "injected (内存占用中)"
	}
	return "ok"
}

// CrashLoopInjector 使进程立即退出（exit 1），由 K8s 重启。
type CrashLoopInjector struct {
	mu     sync.Mutex
	active bool
}

// Type 返回故障类型。
func (c *CrashLoopInjector) Type() string { return "crashloop" }

// Inject 标记一个必须由 HTTP host 执行的进程退出。
func (c *CrashLoopInjector) Inject(_ time.Duration) error {
	c.mu.Lock()
	c.active = true
	c.mu.Unlock()
	return ErrProcessTermination
}

// Recover 进程已退出后无法恢复（幂等返回 nil 表示无需操作）。
func (c *CrashLoopInjector) Recover() error { return nil }

// Status 返回状态。
func (c *CrashLoopInjector) Status() string {
	if c.active {
		return "injected (进程已退出)"
	}
	return "ok"
}

// ConfigInjector 模拟坏配置：/checkout 返回 500。
type ConfigInjector struct {
	mu     sync.Mutex
	active bool
}

// Type 返回故障类型。
func (c *ConfigInjector) Type() string { return "config" }

// Inject 启用坏配置。
func (c *ConfigInjector) Inject(d time.Duration) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.active = true
	return nil
}

// Recover 恢复。
func (c *ConfigInjector) Recover() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.active = false
	return nil
}

// Active 返回是否注入中。
func (c *ConfigInjector) Active() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.active
}

// Status 返回状态。
func (c *ConfigInjector) Status() string {
	if c.active {
		return "injected (checkout 返回 500)"
	}
	return "ok"
}

// CPUInjector 忙循环占满 CPU。
type CPUInjector struct {
	mu     sync.Mutex
	active bool
	stop   chan struct{}
}

// Type 返回故障类型。
func (c *CPUInjector) Type() string { return "cpu" }

// Inject 启动忙循环 goroutine。
func (c *CPUInjector) Inject(_ time.Duration) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.active {
		return nil
	}
	c.stop = make(chan struct{})
	c.active = true
	go func() {
		for {
			select {
			case <-c.stop:
				return
			default:
				// 忙循环（gosec 无操作警告可忽略）。
				_ = 1 + 1
			}
		}
	}()
	return nil
}

// Recover 停止忙循环。
func (c *CPUInjector) Recover() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.active {
		return nil
	}
	close(c.stop)
	c.active = false
	return nil
}

// Status 返回状态。
func (c *CPUInjector) Status() string {
	if c.active {
		return "injected (CPU 占满)"
	}
	return "ok"
}

// DependencyInjector 模拟下游依赖超时：/checkout 阻塞 duration。
type DependencyInjector struct {
	mu       sync.Mutex
	latency  time.Duration
	lastResp atomic.Int64
}

// Type 返回故障类型。
func (d *DependencyInjector) Type() string { return "dependency" }

// Inject 设置下游延迟。
func (d *DependencyInjector) Inject(duration time.Duration) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.latency = duration
	return nil
}

// Recover 清除延迟。
func (d *DependencyInjector) Recover() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.latency = 0
	return nil
}

// Latency 返回当前下游延迟。
func (d *DependencyInjector) Latency() time.Duration {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.latency
}

// Status 返回状态。
func (d *DependencyInjector) Status() string {
	if d.latency > 0 {
		return fmt.Sprintf("injected (下游延迟 %s)", d.latency)
	}
	return "ok"
}
