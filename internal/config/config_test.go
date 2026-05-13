package config

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestDefaultValidates(t *testing.T) {
	d := Default()
	if err := d.Validate(); err != nil {
		t.Fatalf("default config must validate: %v", err)
	}
}

func TestDefaultSeedsBuiltinPresets(t *testing.T) {
	d := Default()
	wantNames := []string{"Flashy", "Punchy", "Vivid", "Smooth", "Lazy"}
	if len(d.Presets) != len(wantNames) {
		t.Fatalf("expected %d default presets, got %d", len(wantNames), len(d.Presets))
	}
	for i, n := range wantNames {
		if d.Presets[i].Name != n {
			t.Errorf("preset[%d]: got %q, want %q", i, d.Presets[i].Name, n)
		}
		if d.Presets[i].Description == "" {
			t.Errorf("preset[%d] %q: description must not be empty", i, d.Presets[i].Name)
		}
	}
}

func TestValidateRejectsBadFields(t *testing.T) {
	c := Default()
	c.Squelch.Threshold = -1
	c.AGC.Mode = "wat"
	c.Bands.Scale = "loglog"
	c.Beat.MinIntervalMs = 1
	err := c.Validate()
	if err == nil {
		t.Fatal("expected validation error")
	}
	ve, ok := err.(*ValidationError)
	if !ok {
		t.Fatalf("expected *ValidationError, got %T", err)
	}
	for _, want := range []string{"squelch.threshold", "agc.mode", "bands.scale", "beat.min_interval_ms"} {
		if _, ok := ve.Fields[want]; !ok {
			t.Errorf("expected error for %q, got %v", want, ve.Fields)
		}
	}
	for _, want := range []string{"squelch.threshold", "agc.mode"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error string missing %q: %s", want, err.Error())
		}
	}
}

func TestValidatePresetErrorsArePrefixed(t *testing.T) {
	c := Default()
	c.Presets[1].Settings.AGC.Mode = "garbage"
	c.Presets[1].Name = ""
	err := c.Validate()
	if err == nil {
		t.Fatal("expected validation error")
	}
	ve := err.(*ValidationError)
	if _, ok := ve.Fields["presets[1].agc.mode"]; !ok {
		t.Errorf("expected presets[1].agc.mode error, got %v", ve.Fields)
	}
	if _, ok := ve.Fields["presets[1].name"]; !ok {
		t.Errorf("expected presets[1].name error, got %v", ve.Fields)
	}
}

func TestValidateRejectsDuplicatePresetNames(t *testing.T) {
	c := Default()
	c.Presets = []Preset{
		{Name: "Same", Settings: DefaultDSP()},
		{Name: "Same", Settings: DefaultDSP()},
	}
	err := c.Validate()
	if err == nil {
		t.Fatal("expected duplicate-name error")
	}
	ve := err.(*ValidationError)
	msg, ok := ve.Fields["presets[1].name"]
	if !ok || !strings.Contains(msg, "duplicate") {
		t.Errorf("expected duplicate hint on presets[1].name, got %v", ve.Fields)
	}
}

func TestLoadOrCreateSeedsDefaults(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "subdir", "config.toml")

	cfg, err := LoadOrCreate(path)
	if err != nil {
		t.Fatalf("LoadOrCreate: %v", err)
	}
	if !reflect.DeepEqual(cfg, Default()) {
		t.Errorf("expected default config, got %+v", cfg)
	}
	cfg2, err := LoadOrCreate(path)
	if err != nil {
		t.Fatalf("re-load: %v", err)
	}
	if !reflect.DeepEqual(cfg2, cfg) {
		t.Errorf("round-trip mismatch:\n  got %+v\n want %+v", cfg2, cfg)
	}
}

func TestSaveAtomic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	c := Default()
	c.AGC.Mode = AGCVivid
	if err := Save(path, c); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := LoadOrCreate(path)
	if err != nil {
		t.Fatalf("LoadOrCreate: %v", err)
	}
	if got.AGC.Mode != AGCVivid {
		t.Errorf("expected AGC.Mode=vivid after save, got %q", got.AGC.Mode)
	}
	if !reflect.DeepEqual(got.Presets, c.Presets) {
		t.Errorf("presets did not round-trip:\n  got %+v\n want %+v", got.Presets, c.Presets)
	}
}

func TestSavePersistsCustomPreset(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	c := Default()
	custom := Preset{
		Name:        "MyCustom",
		Description: "for the kitchen lights",
		Settings:    DefaultDSP(),
	}
	custom.Settings.Beat.MinIntervalMs = 700
	c.Presets = append(c.Presets, custom)
	if err := Save(path, c); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := LoadOrCreate(path)
	if err != nil {
		t.Fatalf("LoadOrCreate: %v", err)
	}
	if len(got.Presets) != 6 || got.Presets[5].Name != "MyCustom" {
		t.Fatalf("custom preset missing after round-trip: %+v", got.Presets)
	}
	if got.Presets[5].Settings.Beat.MinIntervalMs != 700 {
		t.Errorf("custom preset settings did not round-trip: %+v", got.Presets[5].Settings.Beat)
	}
}

func TestSaveRejectsInvalid(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	c := Default()
	c.AGC.Mode = "garbage"
	if err := Save(path, c); err == nil {
		t.Fatal("expected validation error from Save")
	}
}

func TestStoreApply(t *testing.T) {
	persisted := 0
	st := NewStore(Default(), func(c Config) error {
		persisted++
		return nil
	})

	next := Default()
	next.Beat.MinIntervalMs = 400
	if err := st.Apply(next); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if got := st.Load().Beat.MinIntervalMs; got != 400 {
		t.Errorf("Load after Apply: got %d, want 400", got)
	}
	if persisted != 1 {
		t.Errorf("expected 1 persist call, got %d", persisted)
	}

	bad := Default()
	bad.AGC.Mode = "wat"
	prev := *st.Load()
	if err := st.Apply(bad); err == nil {
		t.Fatal("expected validation error")
	}
	if !reflect.DeepEqual(*st.Load(), prev) {
		t.Error("invalid Apply must not mutate stored config")
	}
	if persisted != 1 {
		t.Errorf("invalid Apply must not persist; calls=%d", persisted)
	}
}
