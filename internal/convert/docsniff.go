package convert

import (
	"archive/zip"
	"errors"
	"io"
)

// ContainerResult is the outcome of a single ZIP-central-directory pass used
// to disambiguate the six OOXML/ODF office formats and simultaneously
// gather the zip-bomb (D-03) and macro-part (D-05) safety signals.
type ContainerResult struct {
	Format            string // "docx"|"xlsx"|"pptx"|"odt"|"ods"|"odp"|"" (unrecognized)
	TotalUncompressed uint64
	HasMacro          bool
	DuplicateRootPart bool // fail-closed signal (RESEARCH.md Pitfall 3)
}

// ErrNotAZip is returned when SniffContainer's input cannot be parsed as a
// ZIP central directory at all (as opposed to being a valid-but-unrecognized
// zip, which returns ContainerResult{} with Format=="" and a nil error).
var ErrNotAZip = errors.New("not a valid zip container")

// ooxmlRootParts is the closed set of OOXML root-part paths this project
// disambiguates by name-presence, not position (D-01/D-02).
// Source: verified via Go programs against real pandoc/openpyxl-generated
// docx/pptx/xlsx fixtures (RESEARCH.md Pattern 1) -- root-part names are
// position-independent in the ZIP central directory.
var ooxmlRootParts = map[string]string{
	"word/document.xml":    "docx",
	"xl/workbook.xml":      "xlsx",
	"ppt/presentation.xml": "pptx",
}

// odfMimetypes is the closed set of OASIS-mandated ODF media-type payloads
// found in the first ZIP entry ("mimetype", stored uncompressed).
// Source: OASIS OpenDocument v1.2 Part 3 §17.4 (mimetype file requirement).
var odfMimetypes = map[string]string{
	"application/vnd.oasis.opendocument.text":         "odt",
	"application/vnd.oasis.opendocument.spreadsheet":  "ods",
	"application/vnd.oasis.opendocument.presentation": "odp",
}

// ooxmlMacroParts is the closed set of literal OOXML macro-storage paths
// (docm/xlsm/pptm and similar macro-enabled variants).
// Source: xlsxwriter documentation -- "a package must contain at most one
// VBA Project part" at word|xl|ppt/vbaProject.bin.
var ooxmlMacroParts = map[string]bool{
	"word/vbaProject.bin": true,
	"xl/vbaProject.bin":   true,
	"ppt/vbaProject.bin":  true,
}

// SniffContainer inspects a ZIP-shaped upload's central directory to
// disambiguate the six supported office formats (D-01), sum declared
// uncompressed size across every entry for the zip-bomb guard (D-02/D-03),
// and detect macro-carrying parts (D-03/D-05) -- all in a single pass over
// the archive/zip central directory (RESEARCH.md Pitfall 4: never re-parse
// the central directory more than once per upload). r must be the original,
// unconsumed upload source (e.g. multipart.File), not a re-stitched
// io.Reader -- the archive/zip package requires positional access.
func SniffContainer(r io.ReaderAt, size int64) (ContainerResult, error) {
	zr, err := zip.NewReader(r, size)
	if err != nil {
		return ContainerResult{}, ErrNotAZip
	}

	var res ContainerResult
	nameCount := map[string]int{}

	for i, f := range zr.File {
		res.TotalUncompressed += f.UncompressedSize64

		if ooxmlMacroParts[f.Name] || hasBasicPrefix(f.Name) {
			res.HasMacro = true
			nameCount[f.Name]++
		}
		if fmtName, ok := ooxmlRootParts[f.Name]; ok {
			res.Format = fmtName
			nameCount[f.Name]++
		}

		if i == 0 && f.Name == "mimetype" && f.Method == zip.Store {
			payload, rerr := readBounded(f, 128)
			if rerr == nil {
				if odfFmt, ok := odfMimetypes[string(payload)]; ok {
					res.Format = odfFmt
				}
			}
		}
	}

	for _, c := range nameCount {
		if c > 1 {
			res.DuplicateRootPart = true
		}
	}
	return res, nil
}

// hasBasicPrefix reports whether name lives under ODF's "Basic/" macro-
// storage directory (script-lc.xml at the root plus script-lb.xml/module
// files under arbitrary, user-defined library-name subdirectories) -- a
// prefix match, not a literal library-name match, so a renamed library is
// still caught.
func hasBasicPrefix(name string) bool {
	return len(name) >= 6 && name[:6] == "Basic/"
}

// readBounded opens f and reads at most max bytes -- used only for the
// small "mimetype" payload comparison; every other SniffContainer decision
// (format, size, macro) uses central-directory metadata alone with zero
// decompression.
func readBounded(f *zip.File, max int64) ([]byte, error) {
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return io.ReadAll(io.LimitReader(rc, max))
}
