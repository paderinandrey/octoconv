package convert

import (
	"bytes"
	"encoding/binary"
	"io"
	"unicode/utf16"
)

// CFBClass is the outcome of classifying an OLE-CFB (Compound File Binary)
// upload by reading ONLY its root-storage directory entry names. It is
// deliberately NOT wired into the Converter registry -- see IsOLECFB
// (olecfb.go) for why: every CFB match is an unconditional rejection, this
// merely refines WHICH rejection message the caller should show.
type CFBClass int

// CFBUnknown is the zero value on purpose (D-04/Pitfall 11): every early
// return, parse error, bounds violation, or cycle/cap trip falls through to
// CFBUnknown by construction rather than requiring an explicit assignment at
// every failure site. This is the fail-closed default -- ClassifyCFB never
// "accepts" a file, it only distinguishes rejection reasons.
const (
	CFBUnknown CFBClass = iota
	CFBEncrypted
	CFBLegacy
)

// MS-CFB structural constants (see the ms_cfb_facts block in
// .planning/phases/22-cfb-classification/22-01-PLAN.md for the spec this
// mirrors). This parser intentionally supports only the 109 header-DIFAT
// entries and never reads stream content, mini-FAT, or the directory
// red-black tree -- see cfbWalkDirectory.
const (
	cfbHeaderSize          = 512
	cfbNumHeaderDIFATSlots = 109
	cfbDIFATOffset         = 76
	cfbEntrySize           = 128
	cfbNameFieldSize       = 64
	cfbMaxDirSectors       = 4096

	// cfbEndOfChain is the FAT/DIFAT terminator sentinel.
	cfbEndOfChain uint32 = 0xFFFFFFFE

	// cfbSentinelThreshold: any sector number >= this value is one of the
	// four reserved FAT sentinels (FATSECT 0xFFFFFFFD, ENDOFCHAIN
	// 0xFFFFFFFE, FREESECT 0xFFFFFFFF, DIFSECT 0xFFFFFFFC) or otherwise
	// invalid -- never a real sector index (ms_cfb_facts).
	cfbSentinelThreshold uint32 = 0xFFFFFFFA
)

// ClassifyCFB reads the CFB header, FAT (header-DIFAT entries only), and
// directory-sector chain of r (size bytes long) and classifies it by the set
// of decoded root-storage entry NAMES -- never stream content. It returns
// CFBEncrypted if an EncryptionInfo/EncryptedPackage stream is present
// (encrypted wins over legacy if both are present, D-03), CFBLegacy if a
// legacy-binary Office marker stream is present (WordDocument, Workbook,
// Book, or "PowerPoint Document"), or CFBUnknown otherwise.
//
// Every declared offset/count/sector index is bounds-checked against size
// before use; every FAT-chain follow is guarded by a visited-set and a hard
// sector cap. Any short read, out-of-range field, unsupported >109-DIFAT-
// sector file, or chain cycle/cap-exceeded returns CFBUnknown -- fail
// closed, never a panic, never an unbounded loop (D-04, T-22-01, T-22-02).
func ClassifyCFB(r io.ReaderAt, size int64) CFBClass {
	if size < cfbHeaderSize {
		return CFBUnknown
	}

	header := make([]byte, cfbHeaderSize)
	if n, _ := r.ReadAt(header, 0); n != cfbHeaderSize {
		return CFBUnknown
	}
	if !bytes.Equal(header[0:8], oleCFBMagic) {
		return CFBUnknown
	}

	sectorShift := binary.LittleEndian.Uint16(header[30:32])
	if sectorShift != 9 && sectorShift != 12 {
		return CFBUnknown
	}
	sectorSize := 1 << sectorShift

	firstDirSector := binary.LittleEndian.Uint32(header[48:52])
	firstDIFATSector := binary.LittleEndian.Uint32(header[68:72])

	// This parser only follows the 109 header-DIFAT entries. A file that
	// declares additional DIFAT sectors (roughly >7MB) is outside this
	// bound and is rejected fail-closed rather than walked further -- both
	// target fixtures are ~9KB and never need DIFAT sectors (ms_cfb_facts).
	if firstDIFATSector != cfbEndOfChain {
		return CFBUnknown
	}

	fat := buildCFBFAT(r, header, sectorSize, size)
	if fat == nil {
		return CFBUnknown
	}

	names, ok := cfbWalkDirectory(r, firstDirSector, sectorSize, size, fat)
	if !ok {
		return CFBUnknown
	}
	return classifyCFBNames(names)
}

