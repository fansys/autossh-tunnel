package sshtunnel

import (
	"fmt"
	"io"
	"net"
	"sync/atomic"
)

// TunnelMetrics tracks real-time throughput and connection statistics
type TunnelMetrics struct {
	BytesRx           uint64 `json:"bytes_rx"`           // Total received bytes
	BytesTx           uint64 `json:"bytes_tx"`           // Total transmitted bytes
	ActiveConnections int64  `json:"active_connections"` // Currently open TCP connections
	TotalConnections  uint64 `json:"total_connections"`  // Cumulative connections handled
}

func (m *TunnelMetrics) AddRx(n int) {
	if n > 0 {
		atomic.AddUint64(&m.BytesRx, uint64(n))
	}
}

func (m *TunnelMetrics) AddTx(n int) {
	if n > 0 {
		atomic.AddUint64(&m.BytesTx, uint64(n))
	}
}

func (m *TunnelMetrics) IncConn() {
	atomic.AddInt64(&m.ActiveConnections, 1)
	atomic.AddUint64(&m.TotalConnections, 1)
}

func (m *TunnelMetrics) DecConn() {
	atomic.AddInt64(&m.ActiveConnections, -1)
}

func (m *TunnelMetrics) Snapshot() TunnelMetrics {
	return TunnelMetrics{
		BytesRx:           atomic.LoadUint64(&m.BytesRx),
		BytesTx:           atomic.LoadUint64(&m.BytesTx),
		ActiveConnections: atomic.LoadInt64(&m.ActiveConnections),
		TotalConnections:  atomic.LoadUint64(&m.TotalConnections),
	}
}

// FormatBytes formats bytes into human-readable string (KB, MB, GB)
func FormatBytes(b uint64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(b)/float64(div), "KMGTPE"[exp])
}

// CountingConn wraps a net.Conn to count read and written bytes
type CountingConn struct {
	net.Conn
	metrics *TunnelMetrics
}

func NewCountingConn(c net.Conn, m *TunnelMetrics) *CountingConn {
	m.IncConn()
	return &CountingConn{Conn: c, metrics: m}
}

func (c *CountingConn) Read(b []byte) (int, error) {
	n, err := c.Conn.Read(b)
	if n > 0 {
		c.metrics.AddRx(n)
	}
	return n, err
}

func (c *CountingConn) Write(b []byte) (int, error) {
	n, err := c.Conn.Write(b)
	if n > 0 {
		c.metrics.AddTx(n)
	}
	return n, err
}

func (c *CountingConn) Close() error {
	c.metrics.DecConn()
	return c.Conn.Close()
}

// Pipe transfers data between two connections bidirectionally with metrics counting
func Pipe(src, dst net.Conn, metrics *TunnelMetrics) {
	countedSrc := NewCountingConn(src, metrics)
	defer countedSrc.Close()
	defer dst.Close()

	done := make(chan struct{}, 2)

	go func() {
		_, _ = io.Copy(dst, countedSrc)
		done <- struct{}{}
	}()

	go func() {
		_, _ = io.Copy(countedSrc, dst)
		done <- struct{}{}
	}()

	<-done
}
