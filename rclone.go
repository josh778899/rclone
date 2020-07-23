// Sync files and directories to and from local and remote object stores
//
// Nick Craig-Wood <nick@craig-wood.com>
package main

import (
	_ "github.com/rclone/rclone/backend/all" // import all backends
	"github.com/rclone/rclone/cmd"
	_ "github.com/rclone/rclone/cmd/all"    // import all commands
	_ "github.com/rclone/rclone/lib/plugin" // import plugins
	"log"
	"net/http"
	_ "net/http/pprof"
	"runtime"
	"runtime/debug"
	"time"
)

func bToMb(b uint64) uint64 {
	return b / 1024 / 1024
}

func PrintMemUsage() {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	// For info on each, see: https://golang.org/pkg/runtime/#MemStats
	log.Printf("Alloc = %v MiB\tTotalAlloc = %v MiB\tSys = %v MiB\tGoroutines = %v\tNumGC = %v",
		bToMb(m.Alloc),
		bToMb(m.TotalAlloc),
		bToMb(m.Sys),
		runtime.NumGoroutine(),
		m.NumGC)
}

func printMem() {
	log.Printf("Starting pprof server")
	go http.ListenAndServe("0.0.0.0:49315", nil)

	go func() {
		ticker := time.NewTicker(time.Minute * 1)
		for {
			PrintMemUsage()
			<-ticker.C
		}
	}()
	ticker := time.NewTicker(time.Second * 5)
	for {
		//start := time.Now()
		runtime.GC()
		debug.FreeOSMemory()
		//log.Printf("GC & scvg use %s", time.Now().Sub(start))
		<-ticker.C
	}
}

func main() {
	http.DefaultClient.Transport = &http.Transport{
		DisableKeepAlives:  false,
		DisableCompression: true,
	}
	go printMem()
	cmd.Main()
}
