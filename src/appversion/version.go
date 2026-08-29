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

func RepositoryName() string {
	return strings.Trim(strings.TrimSpace(Repository), "/")
}

func RepositoryURL() string {
	if repository := RepositoryName(); repository != "" {
		return "https://github.com/" + repository
	}
	return ""
}
