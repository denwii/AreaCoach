// Area Coach: the tablet area for osu! from how you aim.
package main

import (
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"

	"areacoach/internal/app"
	"areacoach/internal/platform"
	"areacoach/internal/server"
	"areacoach/web"
)

func main() {
	platform.Init()
	if !acquireSingleInstance() {
		return
	}
	dir := dataDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		platform.MessageBox("Area Coach", "I can't create the data folder:\n"+dir, true)
		return
	}
	setupLog(dir)
	a := app.New(dir, server.Version)
	a.Run()
	srv := server.New(a, web.FS, dir)
	url, err := srv.Start()
	if err != nil {
		platform.MessageBox("Area Coach", "I can't start the interface:\n"+err.Error(), true)
		return
	}
	log.Printf("Area Coach %s started", server.Version)
	showUI(url, dir, "")
	a.Close()
}

func dataDir() string {
	if d := os.Getenv("AREACOACH_DATA"); d != "" {
		return d
	}
	if ad := os.Getenv("APPDATA"); ad != "" {
		return filepath.Join(ad, "AreaCoach")
	}
	if h, err := os.UserHomeDir(); err == nil {
		return filepath.Join(h, ".config", "AreaCoach")
	}
	return "AreaCoach-data"
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
