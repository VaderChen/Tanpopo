package appversion

import "testing"

func TestCurrentIncludesBuildNumber(t *testing.T) {
	originalVersion := Version
	originalBuild := Build
	t.Cleanup(func() {
		Version = originalVersion
		Build = originalBuild
	})

	Version = "1.26.0829"
	Build = "1430"
	if actual := Current(); actual != "1.26.0829 build 1430" {
		t.Fatalf("顯示版本格式錯誤：%s", actual)
	}
	if actual := Release(); actual != "1.26.0829" {
		t.Fatalf("Release 版本不應包含 build 編號：%s", actual)
	}
}

func TestTagIncludesBuildNumber(t *testing.T) {
	originalVersion := Version
	originalBuild := Build
	t.Cleanup(func() {
		Version = originalVersion
		Build = originalBuild
	})

	Version = "1.26.0901"
	Build = "1507"
	if actual := Tag(); actual != "1.26.0901-build-1507" {
		t.Fatalf("Tag 應包含 build 編號：%s", actual)
	}
}
