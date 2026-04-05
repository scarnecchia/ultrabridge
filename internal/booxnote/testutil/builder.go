package testutil

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"testing"

	"google.golang.org/protobuf/proto"

	"github.com/sysop/ultrabridge/internal/booxnote"
	pb "github.com/sysop/ultrabridge/internal/booxnote/proto"
)

// TestPage describes a page to be included in a test note.
type TestPage struct {
	PageID string
	Width  float64
	Height float64
	Shapes []*pb.ShapeInfoProto
	Points map[string][]booxnote.TinyPoint // shapeID → point data
}

// TestNoteOpts customizes the test note.
type TestNoteOpts struct {
	NoteID string
	Title  string
	Pages  []*TestPage
}

// BuildTestNoteFile constructs a complete Boox .note ZIP file on disk with the given options.
// Returns the absolute path to the created .note file.
func BuildTestNoteFile(t *testing.T, tmpDir string, opts TestNoteOpts) string {
	t.Helper()

	if opts.NoteID == "" {
		opts.NoteID = "test-note-id"
	}
	if opts.Title == "" {
		opts.Title = "Test Note"
	}

	// Build ZIP in memory
	buf := &bytes.Buffer{}
	zw := zip.NewWriter(buf)
	defer zw.Close()

	// Build note_info protobuf
	pageNameList := make([]string, len(opts.Pages))
	for i, pg := range opts.Pages {
		pageNameList[i] = pg.PageID
	}
	pageListJSON, err := json.Marshal(pageNameList)
	if err != nil {
		t.Fatalf("marshal pageNameList: %v", err)
	}

	noteInfo := &pb.NoteInfo{
		Title:        opts.Title,
		PageNameList: string(pageListJSON),
	}
	noteInfoData, err := proto.Marshal(noteInfo)
	if err != nil {
		t.Fatalf("marshal note_info: %v", err)
	}

	// Write note_info to ZIP
	if err := writeZIPEntry(zw, opts.NoteID+"/note/pb/note_info", noteInfoData); err != nil {
		t.Fatalf("write note_info: %v", err)
	}

	// Write each page's virtual page protobuf, shape ZIP, and point files
	for i, pg := range opts.Pages {
		// Write VirtualPage protobuf
		vp := &pb.VirtualPage{
			PageId:     pg.PageID,
			PageSize:   fmt.Sprintf("%.1fx%.1f", pg.Width, pg.Height),
			OrderIndex: float32(i),
		}
		vpData, err := proto.Marshal(vp)
		if err != nil {
			t.Fatalf("marshal virtual page: %v", err)
		}
		vpPath := opts.NoteID + "/virtual/page/pb/" + pg.PageID
		if err := writeZIPEntry(zw, vpPath, vpData); err != nil {
			t.Fatalf("write virtual page: %v", err)
		}

		// Write shape ZIP (nested)
		if len(pg.Shapes) > 0 {
			shapeZIPData := buildShapeZIP(t, pg.Shapes)
			shapePath := opts.NoteID + "/shape/" + pg.PageID + "#shapes.zip"
			if err := writeZIPEntry(zw, shapePath, shapeZIPData); err != nil {
				t.Fatalf("write shape zip: %v", err)
			}
		}

		// Write point files
		if len(pg.Points) > 0 {
			// All points for a page go into a single point file
			pointFileData := buildPointFile(t, pg.Points)
			pointPath := opts.NoteID + "/point/" + pg.PageID + "/points"
			if err := writeZIPEntry(zw, pointPath, pointFileData); err != nil {
				t.Fatalf("write point file: %v", err)
			}
		}
	}

	zw.Close()

	// Write to disk
	notePath := tmpDir + "/" + opts.NoteID + ".note"
	if err := os.WriteFile(notePath, buf.Bytes(), 0644); err != nil {
		t.Fatalf("write note file: %v", err)
	}

	return notePath
}

// buildShapeZIP creates a nested ZIP containing a serialized ShapeInfoProtoList
func buildShapeZIP(t *testing.T, shapes []*pb.ShapeInfoProto) []byte {
	t.Helper()
	innerBuf := &bytes.Buffer{}
	innerZW := zip.NewWriter(innerBuf)

	// Marshal ShapeInfoProtoList
	list := &pb.ShapeInfoProtoList{
		Proto: shapes,
	}
	pbData, err := proto.Marshal(list)
	if err != nil {
		t.Fatalf("marshal ShapeInfoProtoList: %v", err)
	}

	// Write to inner ZIP
	if err := writeZIPEntry(innerZW, "shapes", pbData); err != nil {
		t.Fatalf("write shapes to inner zip: %v", err)
	}

	innerZW.Close()
	return innerBuf.Bytes()
}

