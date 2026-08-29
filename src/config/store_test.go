package config

import (
	"testing"

	"LlamaLoader/src/domain"
)

func TestNormalizeSettingsPreservesSupportedTheme(t *testing.T) {
	value := DefaultSettings()
	value.UITheme = " WISTERIA "

	actual := normalizeSettings(value)
	if actual.UITheme != "wisteria" {
		t.Fatalf("預期 wisteria，實際為 %q", actual.UITheme)
	}
}

func TestNormalizeSettingsMigratesMissingTheme(t *testing.T) {
	value := DefaultSettings()
	value.UITheme = ""

	actual := normalizeSettings(value)
	if actual.UITheme != "tanpopo" {
		t.Fatalf("舊設定應沿用 tanpopo，實際為 %q", actual.UITheme)
	}
}

func TestValidateSettingsRejectsUnknownTheme(t *testing.T) {
	value := DefaultSettings()
	value.UITheme = "unknown"

	if err := ValidateSettings(domain.Settings(value)); err == nil {
		t.Fatal("不支援的介面配色應回傳錯誤")
	}
}
