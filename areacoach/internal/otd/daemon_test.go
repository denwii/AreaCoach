package otd

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"testing"
)

// a fake daemon speaking StreamJsonRpc's header-delimited JSON-RPC
func fakeDaemon(t *testing.T, c net.Conn, settings map[string]any) {
	r := bufio.NewReader(c)
	send := func(v any) {
		b, _ := json.Marshal(v)
		fmt.Fprintf(c, "Content-Length: %d\r\nContent-Type: application/vscode-jsonrpc; charset=utf-8\r\n\r\n", len(b))
		c.Write(b)
	}
	for {
		n := -1
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				return
			}
			line = strings.TrimSpace(line)
			if line == "" {
				break
			}
			if k, v, ok := strings.Cut(line, ":"); ok && k == "Content-Length" {
				n, _ = strconv.Atoi(strings.TrimSpace(v))
			}
		}
		buf := make([]byte, n)
		io.ReadFull(r, buf)
		var m map[string]any
		json.Unmarshal(buf, &m)
		// a notification first, as the real daemon sends log messages
		send(map[string]any{"jsonrpc": "2.0", "method": "Message", "params": []any{map[string]any{"Message": "log"}}})
		switch m["method"] {
		case "GetSettings":
			send(map[string]any{"jsonrpc": "2.0", "id": m["id"], "result": settings})
		case "SetSettings":
			settings = m["params"].([]any)[0].(map[string]any)
			send(map[string]any{"jsonrpc": "2.0", "id": m["id"], "result": nil})
		default:
			send(map[string]any{"jsonrpc": "2.0", "id": m["id"], "error": map[string]any{"code": -32601, "message": "no"}})
		}
	}
}

func TestDaemonRoundTrip(t *testing.T) {
	a, b := net.Pipe()
	initial := map[string]any{"Profiles": []any{map[string]any{"Tablet": "Wacom CTL-472",
		"AbsoluteModeSettings": map[string]any{"Tablet": map[string]any{"Width": 80.0, "Height": 45.0, "X": 76.0, "Y": 47.5, "Rotation": 0.0}}}}}
	go fakeDaemon(t, b, initial)
	d := NewDaemon(a)
	s, err := d.GetSettings()
	if err != nil {
		t.Fatal(err)
	}
	r, ok := AreaOf(s, "Wacom CTL-472")
	if !ok || r.W != 80 {
		t.Fatalf("area %+v", r)
	}
	s2, err := WithArea(s, "Wacom CTL-472", Rect{W: 90, H: 50.63, X: 70, Y: 45})
	if err != nil {
		t.Fatal(err)
	}
	if err := d.SetSettings(s2); err != nil {
		t.Fatal(err)
	}
	s3, _ := d.GetSettings()
	if r, _ := AreaOf(s3, "Wacom CTL-472"); r.W != 90 || r.X != 70 {
		t.Fatalf("area not applied: %+v", r)
	}
	if r, _ := AreaOf(s, "Wacom CTL-472"); r.W != 80 {
		t.Fatalf("WithArea changed the original")
	}
	if err := d.Resync(); err == nil {
		t.Fatalf("expected an error from an unknown method")
	}
}

func TestWithAreaKeepsNumbers(t *testing.T) {
	raw := []byte("\xef\xbb\xbf" + `{"Profiles":[{"Tablet":"Wacom CTL-472","AbsoluteModeSettings":{"Tablet":{"Width":80,"Height":45,"X":76,"Y":47.5,"Rotation":0}},"Filters":[{"Path":"X","Settings":[{"Property":"Seed","Value":9007199254740993},{"Property":"Coef","Value":0.1}]}]}],"Revision":"0.6.4.0"}`)
	s, err := DecodeSettings(raw)
	if err != nil {
		t.Fatal(err)
	}
	ns, err := WithArea(s, "Wacom CTL-472", Rect{W: 90.5, H: 50.91, X: 70, Y: 40})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(ns)
	out := string(b)
	for _, want := range []string{`"Value":9007199254740993`, `"Value":0.1`, `"Width":90.5`, `"Revision":"0.6.4.0"`} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %s in %s", want, out)
		}
	}
	if r, ok := AreaOf(ns, "Wacom CTL-472"); !ok || r.W != 90.5 || r.Y != 40 {
		t.Errorf("area %+v", r)
	}
}
