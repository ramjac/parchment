package docinterop

import (
	"archive/zip"
	"errors"
	"io"
)

const (
	MaxInputSize        = 64 << 20  // 64 MiB
	MaxUncompressedSize = 128 << 20 // 128 MiB
	MaxEntries          = 1000
)

// ReadZip opens a zip file and enforces limits to prevent zip bombs.
func ReadZip(r io.ReaderAt, size int64) (*zip.Reader, error) {
	if size > MaxInputSize {
		return nil, errors.New("input archive exceeds 64 MiB limit")
	}
	zr, err := zip.NewReader(r, size)
	if err != nil {
		return nil, err
	}
	if len(zr.File) > MaxEntries {
		return nil, errors.New("archive exceeds 1000 entries limit")
	}

	var totalSize uint64
	for _, f := range zr.File {
		totalSize += f.UncompressedSize64
		if totalSize > MaxUncompressedSize {
			return nil, errors.New("archive exceeds 128 MiB uncompressed size limit")
		}
	}

	return zr, nil
}