// buildCFBFAT reads the FAT sectors named by the header's 109 DIFAT entries
// and returns a sparse map from sector number to its FAT "next sector"
// value. It returns nil on any bounds violation or short read (fail
// closed) -- the caller treats a nil map identically to CFBUnknown.
func buildCFBFAT(r io.ReaderAt, header []byte, sectorSize int, size int64) map[uint32]uint32 {
	fat := make(map[uint32]uint32)
	entriesPerFATSector := sectorSize / 4

	for i := 0; i < cfbNumHeaderDIFATSlots; i++ {
		off := cfbDIFATOffset + i*4
		fatSector := binary.LittleEndian.Uint32(header[off : off+4])
		if fatSector >= cfbSentinelThreshold {
			// Unused DIFAT slot (FREESECT) or another reserved sentinel:
			// not a real FAT sector, nothing more to read from this slot.
			continue
		}
		if !cfbSectorInBounds(fatSector, sectorSize, size) {
			return nil
		}

		data := make([]byte, sectorSize)
		if n, _ := r.ReadAt(data, cfbSectorOffset(fatSector, sectorSize)); n != sectorSize {
			return nil
		}

		base := uint32(i) * uint32(entriesPerFATSector)
		for j := 0; j < entriesPerFATSector; j++ {
			fat[base+uint32(j)] = binary.LittleEndian.Uint32(data[j*4 : j*4+4])
		}
	}
	return fat
}

// cfbWalkDirectory follows the directory-sector FAT chain starting at
// start, collecting every decoded entry name from every allocated
// (storage/stream/root-storage) 128-byte directory entry. It never walks
// the red-black sibling/child tree -- a flat linear scan of all entries in
// every visited sector is sufficient to enumerate every stream name and
// avoids that tree-traversal bug surface (Pitfall 10).
//
// ok is false (names discarded) on any bounds violation, short read, cycle
// (repeat visit to a sector), cap-exceeded (more than cfbMaxDirSectors
// sectors), or undetermined chain continuation -- the caller must treat
// that identically to CFBUnknown rather than classifying partial results.
func cfbWalkDirectory(r io.ReaderAt, start uint32, sectorSize int, size int64, fat map[uint32]uint32) (names []string, ok bool) {
	visited := make(map[uint32]bool)
	entriesPerSector := sectorSize / cfbEntrySize
	sector := start
	count := 0

	for sector < cfbSentinelThreshold {
		if visited[sector] {
			return nil, false // cycle: repeat visit to an already-walked sector
		}
		if count >= cfbMaxDirSectors {
			return nil, false // hard cap exceeded
		}
		if !cfbSectorInBounds(sector, sectorSize, size) {
			return nil, false
		}
		visited[sector] = true
		count++

		data := make([]byte, sectorSize)
		if n, _ := r.ReadAt(data, cfbSectorOffset(sector, sectorSize)); n != sectorSize {
			return nil, false
		}

		for e := 0; e < entriesPerSector; e++ {
			off := e * cfbEntrySize
			entry := data[off : off+cfbEntrySize]

			objType := entry[66]
			if objType == 0x00 {
				continue // unallocated entry
			}

			nameLen := binary.LittleEndian.Uint16(entry[64:66])
			if nameLen < 2 || nameLen > cfbNameFieldSize || nameLen%2 != 0 {
				// A single malformed entry is not a cycle/DoS -- skip it
				// and keep scanning the rest of this sector's entries.
				continue
			}

			u16 := make([]uint16, nameLen/2)
			for k := range u16 {
				u16[k] = binary.LittleEndian.Uint16(entry[k*2 : k*2+2])
			}
			if len(u16) > 0 && u16[len(u16)-1] == 0 {
				u16 = u16[:len(u16)-1] // strip trailing UTF-16 NUL
			}
			names = append(names, string(utf16.Decode(u16)))
		}

		next, known := fat[sector]
		if !known {
			// Chain continuation undetermined (sector not covered by any
			// header-DIFAT-referenced FAT range) -- fail closed rather
			// than classify on a possibly-incomplete name set.
			return nil, false
		}
		sector = next
	}
	return names, true
}

// classifyCFBNames applies the D-03 allow-lists over the decoded entry name
// set. Encrypted markers win if both an encrypted and a legacy marker are
// present.
func classifyCFBNames(names []string) CFBClass {
	hasEncrypted := false
	hasLegacy := false
	for _, n := range names {
		switch n {
		case "EncryptionInfo", "EncryptedPackage":
			hasEncrypted = true
		case "WordDocument", "Workbook", "Book", "PowerPoint Document":
			hasLegacy = true
		}
	}
	switch {
	case hasEncrypted:
		return CFBEncrypted
	case hasLegacy:
		return CFBLegacy
	default:
		return CFBUnknown
	}
}

// cfbSectorOffset returns the byte offset of sector number sector. Sector 0
// begins at offset sectorSize (the header always occupies the first 512
// bytes, followed by padding to sectorSize for v4/4096-byte-sector files).
func cfbSectorOffset(sector uint32, sectorSize int) int64 {
	return (int64(sector) + 1) * int64(sectorSize)
}

// cfbSectorInBounds reports whether sector is a real, in-range sector index
// that can be fully read from a file of size bytes.
func cfbSectorInBounds(sector uint32, sectorSize int, size int64) bool {
	if sector >= cfbSentinelThreshold {
		return false
	}
	off := cfbSectorOffset(sector, sectorSize)
	return off >= 0 && off+int64(sectorSize) <= size
}
