package booxnote

import (
	"encoding/binary"
	"math"
	"testing"
)

// TestParsePointFile_V1 verifies AC1.4: Parser reads V1 point files with correct header,
// xref table, and 16-byte TinyPoint records (x, y, pressure, size, time).
func TestParsePointFile_V1(t *testing.T) {
	// Build a V1 point file manually with known structure.
	buf := make([]byte, 0, 1000)

	// 1. Header (76 bytes of zeros).
	header := make([]byte, pointHeaderSize)
	buf = append(buf, header...)

	// 2. Point data block for one shape.
	blockStart := len(buf)
	shapeID := "test-shape-uuid-12345678901234"

	// Attrs (4 bytes).
	attrs := make([]byte, pointAttrSize)
	binary.BigEndian.PutUint16(attrs[0:2], 0)
	binary.BigEndian.PutUint16(attrs[2:4], 0)
	buf = append(buf, attrs...)

	// Three TinyPoint records with known values.
	points := []TinyPoint{
		{X: 10.5, Y: 20.5, Size: 5, Pressure: 100, Time: 1000},
		{X: 11.5, Y: 21.5, Size: 6, Pressure: 101, Time: 1001},
		{X: 12.5, Y: 22.5, Size: 7, Pressure: 102, Time: 1002},
	}

	for _, pt := range points {
		ptBuf := make([]byte, tinyPointSize)
		binary.BigEndian.PutUint32(ptBuf[0:4], math.Float32bits(pt.X))
		binary.BigEndian.PutUint32(ptBuf[4:8], math.Float32bits(pt.Y))
		binary.BigEndian.PutUint16(ptBuf[8:10], uint16(pt.Size))
		binary.BigEndian.PutUint16(ptBuf[10:12], uint16(pt.Pressure))
		binary.BigEndian.PutUint32(ptBuf[12:16], pt.Time)
		buf = append(buf, ptBuf...)
	}

	blockLength := int32(len(buf) - blockStart)

	// 3. Xref table.
	xrefStart := len(buf)
	xrefEntry := make([]byte, xrefEntrySize)
	copy(xrefEntry[:36], []byte(shapeID))
	binary.BigEndian.PutUint32(xrefEntry[36:40], uint32(blockStart))
	binary.BigEndian.PutUint32(xrefEntry[40:44], uint32(blockLength))
	buf = append(buf, xrefEntry...)

	// 4. Xref offset (last 4 bytes).
	xrefOffsetBytes := make([]byte, 4)
	binary.BigEndian.PutUint32(xrefOffsetBytes, uint32(xrefStart))
	buf = append(buf, xrefOffsetBytes...)

	// Parse the file.
	result, err := parsePointFile(buf)
	if err != nil {
		t.Fatalf("parsePointFile failed: %v", err)
	}

	if len(result) != 1 {
		t.Errorf("got %d shapes, want 1", len(result))
	}

	parsedPoints, ok := result[shapeID]
	if !ok {
		t.Errorf("shape ID %q not found in result", shapeID)
	}

	if len(parsedPoints) != 3 {
		t.Fatalf("got %d points, want 3", len(parsedPoints))
	}

	// Verify first point.
	if parsedPoints[0].X != 10.5 {
		t.Errorf("point 0 X: got %.1f, want 10.5", parsedPoints[0].X)
	}
	if parsedPoints[0].Y != 20.5 {
		t.Errorf("point 0 Y: got %.1f, want 20.5", parsedPoints[0].Y)
	}
	if parsedPoints[0].Size != 5 {
		t.Errorf("point 0 Size: got %d, want 5", parsedPoints[0].Size)
	}
	if parsedPoints[0].Pressure != 100 {
		t.Errorf("point 0 Pressure: got %d, want 100", parsedPoints[0].Pressure)
	}
	if parsedPoints[0].Time != 1000 {
		t.Errorf("point 0 Time: got %d, want 1000", parsedPoints[0].Time)
	}

	// Verify second point.
	if parsedPoints[1].X != 11.5 {
		t.Errorf("point 1 X: got %.1f, want 11.5", parsedPoints[1].X)
	}
	if parsedPoints[1].Pressure != 101 {
		t.Errorf("point 1 Pressure: got %d, want 101", parsedPoints[1].Pressure)
	}

	// Verify third point.
	if parsedPoints[2].Time != 1002 {
		t.Errorf("point 2 Time: got %d, want 1002", parsedPoints[2].Time)
	}
}

// TestParsePointFile_UnsupportedVersion verifies AC1.7: Parser returns clear error
// for unsupported point file format version.
func TestParsePointFile_UnsupportedVersion(t *testing.T) {
	// Build a file with an invalid xref table size (not a multiple of 44).
	buf := make([]byte, 0, 200)

	// Header (76 bytes).
	header := make([]byte, pointHeaderSize)
	buf = append(buf, header...)

	// Point data (some placeholder).
	buf = append(buf, make([]byte, 100)...)

	// Xref that's not a multiple of 44 bytes.
	xrefStart := len(buf)
	buf = append(buf, make([]byte, 50)...) // 50 is not divisible by 44

	// Xref offset.
	xrefOffsetBytes := make([]byte, 4)
	binary.BigEndian.PutUint32(xrefOffsetBytes, uint32(xrefStart))
	buf = append(buf, xrefOffsetBytes...)

	// Try to parse.
	_, err := parsePointFile(buf)
	if err == nil {
		t.Errorf("got nil error for unsupported format, want error")
	}

	if err.Error() == "" {
		t.Errorf("got empty error message, want descriptive error")
	}

	// Check that the error message mentions unsupported format.
	if err.Error() != "booxnote: unsupported point file format version" {
		t.Errorf("got error %q, want 'booxnote: unsupported point file format version'", err.Error())
	}
}

// TestDecodeTinyPoints_EmptyBlock verifies that decodeTinyPoints handles
// blocks shorter than the attrs size without panicking.
func TestDecodeTinyPoints_EmptyBlock(t *testing.T) {
	// Block with only 2 bytes (shorter than pointAttrSize).
	block := []byte{0x00, 0x00}

	result := decodeTinyPoints(block)

	if result != nil {
		t.Errorf("got %v, want nil for empty block", result)
	}
}
