// Command doxie-scanner runs a standalone HTTP server for driving a Doxie
// Pro DX400 scanner over raw SCSI-over-USB-bulk, with a web UI for
// scanning, reviewing, editing, and exporting pages. See README.md for
// setup (USB passthrough, the required persistent data volume) and the
// full HTTP API reference.
package main

import (
	"log"
	"net/http"
	"os"

	"github.com/andiapps-dev/doxie-scanner/internal/api"
	_ "github.com/andiapps-dev/doxie-scanner/internal/doxiedx400" // registers the "doxie-dx400" driver
	"github.com/andiapps-dev/doxie-scanner/internal/driver"
	"github.com/andiapps-dev/doxie-scanner/internal/scanjobs"
	"github.com/andiapps-dev/doxie-scanner/internal/storage"
	"github.com/andiapps-dev/doxie-scanner/internal/web"
)

func main() {
	cfg := loadConfig(os.Getenv)

	store := storage.New(cfg.DataDir)
	if err := store.CheckWritable(); err != nil {
		log.Fatalf(
			"data directory %q is not usable: %v\n\n"+
				"DOXIE_DATA_DIR must point at a writable, persistent volume — "+
				"mount one with `-v <host-path>:%s`, or scanned pages will be "+
				"lost the moment this container is recreated.",
			cfg.DataDir, err, cfg.DataDir,
		)
	}
	log.Printf(
		"using data directory %q — make sure this is bind-mounted to persistent "+
			"host storage; an anonymous/ephemeral volume here means scans are lost "+
			"if this container is ever recreated",
		cfg.DataDir,
	)

	drv, err := driver.Get(cfg.DriverName)
	if err != nil {
		log.Fatalf("unknown driver %q: %v", cfg.DriverName, err)
	}

	mgr := scanjobs.NewManager(drv, store)
	srv := api.NewServer(drv, mgr, store, web.FS())

	log.Printf("doxie-scanner listening on %s (driver=%s)", cfg.ListenAddr, cfg.DriverName)
	log.Fatal(http.ListenAndServe(cfg.ListenAddr, srv))
}
