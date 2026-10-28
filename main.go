// Area Coach: finds the ideal tablet area for osu! from your replays.
package main

import (
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"

	"areacoach/internal/engine"
	"areacoach/internal/platform"
	"areacoach/internal/server"
	"areacoach/internal/store"
	"areacoach/web"
)

func main() {
	platform.Init()
	if !acquireSingleInstance() {
		return
	}
	dir := store.DefaultDir()
	if d := os.Getenv("AREACOACH_DATA"); d != "" {
		dir = d
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		platform.MessageBox("Area Coach", "I can't create the data folder:\n"+dir, true)
		return
	}
	setupLog(dir)
	st, err := store.Open(dir)
	if err != nil {
		platform.MessageBox("Area Coach", "I can't open the saved data:\n"+err.Error(), true)
		return
	}
	eng := engine.New(st)
	eng.Run()
	srv := server.New(eng, web.FS)
	url, err := srv.Start()
	if err != nil {
		platform.MessageBox("Area Coach", "I can't start the interface:\n"+err.Error(), true)
		return
	}
	log.Printf("Area Coach %s started: %s", server.Version, url)
	showUI(url, dir, st.State().Settings.Theme)
	eng.Shutdown()
	_ = st.Save()
}

func setupLog(dir string) {
	path := filepath.Join(dir, "areacoach.log")
	if st, err := os.Stat(path); err == nil && st.Size() > 2<<20 {
		_ = os.Rename(path, path+".old")
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	log.SetOutput(io.MultiWriter(f, os.Stderr))
	log.SetFlags(log.LstdFlags)
	fmt.Fprintln(f, "----")
}
