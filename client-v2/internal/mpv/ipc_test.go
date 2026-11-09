package mpv

import (
	"encoding/json"
	"net"
	"strings"
	"testing"
	"time"
)

// TestIPCPairingSkipsNoise requestID 配对：事件行/async pending/他人 id/
// 非 JSON 噪声全部跳过，直到取到本 id 的最终回复。
func TestIPCPairingSkipsNoise(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	type in struct {
		Command   []any `json:"command"`
		RequestID int   `json:"requestID"`
	}

	go func() {
		var line []byte
		dec := json.NewDecoder(server)
		for {
			var req in
			if err := dec.Decode(&req); err != nil {
				return
			}
			id := req.RequestID
			// 噪声：事件行、async pending、错 id、非 JSON
			server.Write([]byte(`{"event":"log","format":"text","data":{"text":"[vo] hi"}}` + "\n"))
			server.Write([]byte(`{"error":"async","async":true,"requestID":` + itoa(id) + `}` + "\n"))
			server.Write([]byte(`{"error":"success","requestID":` + itoa(id+100) + `}` + "\n"))
			server.Write([]byte("!!not-json!!\n"))
			server.Write([]byte(`{"error":"success","requestID":` + itoa(id) + `,"data":12.5}` + "\n"))
			_ = line
		}
	}()

	ipc := NewIPC(client)
	done := make(chan struct{})
	go func() {
		defer close(done)
		resp, err := ipc.SendCommand([]any{"get_property", "time-pos"})
		if err != nil {
			t.Errorf("SendCommand: %v", err)
			return
		}
		var v float64
		if err := json.Unmarshal(resp.Data, &v); err != nil || v != 12.5 {
			t.Errorf("data = %s, want 12.5", resp.Data)
		}
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("SendCommand 未在噪声中配对到最终回复")
	}
}

// TestIPCErrorReply mpv 报 error != success 时透传错误。
func TestIPCErrorReply(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	go func() {
		dec := json.NewDecoder(server)
		var req struct {
			RequestID int `json:"requestID"`
		}
		if err := dec.Decode(&req); err != nil {
			return
		}
		server.Write([]byte(`{"error":"property list is empty","requestID":` + itoa(req.RequestID) + `}` + "\n"))
	}()

	ipc := NewIPC(client)
	_, err := ipc.SendCommand([]any{"get_property", "nope"})
	if err == nil || !strings.Contains(err.Error(), "property list is empty") {
		t.Fatalf("want mpv error passthrough, got %v", err)
	}
}

func itoa(v int) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// TestParseEventLine 事件行/回复行判别。
func TestParseEventLine(t *testing.T) {
	ev, ok := parseEventLine([]byte(`{"event":"property-change","id":2,"name":"duration","data":930}`))
	if !ok || ev.Name != "duration" {
		t.Fatalf("property-change 未解析: %v %v", ev, ok)
	}
	if _, ok := parseEventLine([]byte(`{"error":"success","requestID":3}`)); ok {
		t.Fatal("回复行不该当事件")
	}
	if _, ok := parseEventLine([]byte(`random noise`)); ok {
		t.Fatal("噪声不该当事件")
	}
}

// TestLavfOptions v1 protocol_whitelist 两分支 + decryption_key 追加。
func TestLavfOptions(t *testing.T) {
	online := strings.Join(lavfOptions(false, ""), ",")
	if !strings.Contains(online, "protocol_whitelist=[http,https,tcp,tls,crypto,data,file]") {
		t.Fatalf("online whitelist: %s", online)
	}
	local := strings.Join(lavfOptions(true, "abc123"), ",")
	if !strings.Contains(local, "protocol_whitelist=[file,crypto,data]") ||
		!strings.Contains(local, "decryption_key=abc123") {
		t.Fatalf("local+key: %s", local)
	}
}
