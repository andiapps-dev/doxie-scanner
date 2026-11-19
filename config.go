package main

import "strings"

// Config holds every environment-driven setting this application reads.
// Kept as a plain struct populated by loadConfig (rather than reading
// env vars scattered through main.go) so config parsing itself is
// unit-testable without needing to touch real process environment
// variables.
type Config struct {
	// DataDir is the required, persistent-volume-backed directory scan
	// jobs are stored under. main.go refuses to start if this isn't
	// writable.
	DataDir string
	// ListenAddr is the address net/http.ListenAndServe binds to.
	ListenAddr string
	// DriverName selects which registered driver.Driver to use (see
	// internal/driver.Get).
	DriverName string
	// OCRLang is the tesseract language code used for "Extract Text".
	// Anything other than the default "eng" requires the matching
	// tesseract-ocr-data-<lang> (Alpine) / tesseract-ocr-<lang> (Debian)
	// package to also be installed in the image.
	OCRLang string
	// PaperlessURL and PaperlessToken turn on "Send to Paperless"; both
	// must be set.
	PaperlessURL   string
	PaperlessToken string
	// ButtonDuplex makes a scan started from the scanner's own button scan
	// both sides. The button has no way to say so, and the page's duplex
	// switch lives in the browser, so it is a setting of the server.
	ButtonDuplex bool
	// NoButton turns off listening for the scanner's button.
	NoButton bool
}

const (
	defaultDataDir    = "/data"
	defaultListenAddr = ":8080"
	defaultDriverName = "doxie-dx400"
	defaultOCRLang    = "eng"
)

// loadConfig reads configuration from environment variables via getenv
// (normally os.Getenv), falling back to sane defaults for anything unset.
func loadConfig(getenv func(string) string) Config {
	cfg := Config{
		DataDir:    defaultDataDir,
		ListenAddr: defaultListenAddr,
		DriverName: defaultDriverName,
		OCRLang:    defaultOCRLang,
	}
	if v := getenv("DOXIE_DATA_DIR"); v != "" {
		cfg.DataDir = v
	}
	if v := getenv("DOXIE_LISTEN_ADDR"); v != "" {
		cfg.ListenAddr = v
	}
	if v := getenv("DOXIE_DRIVER"); v != "" {
		cfg.DriverName = v
	}
	if v := getenv("DOXIE_OCR_LANG"); v != "" {
		cfg.OCRLang = v
	}
	cfg.PaperlessURL = strings.TrimSpace(getenv("DOXIE_PAPERLESS_URL"))
	cfg.PaperlessToken = strings.TrimSpace(getenv("DOXIE_PAPERLESS_TOKEN"))
	cfg.ButtonDuplex = isTrue(getenv("DOXIE_BUTTON_DUPLEX"))
	cfg.NoButton = isTrue(getenv("DOXIE_DISABLE_BUTTON"))
	return cfg
}

func isTrue(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}
