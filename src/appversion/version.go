package appversion

import "strings"

// Version、Build 與 Repository 可在正式建置時透過 -ldflags -X 覆寫。
var (
	Version    = "1.26.0829"
	Build      = "0000"
	Repository = "VaderChen/Tanpopo"
)

func Release() string {
	version := strings.TrimSpace(Version)
	if version == "" {
		return "dev"
	}
	return version
}

func Current() string {
	version := Release()
	build := strings.TrimSpace(Build)
	if build == "" || version == "dev" {
		return version
	}
	return version + " build " + build
}

// Tag 回傳與 GitHub Release 相同的可比較版本格式。Build 是正式版本順序的
// 一部分，確保同一天發布多個 Release 時仍能正確偵測更新。
func Tag() string {
	version := Release()
	build := strings.TrimSpace(Build)
	if build == "" || version == "dev" {
		return version
	}
	return version + "-build-" + build
}

func RepositoryName() string {
	return strings.Trim(strings.TrimSpace(Repository), "/")
}

func RepositoryURL() string {
	if repository := RepositoryName(); repository != "" {
		return "https://github.com/" + repository
	}
	return ""
}
