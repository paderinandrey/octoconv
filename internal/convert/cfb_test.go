package convert

import (
	"bytes"
	"encoding/binary"
	"os"
	"testing"
	"time"
	"unicode/utf16"
)

func TestClassifyCFB(t *testing.T) {
	t.Run("real fixture: legacy.doc classifies as CFBLegacy", func(t *testing.T) {
		data := readFixture(t, "testdata/legacy.doc")
		got := ClassifyCFB(bytes.NewReader(data), int64(len(data)))
		if got != CFBLegacy {
			t.Fatalf("ClassifyCFB(legacy.doc) = %v, want CFBLegacy", got)
		}
	})

	t.Run("real fixture: encrypted.docx classifies as CFBEncrypted", func(t *testing.T) {
		data := readFixture(t, "testdata/encrypted.docx")
		got := ClassifyCFB(bytes.NewReader(data), int64(len(data)))
		if got != CFBEncrypted {
			t.Fatalf("ClassifyCFB(encrypted.docx) = %v, want CFBEncrypted", got)
		}
	})

	t.Run("self-referential directory sector cycle returns CFBUnknown promptly", func(t *testing.T) {
		buf := cfbCyclicFATSample()

		// The parser's visited-set should already prevent a hang, but assert
		// bounded wall-clock time explicitly so a broken visited-set fails
		// fast with a clear diagnostic instead of the generic go-test timeout.
		done := make(chan CFBClass, 1)
		go func() {
			done <- ClassifyCFB(bytes.NewReader(buf), int64(len(buf)))
		}()
		select {
		case got := <-done:
			if got != CFBUnknown {
				t.Fatalf("ClassifyCFB(cyclic FAT) = %v, want CFBUnknown", got)
			}
		case <-time.After(1 * time.Second):
			t.Fatal("parser hung: ClassifyCFB did not return within 1s on a self-referential FAT chain")
		}
	})

	t.Run("truncated header (< 512 bytes) returns CFBUnknown", func(t *testing.T) {
		data := readFixture(t, "testdata/legacy.doc")
		truncated := data[:200]
		got := ClassifyCFB(bytes.NewReader(truncated), int64(len(truncated)))
		if got != CFBUnknown {
			t.Fatalf("ClassifyCFB(truncated) = %v, want CFBUnknown", got)
		}
	})

	t.Run("invalid SectorShift returns CFBUnknown", func(t *testing.T) {
		buf := cfbCorruptedSectorShiftSample()
		got := ClassifyCFB(bytes.NewReader(buf), int64(len(buf)))
		if got != CFBUnknown {
			t.Fatalf("ClassifyCFB(bad SectorShift) = %v, want CFBUnknown", got)
		}
	})

	t.Run("out-of-bounds FirstDirSectorLocation returns CFBUnknown", func(t *testing.T) {
		buf := cfbOutOfBoundsDirSample()
		got := ClassifyCFB(bytes.NewReader(buf), int64(len(buf)))
		if got != CFBUnknown {
			t.Fatalf("ClassifyCFB(OOB FirstDirSectorLocation) = %v, want CFBUnknown", got)
		}
	})

	t.Run("non-CFB bytes return CFBUnknown", func(t *testing.T) {
		data := []byte("not a cfb file at all")
		got := ClassifyCFB(bytes.NewReader(data), int64(len(data)))
		if got != CFBUnknown {
			t.Fatalf("ClassifyCFB(non-CFB) = %v, want CFBUnknown", got)
		}
	})

	t.Run("encrypted marker wins when both encrypted and legacy markers present", func(t *testing.T) {
		buf := cfbBothMarkersSample()
		got := ClassifyCFB(bytes.NewReader(buf), int64(len(buf)))
		if got != CFBEncrypted {
			t.Fatalf("ClassifyCFB(both markers) = %v, want CFBEncrypted (encrypted must win)", got)
		}
	})
}

// FuzzClassifyCFB is the CFB-02 phase-exit fuzz gate. It seeds with the two
// real fixtures plus every crafted corrupt variant from TestClassifyCFB
// above (so those inputs also run under a normal `go test`, giving CI
// tier 1/2 coverage automatically, D-05) and asserts only that ClassifyCFB
// never panics and always returns one of the three valid CFBClass values --
// the harness itself fails the run on any panic/hang, and the parser's own
// bounds/visited-set/cap guarantee termination within -fuzztime.
func FuzzClassifyCFB(f *testing.F) {
	if data, err := os.ReadFile("testdata/legacy.doc"); err == nil {
		f.Add(data)
		if len(data) > 200 {
			f.Add(data[:200]) // truncated header
		}
	}
	if data, err := os.ReadFile("testdata/encrypted.docx"); err == nil {
		f.Add(data)
	}

	f.Add(cfbCyclicFATSample())
	f.Add(cfbCorruptedSectorShiftSample())
	f.Add(cfbOutOfBoundsDirSample())
	f.Add(cfbBothMarkersSample())
	f.Add([]byte("not a cfb file at all"))

	f.Fuzz(func(t *testing.T, data []byte) {
		got := ClassifyCFB(bytes.NewReader(data), int64(len(data)))
		switch got {
		case CFBUnknown, CFBEncrypted, CFBLegacy:
			// valid outcome
		default:
			t.Fatalf("ClassifyCFB returned an invalid CFBClass %v for input of length %d", got, len(data))
		}
	})
}

