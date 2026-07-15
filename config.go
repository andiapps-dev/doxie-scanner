package main

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
}

const (
	defaultDataDir    = "/data"
	defaultListenAddr = ":8080"
	defaultDriverName = "doxie-dx400"
)

// loadConfig reads configuration from environment variables via getenv
// (normally os.Getenv), falling back to sane defaults for anything unset.
func loadConfig(getenv func(string) string) Config {
	cfg := Config{
		DataDir:    defaultDataDir,
		ListenAddr: defaultListenAddr,
		DriverName: defaultDriverName,
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
	return cfg
}
