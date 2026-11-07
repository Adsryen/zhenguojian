package main

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"sync"

	"duanjuapp/native/core"
)

var (
	writeMu sync.Mutex
	out     = bufio.NewWriterSize(os.Stdout, 1<<16)
)

func jsonString(value string) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}

// reply writes one JSON line, merging the caller's correlation id into the
// envelope so the UI can match concurrent answers back to their requests.
func reply(id string, body string) {
	if id != "" {
		var payload map[string]json.RawMessage
		if json.Unmarshal([]byte(body), &payload) == nil {
			payload["id"] = json.RawMessage(jsonString(id))
			if merged, err := json.Marshal(payload); err == nil {
				body = string(merged)
			}
		}
	}
	writeMu.Lock()
	defer writeMu.Unlock()
	out.WriteString(body)
	out.WriteByte('\n')
	out.Flush()
}

func main() {
	directory := ""
	if len(os.Args) > 1 {
		directory = os.Args[1]
	}
	slots := make(chan struct{}, 6)
	group := sync.WaitGroup{}
	reader := bufio.NewReaderSize(os.Stdin, 1<<20)
	for {
		line, err := reader.ReadBytes('\n')
		if len(line) > 0 {
			var payload map[string]any
			if json.Unmarshal(line, &payload) == nil {
				id := ""
				if value, ok := payload["id"].(string); ok {
					id = value
					delete(payload, "id")
				}
				if directory != "" && payload["action"] == "initialize" {
					payload["directory"] = directory
				}
				encoded, marshalErr := json.Marshal(payload)
				if marshalErr != nil {
					reply(id, `{"ok":false,"error":"请求格式无效"}`)
				} else {
					group.Add(1)
					go func(id string, encoded []byte) {
						defer group.Done()
						slots <- struct{}{}
						defer func() { <-slots }()
						var request struct {
							Action string `json:"action"`
						}
						_ = json.Unmarshal(encoded, &request)
						if request.Action == "posterMetadata" {
							reply(id, core.NativePosterMetadata(string(encoded)))
						} else if request.Action == "dramaCreators" || request.Action == "creatorProfile" || request.Action == "creatorWorks" || request.Action == "browseFilters" || request.Action == "browseFiltered" {
							reply(id, core.NativeDiscovery(string(encoded)))
						} else {
							reply(id, core.NativeRequest(string(encoded)))
						}
					}(id, encoded)
				}
			}
		}
		if err != nil {
			if err != io.EOF {
				os.Stderr.WriteString("stdin: " + err.Error() + "\n")
			}
			break
		}
	}
	group.Wait()
}