func readFixture(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture %s: %v", path, err)
	}
	return data
}

// cfbEntryBytes builds one 128-byte directory entry with the given
// UTF-16LE name and object type, for hand-crafted test fixtures.
func cfbEntryBytes(name string, objType byte) [cfbEntrySize]byte {
	var e [cfbEntrySize]byte
	u16 := utf16.Encode([]rune(name))
	for i, u := range u16 {
		binary.LittleEndian.PutUint16(e[i*2:i*2+2], u)
	}
	nameLen := uint16((len(u16) + 1) * 2) // + trailing UTF-16 NUL
	binary.LittleEndian.PutUint16(e[64:66], nameLen)
	e[66] = objType
	return e
}

// buildCFBFile constructs a minimal, well-formed 512-byte-sector CFB byte
// buffer containing the given directory entries (packed across as many
// directory sectors as needed) followed by one FAT sector describing the
// directory-sector chain (0 -> 1 -> ... -> ENDOFCHAIN by default).
// fatOverride lets a test corrupt specific FAT[sector] values, e.g. a
// self-referential cycle.
func buildCFBFile(entries [][cfbEntrySize]byte, fatOverride map[uint32]uint32) []byte {
	const sectorSize = 512
	const entriesPerSector = sectorSize / cfbEntrySize

	numDirSectors := (len(entries) + entriesPerSector - 1) / entriesPerSector
	if numDirSectors == 0 {
		numDirSectors = 1
	}
	fatSectorNum := uint32(numDirSectors)

	buf := make([]byte, sectorSize+(numDirSectors+1)*sectorSize)

	copy(buf[0:8], oleCFBMagic)
	binary.LittleEndian.PutUint16(buf[30:32], 9) // SectorShift 9 -> 512-byte sectors
	binary.LittleEndian.PutUint32(buf[48:52], 0) // FirstDirSectorLocation = sector 0
	binary.LittleEndian.PutUint32(buf[68:72], cfbEndOfChain)
	binary.LittleEndian.PutUint32(buf[76:80], fatSectorNum) // DIFAT[0] -> FAT sector number
	for i := 1; i < cfbNumHeaderDIFATSlots; i++ {
		off := cfbDIFATOffset + i*4
		binary.LittleEndian.PutUint32(buf[off:off+4], 0xFFFFFFFF) // FREESECT: unused DIFAT slot
	}

	for i, e := range entries {
		sectorIdx := i / entriesPerSector
		entryIdx := i % entriesPerSector
		off := sectorSize + sectorIdx*sectorSize + entryIdx*cfbEntrySize
		copy(buf[off:off+cfbEntrySize], e[:])
	}

	fatOff := sectorSize + numDirSectors*sectorSize
	for i := 0; i < numDirSectors; i++ {
		next := uint32(i + 1)
		if i == numDirSectors-1 {
			next = cfbEndOfChain
		}
		binary.LittleEndian.PutUint32(buf[fatOff+i*4:fatOff+i*4+4], next)
	}
	for sector, next := range fatOverride {
		binary.LittleEndian.PutUint32(buf[fatOff+int(sector)*4:fatOff+int(sector)*4+4], next)
	}

	return buf
}

// cfbCyclicFATSample builds a minimal CFB buffer whose sole directory
// sector's FAT next-pointer points back at itself (D-04 DoS case).
func cfbCyclicFATSample() []byte {
	return buildCFBFile([][cfbEntrySize]byte{cfbEntryBytes("WordDocument", 0x02)}, map[uint32]uint32{0: 0})
}

// cfbCorruptedSectorShiftSample builds an otherwise-valid CFB buffer with an
// invalid (neither 0x0009 nor 0x000C) SectorShift field.
func cfbCorruptedSectorShiftSample() []byte {
	buf := buildCFBFile([][cfbEntrySize]byte{cfbEntryBytes("WordDocument", 0x02)}, nil)
	binary.LittleEndian.PutUint16(buf[30:32], 0x00FF)
	return buf
}

// cfbOutOfBoundsDirSample builds an otherwise-valid CFB buffer whose
// FirstDirSectorLocation points past the end of the (small) buffer.
func cfbOutOfBoundsDirSample() []byte {
	buf := buildCFBFile([][cfbEntrySize]byte{cfbEntryBytes("WordDocument", 0x02)}, nil)
	binary.LittleEndian.PutUint32(buf[48:52], 0x00FFFFFF)
	return buf
}

// cfbBothMarkersSample builds a CFB buffer whose directory contains both an
// encrypted marker (EncryptionInfo) and a legacy marker (WordDocument) to
// prove encrypted wins (D-03).
func cfbBothMarkersSample() []byte {
	return buildCFBFile([][cfbEntrySize]byte{
		cfbEntryBytes("EncryptionInfo", 0x02),
		cfbEntryBytes("WordDocument", 0x02),
	}, nil)
}
