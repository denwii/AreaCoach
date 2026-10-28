// Command fakeotd imitates the OpenTabletDriver daemon RPC on Linux, to try
// the live calibration without a tablet: it keeps the settings in memory and
// logs every area it receives.
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"areacoach/internal/otd"
)

func main() {
	path := flag.String("settings", "", "settings.json to start from")
	connected := flag.String("connected", "", "tablets reported as connected (comma separated; \"none\": no answer to GetTablets)")
	flag.Parse()
	b, err := os.ReadFile(*path)
	if err != nil {
		log.Fatal(err)
	}
	settings, err := otd.DecodeSettings(b)
	if err != nil {
		log.Fatal(err)
	}
	var mu sync.Mutex
	dir := os.Getenv("TMPDIR")
	if dir == "" {
		dir = "/tmp"
	}
	sock := filepath.Join(dir, "CoreFxPipe_OpenTabletDriver.Daemon")
	_ = os.Remove(sock)
	ln, err := net.Listen("unix", sock)
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("listening on %s", sock)
	for {
		c, err := ln.Accept()
		if err != nil {
			log.Fatal(err)
		}
		go func(c net.Conn) {
			defer c.Close()
			r := bufio.NewReader(c)
			for {
				n := -1
				for {
					line, err := r.ReadString('\n')
					if err != nil {
						return
					}
					line = strings.TrimSpace(line)
					if line == "" && n >= 0 {
						break
					}
					if k, v, ok := strings.Cut(line, ":"); ok && strings.EqualFold(k, "Content-Length") {
						n, _ = strconv.Atoi(strings.TrimSpace(v))
					}
				}
				buf := make([]byte, n)
				if _, err := io.ReadFull(r, buf); err != nil {
					return
				}
				var req struct {
					ID     json.RawMessage   `json:"id"`
					Method string            `json:"method"`
					Params []json.RawMessage `json:"params"`
				}
				_ = json.Unmarshal(buf, &req)
				resp := map[string]any{"jsonrpc": "2.0", "id": req.ID}
				mu.Lock()
				switch req.Method {
				case "GetSettings":
					resp["result"] = settings
				case "SetSettings":
					if s, err := otd.DecodeSettings(req.Params[0]); err == nil {
						settings = s
						for _, p := range s["Profiles"].([]any) {
							pm := p.(map[string]any)
							if a, ok := otd.AreaOf(map[string]any{"Profiles": []any{pm}}, ""); ok {
								log.Printf("area set: %v %.2f × %.2f, X %.2f, Y %.2f", pm["Tablet"], a.W, a.H, a.X, a.Y)
							}
						}
					}
					resp["result"] = nil
				case "ForceResynchronize":
					resp["result"] = nil
				case "GetTablets":
					if *connected == "none" {
						resp["error"] = map[string]any{"code": -32601, "message": "method not found: GetTablets"}
						break
					}
					var list []any
					for _, n := range strings.Split(*connected, ",") {
						if n = strings.TrimSpace(n); n != "" {
							list = append(list, map[string]any{"Properties": map[string]any{"Name": n}})
						}
					}
					resp["result"] = list
				default:
					resp["error"] = map[string]any{"code": -32601, "message": "method not found: " + req.Method}
				}
				mu.Unlock()
				out, _ := json.Marshal(resp)
				fmt.Fprintf(c, "Content-Length: %d\r\n\r\n", len(out))
				_, _ = c.Write(out)
			}
		}(c)
	}
}
