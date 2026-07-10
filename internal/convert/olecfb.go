package convert

import (
	"bytes"
	"io"
)

// oleCFBMagic is the 8-byte Compound File Binary (OLE2/MS-CFB) signature
// shared by two otherwise-unrelated unconvertible file classes: legacy
// binary Office documents (.doc/.xls/.ppt, pre-OOXML) AND password-protected
// OOXML documents wrapped in Microsoft's "Agile Encryption" container --
// both begin with this identical header, so this milestone deliberately
// does not attempt to distinguish the two sub-cases (SAFE-01, D-05).
// Distinguishing them (by parsing the CFB directory) is deferred to v2
// (DOCV3-02).
var oleCFBMagic = []byte{0xD0, 0xCF, 0x11, 0xE0, 0xA1, 0xB1, 0x1A, 0xE1}

// IsOLECFB reports whether r begins with the OLE-CFB magic signature. This
// is a fail-closed pre-flight rejection check, NOT a Sniff/SniffContainer
// registry entry: no Converter is ever registered for this "format", so
// every match is unconditionally rejected by internal/api before any
// storage write -- unlike the registry sniff tables, whose contract is
// "detected AND supported".
func IsOLECFB(r io.ReaderAt) bool {
	buf := make([]byte, len(oleCFBMagic))
	n, _ := r.ReadAt(buf, 0)
	return n == len(oleCFBMagic) && bytes.Equal(buf, oleCFBMagic)
}
