//go:build !darwin

package desktopui

func hasGraphicalSession() bool {
	return false
}
