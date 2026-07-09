package convert

// init wires concrete converters into the Default registry. To add support for
// a new engine or format pair, register it here with a single line.
func init() {
	Default.Register(LibvipsConverter{})
	Default.Register(LibreOfficeConverter{})
	// Future engines (one line each):
	// Default.Register(FFmpegConverter{})
}
