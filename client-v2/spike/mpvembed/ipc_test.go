package mpvembed

import (
	"encoding/json"
	"net"
	"testing"
	"time"
)

// fakeMPV 用 net.Pipe 模拟 mpv IPC 服务端：先吐一行异步事件（噪声，须被跳过），
// 再对每个带 requestID 的请求回 {"error":"success","requestID":N}。
// net.Pipe 同步无缓冲 → 唯一 writer 走 buffered chan，避免双端互等死锁。
func fakeMPV(t *testing.T, conn net.Conn) func() {
	out := make(chan any, 4)
	out <- map[string]any{"event": "start-almost"}
	go func() {
		enc := json.NewEncoder(conn)
		for m := range out {
			if err := enc.Encode(m); err != nil {
				return
			}
		}
	}()
	done := make(chan struct{})
	go func() {
		defer close(done)
		dec := json.NewDecoder(conn)
		for {
			var req struct {
				Command   []any `json:"command"`
				RequestID int   `json:"requestID"`
			}
			if err := dec.Decode(&req); err != nil {
				close(out)
				return
			}
			out <- map[string]any{"error": "success", "requestID": req.RequestID}
		}
	}()
	return func() { <-done }
}

func TestSendCommandSuccess(t *testing.T) {
	a, b := net.Pipe()
	wait := fakeMPV(t, b)
	defer wait()
	ipc := NewIPC(a)
	defer ipc.Close()

	ipc.conn.(net.Conn).SetReadDeadline(time.Now().Add(2 * time.Second))
	resp, err := ipc.SendCommand("loadfile", "https://example.com/v.mp4")
	if err != nil {
		t.Fatalf("SendCommand: %v", err)
	}
	if resp.Error != "success" {
		t.Fatalf("error=%q, want success", resp.Error)
	}

	resp, err = ipc.SendCommand("get_property", "pause")
	if err != nil {
		t.Fatalf("second SendCommand: %v", err)
	}
	if resp.RequestID != 2 {
		t.Fatalf("requestID=%d, want 2（服务端应回显）", resp.RequestID)
	}
}

func TestSendCommandError(t *testing.T) {
	a, b := net.Pipe()
	go func() {
		defer b.Close()
		dec := json.NewDecoder(b)
		enc := json.NewEncoder(b)
		var req struct {
			RequestID int `json:"requestID"`
		}
		_ = dec.Decode(&req)
		_ = enc.Encode(map[string]any{"error": "internal error", "requestID": req.RequestID})
	}()
	ipc := NewIPC(a)
	defer ipc.Close()
	ipc.conn.(net.Conn).SetReadDeadline(time.Now().Add(2 * time.Second))

	_, err := ipc.SendCommand("set_property", "bad-prop", 1)
	if err == nil {
		t.Fatal("want error from mpv, got nil")
	}
	if got := err.Error(); got != "mpv error: internal error" {
		t.Fatalf("err=%q", got)
	}
}
