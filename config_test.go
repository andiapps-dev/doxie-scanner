package main

import "testing"

func fakeGetenv(values map[string]string) func(string) string {
	return func(key string) string { return values[key] }
}

func TestLoadConfig_Defaults(t *testing.T) {
	cfg := loadConfig(fakeGetenv(nil))
	if cfg.DataDir != defaultDataDir {
		t.Errorf("DataDir = %q, want %q", cfg.DataDir, defaultDataDir)
	}
	if cfg.ListenAddr != defaultListenAddr {
		t.Errorf("ListenAddr = %q, want %q", cfg.ListenAddr, defaultListenAddr)
	}
	if cfg.DriverName != defaultDriverName {
		t.Errorf("DriverName = %q, want %q", cfg.DriverName, defaultDriverName)
	}
	if cfg.OCRLang != defaultOCRLang {
		t.Errorf("OCRLang = %q, want %q", cfg.OCRLang, defaultOCRLang)
	}
}

func TestLoadConfig_Overrides(t *testing.T) {
	cfg := loadConfig(fakeGetenv(map[string]string{
		"DOXIE_DATA_DIR":    "/mnt/scans",
		"DOXIE_LISTEN_ADDR": ":9090",
		"DOXIE_DRIVER":      "some-other-driver",
		"DOXIE_OCR_LANG":    "deu",
	}))
	if cfg.DataDir != "/mnt/scans" {
		t.Errorf("DataDir = %q", cfg.DataDir)
	}
	if cfg.ListenAddr != ":9090" {
		t.Errorf("ListenAddr = %q", cfg.ListenAddr)
	}
	if cfg.DriverName != "some-other-driver" {
		t.Errorf("DriverName = %q", cfg.DriverName)
	}
	if cfg.OCRLang != "deu" {
		t.Errorf("OCRLang = %q", cfg.OCRLang)
	}
}

func TestLoadConfig_Paperless(t *testing.T) {
	cfg := loadConfig(fakeGetenv(nil))
	if cfg.PaperlessURL != "" || cfg.PaperlessToken != "" {
		t.Errorf("Paperless should be off by default, got %+v", cfg)
	}
	cfg = loadConfig(fakeGetenv(map[string]string{
		"DOXIE_PAPERLESS_URL":   "  http://paperless.lan:8000 \n",
		"DOXIE_PAPERLESS_TOKEN": " tok\n",
	}))
	if cfg.PaperlessURL != "http://paperless.lan:8000" || cfg.PaperlessToken != "tok" {
		t.Errorf("values not trimmed: %+v", cfg)
	}
}

func TestLoadConfig_Button(t *testing.T) {
	if cfg := loadConfig(fakeGetenv(nil)); cfg.ButtonDuplex || cfg.NoButton {
		t.Errorf("defaults: %+v, want the button on and simplex", cfg)
	}
	cfg := loadConfig(fakeGetenv(map[string]string{"DOXIE_BUTTON_DUPLEX": " Yes ", "DOXIE_DISABLE_BUTTON": "1"}))
	if !cfg.ButtonDuplex || !cfg.NoButton {
		t.Errorf("got %+v", cfg)
	}
	if loadConfig(fakeGetenv(map[string]string{"DOXIE_BUTTON_DUPLEX": "nope"})).ButtonDuplex {
		t.Error("an unrecognised value should not turn duplex on")
	}
}
