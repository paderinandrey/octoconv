package convert

import (
	"context"
	"fmt"
)

// imageFormats are the raster formats libvips converts between in this slice.
var imageFormats = []string{"png", "jpg", "webp", "heic", "tiff"}

// LibvipsConverter converts raster images by shelling out to the `vips` CLI.
// The output format is selected by outPath's extension (e.g. out.webp).
type LibvipsConverter struct{}

// Pairs returns every ordered pair of supported image formats (from != to).
func (LibvipsConverter) Pairs() []Pair {
	pairs := make([]Pair, 0, len(imageFormats)*(len(imageFormats)-1))
	for _, from := range imageFormats {
		for _, to := range imageFormats {
			if from != to {
				pairs = append(pairs, Pair{From: from, To: to})
			}
		}
	}
	return pairs
}

// Convert runs `vips copy <in> <out>`; libvips infers both codecs from the file
// extensions. ctx must carry the engine timeout.
func (LibvipsConverter) Convert(ctx context.Context, inPath, outPath string, _ map[string]any) error {
	if _, err := runCommand(ctx, "vips", "copy", inPath, outPath); err != nil {
		return fmt.Errorf("libvips: %w", err)
	}
	return nil
}

// Engine reports the image engine class (D-01).
func (LibvipsConverter) Engine() string { return EngineImage }
