//go:build !windows

package systray

func runPlatform(cfg Config) error  { return ErrNotImplemented }
func quitPlatform()                 {}
func setTooltipPlatform(string)     {}
func setIconPlatform([]byte)        {}
func updateMenuPlatform([]MenuItem) {}
