package sshtunnel

import (
	"testing"
)

func TestMetricsTracking(t *testing.T) {
	m := &TunnelMetrics{}

	m.AddRx(1024)
	m.AddTx(2048)
	m.IncConn()
	m.IncConn()

	snap := m.Snapshot()
	if snap.BytesRx != 1024 {
		t.Errorf("Expected BytesRx 1024, got %d", snap.BytesRx)
	}
	if snap.BytesTx != 2048 {
		t.Errorf("Expected BytesTx 2048, got %d", snap.BytesTx)
	}
	if snap.ActiveConnections != 2 {
		t.Errorf("Expected ActiveConnections 2, got %d", snap.ActiveConnections)
	}
	if snap.TotalConnections != 2 {
		t.Errorf("Expected TotalConnections 2, got %d", snap.TotalConnections)
	}

	m.DecConn()
	snap2 := m.Snapshot()
	if snap2.ActiveConnections != 1 {
		t.Errorf("Expected ActiveConnections 1, got %d", snap2.ActiveConnections)
	}
	if snap2.TotalConnections != 2 {
		t.Errorf("Expected TotalConnections to remain 2, got %d", snap2.TotalConnections)
	}
}

func TestFormatBytes(t *testing.T) {
	if FormatBytes(500) != "500 B" {
		t.Errorf("FormatBytes(500) = %s, want '500 B'", FormatBytes(500))
	}
	if FormatBytes(1024) != "1.0 KB" {
		t.Errorf("FormatBytes(1024) = %s, want '1.0 KB'", FormatBytes(1024))
	}
	if FormatBytes(1048576) != "1.0 MB" {
		t.Errorf("FormatBytes(1048576) = %s, want '1.0 MB'", FormatBytes(1048576))
	}
}
