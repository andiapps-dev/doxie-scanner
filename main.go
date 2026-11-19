// Command doxie-scanner runs a standalone HTTP server for driving a Doxie
// Pro DX400 scanner over USB, with a web UI for scanning, reviewing,
// editing, exporting pages and sending them to Paperless-ngx. See
// README.md for setup (USB passthrough, the required persistent data
// volume) and the full HTTP API reference.
package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"

	"github.com/andiapps-dev/doxie-scanner/internal/api"
	_ "github.com/andiapps-dev/doxie-scanner/internal/doxiedx400" // registers the "doxie-dx400" driver
	"github.com/andiapps-dev/doxie-scanner/internal/driver"
	"github.com/andiapps-dev/doxie-scanner/internal/orient"
	"github.com/andiapps-dev/doxie-scanner/internal/paperless"
	"github.com/andiapps-dev/doxie-scanner/internal/scanjobs"
	"github.com/andiapps-dev/doxie-scanner/internal/storage"
	"github.com/andiapps-dev/doxie-scanner/internal/web"
)

// version is overridden at build time via -ldflags "-X main.version=...".
// The Dockerfile/build.sh set it to the git tag being released; local
// `go build`s leave it as "dev".
var version = "dev"

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
	var opts []api.Option
	// Auto-rotate reads the page's text with tesseract's orientation
	// data; without it the option simply isn't offered.
	if orient.Available() {
		mgr.WithOrienter(orient.Detect)
	} else {
		log.Printf("auto-rotate unavailable: tesseract with the osd language data is not installed")
	}
	if cfg.PaperlessURL != "" && cfg.PaperlessToken != "" {
		opts = append(opts, api.WithPaperless(paperless.New(cfg.PaperlessURL, cfg.PaperlessToken)))
		log.Printf("sending to Paperless at %s", cfg.PaperlessURL)
	}
	srv := api.NewServer(drv, mgr, store, web.FS(), version, cfg.OCRLang, opts...)

	if bw, ok := drv.(driver.ButtonWatcher); ok && !cfg.NoButton {
		go bw.WatchButton(context.Background(), buttonScan(mgr, cfg.ButtonDuplex))
		log.Printf("the scanner's own button starts a scan (duplex=%v)", cfg.ButtonDuplex)
	}

	log.Printf("doxie-scanner %s listening on %s (driver=%s)", version, cfg.ListenAddr, cfg.DriverName)
	log.Fatal(http.ListenAndServe(cfg.ListenAddr, srv))
}

// buttonScan returns what happens when the scanner's button is pressed: the
// same scan the page's Start button would begin, with the page's defaults
// (crop and straighten, turn upright when possible). A press during a scan
// that is already running is ignored.
func buttonScan(mgr *scanjobs.Manager, duplex bool) func() {
	return func() {
		id, err := mgr.StartScanWith(scanjobs.Options{
			Duplex:     duplex,
			AutoRotate: mgr.CanAutoRotate(),
			AutoCrop:   true,
		})
		switch {
		case errors.Is(err, scanjobs.ErrAlreadyRunning):
			log.Printf("scanner button pressed while a scan is running; ignored")
		case err != nil:
			log.Printf("scanner button: could not start a scan: %v", err)
		default:
			log.Printf("scanner button pressed: started scan %s", id)
		}
	}
}