// buildPointFile creates a V1 point file with the given shape→points mapping
// Format:
// [Header: 76 bytes]
// [Point data blocks: variable]
// [Xref table: N * 44 bytes]
// [Xref offset: 4 bytes (last 4 bytes)]
func buildPointFile(t *testing.T, pointMap map[string][]booxnote.TinyPoint) []byte {
	t.Helper()
	buf := &bytes.Buffer{}

	// Write 76-byte header (all zeros for test purposes)
	header := make([]byte, 76) // pointHeaderSize
	if _, err := buf.Write(header); err != nil {
		t.Fatalf("write header: %v", err)
	}

	// Collect point data blocks and build xref table
	type blockInfo struct {
		shapeID string
		offset  int32
		length  int32
		data    []byte
	}
	blocks := make([]blockInfo, 0, len(pointMap))

	for shapeID, points := range pointMap {
		blockOffset := int32(buf.Len())

		// Write point data block: 4-byte attrs + point records
		attrs := make([]byte, 4) // pointAttrSize
		binary.BigEndian.PutUint16(attrs[0:2], 0)
		binary.BigEndian.PutUint16(attrs[2:4], 0)
		if _, err := buf.Write(attrs); err != nil {
			t.Fatalf("write point attrs: %v", err)
		}

		// Write TinyPoint records
		for _, pt := range points {
			if err := writeTinyPoint(buf, pt); err != nil {
				t.Fatalf("write tiny point: %v", err)
			}
		}

		blockLength := int32(buf.Len()) - blockOffset
		blockData := make([]byte, blockLength)
		copy(blockData, buf.Bytes()[blockOffset:blockOffset+blockLength])

		blocks = append(blocks, blockInfo{
			shapeID: shapeID,
			offset:  blockOffset,
			length:  blockLength,
			data:    blockData,
		})
	}

	// Write xref table
	xrefOffset := int32(buf.Len())
	for _, block := range blocks {
		if err := writeXrefEntry(buf, block.shapeID, block.offset, block.length); err != nil {
			t.Fatalf("write xref entry: %v", err)
		}
	}

	// Write xref offset (last 4 bytes)
	offsetBytes := make([]byte, 4)
	binary.BigEndian.PutUint32(offsetBytes, uint32(xrefOffset))
	if _, err := buf.Write(offsetBytes); err != nil {
		t.Fatalf("write xref offset: %v", err)
	}

	return buf.Bytes()
}

// writeTinyPoint writes a single 16-byte TinyPoint record in big-endian format
func writeTinyPoint(buf io.Writer, pt booxnote.TinyPoint) error {
	rec := make([]byte, 16) // tinyPointSize
	binary.BigEndian.PutUint32(rec[0:4], math.Float32bits(pt.X))
	binary.BigEndian.PutUint32(rec[4:8], math.Float32bits(pt.Y))
	binary.BigEndian.PutUint16(rec[8:10], uint16(pt.Size))
	binary.BigEndian.PutUint16(rec[10:12], uint16(pt.Pressure))
	binary.BigEndian.PutUint32(rec[12:16], pt.Time)
	_, err := buf.Write(rec)
	return err
}

// writeXrefEntry writes a 44-byte xref entry: 36 bytes shape ID (padded), 4 bytes offset, 4 bytes length
func writeXrefEntry(buf io.Writer, shapeID string, offset, length int32) error {
	entry := make([]byte, 44) // xrefEntrySize
	copy(entry[:36], []byte(shapeID))
	binary.BigEndian.PutUint32(entry[36:40], uint32(offset))
	binary.BigEndian.PutUint32(entry[40:44], uint32(length))
	_, err := buf.Write(entry)
	return err
}

// writeZIPEntry writes a single entry to a ZIP
func writeZIPEntry(zw *zip.Writer, name string, data []byte) error {
	w, err := zw.Create(name)
	if err != nil {
		return err
	}
	_, err = w.Write(data)
	return err
}
