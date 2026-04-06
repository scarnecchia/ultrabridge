package booxnote

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"testing"

	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"

	pb "github.com/sysop/ultrabridge/internal/booxnote/proto"
)

// TestOpen_WrappedNoteInfo verifies parsing when note_info is inside a container message (field 1).
// Real Boox devices (Palma2 Pro C, NoteAir5C) use this format.
func TestOpen_WrappedNoteInfo(t *testing.T) {
	// Build a normal note_info protobuf
	pageListJSON, _ := json.Marshal([]string{"page1"})
	noteInfo := &pb.NoteInfo{
		Title:        "Wrapped Title",
		PageNameList: string(pageListJSON),
	}
	innerData, _ := proto.Marshal(noteInfo)

	// Wrap it in a container message: field 1, bytes type
	var wrapped []byte
	wrapped = protowire.AppendTag(wrapped, 1, protowire.BytesType)
	wrapped = protowire.AppendBytes(wrapped, innerData)

	// Build ZIP with the wrapped note_info
	buf := &bytes.Buffer{}
	zw := zip.NewWriter(buf)

	writeZIPEntry(zw, "noteA/note/pb/note_info", wrapped)

	// VirtualPage for page1
	vp := &pb.VirtualPage{
		PageId:     "page1",
		PageSize:   "100.0x200.0",
		OrderIndex: 0,
	}
	vpData, _ := proto.Marshal(vp)
	writeZIPEntry(zw, "noteA/virtual/page/pb/page1", vpData)

	zw.Close()
	data := buf.Bytes()

	note, err := Open(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	if note.Title != "Wrapped Title" {
		t.Errorf("title = %q, want 'Wrapped Title'", note.Title)
	}
	if len(note.Pages) != 1 {
		t.Fatalf("got %d pages, want 1", len(note.Pages))
	}
	if note.Pages[0].PageID != "page1" {
		t.Errorf("pageID = %q, want 'page1'", note.Pages[0].PageID)
	}
}

// TestOpen_WrappedPageNameList verifies parsing when pageNameList is a JSON object
// {"pageNameList":["id"]} instead of a bare array ["id"].
func TestOpen_WrappedPageNameList(t *testing.T) {
	// Use the wrapped JSON format for pageNameList
	wrappedJSON := `{"pageNameList":["page1"]}`
	noteInfo := &pb.NoteInfo{
		Title:        "Wrapped List",
		PageNameList: wrappedJSON,
	}
	noteInfoData, _ := proto.Marshal(noteInfo)

	buf := &bytes.Buffer{}
	zw := zip.NewWriter(buf)

	writeZIPEntry(zw, "noteB/note/pb/note_info", noteInfoData)

	vp := &pb.VirtualPage{
		PageId:     "page1",
		PageSize:   "100.0x200.0",
		OrderIndex: 0,
	}
	vpData, _ := proto.Marshal(vp)
	writeZIPEntry(zw, "noteB/virtual/page/pb/page1", vpData)

	zw.Close()
	data := buf.Bytes()

	note, err := Open(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	if note.Title != "Wrapped List" {
		t.Errorf("title = %q, want 'Wrapped List'", note.Title)
	}
	if len(note.Pages) != 1 {
		t.Fatalf("got %d pages, want 1", len(note.Pages))
	}
}

// TestOpen_RectPageSize verifies parsing when pageSize is a bounding rect JSON
// {"left":0,"top":0,"right":824,"bottom":1648} instead of "WxH" or {"width":N,"height":N}.
func TestOpen_RectPageSize(t *testing.T) {
	rectJSON := `{"bottom":1648.0,"empty":false,"left":0.0,"right":824.0,"stability":0,"top":0.0}`
	pageListJSON, _ := json.Marshal([]string{"page1"})
	noteInfo := &pb.NoteInfo{
		Title:        "Rect Size",
		PageNameList: string(pageListJSON),
	}
	noteInfoData, _ := proto.Marshal(noteInfo)

	buf := &bytes.Buffer{}
	zw := zip.NewWriter(buf)

	writeZIPEntry(zw, "noteC/note/pb/note_info", noteInfoData)

	vp := &pb.VirtualPage{
		PageId:     "page1",
		PageSize:   rectJSON,
		OrderIndex: 0,
	}
	vpData, _ := proto.Marshal(vp)
	writeZIPEntry(zw, "noteC/virtual/page/pb/page1", vpData)

	zw.Close()
	data := buf.Bytes()

	note, err := Open(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	if len(note.Pages) != 1 {
		t.Fatalf("got %d pages, want 1", len(note.Pages))
	}
	pg := note.Pages[0]
	if pg.Width != 824 {
		t.Errorf("width = %f, want 824", pg.Width)
	}
	if pg.Height != 1648 {
		t.Errorf("height = %f, want 1648", pg.Height)
	}
}

// TestParsePageSize_AllFormats verifies all three pageSize formats.
func TestParsePageSize_AllFormats(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		wantW  float64
		wantH  float64
		wantOK bool
	}{
		{"WxH", "1860.0x2480.0", 1860, 2480, true},
		{"JSON width/height", `{"width":1860,"height":2480}`, 1860, 2480, true},
		{"JSON rect", `{"left":0,"top":0,"right":824,"bottom":1648}`, 824, 1648, true},
		{"empty", "", 0, 0, false},
		{"garbage", "notasize", 0, 0, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			w, h, err := parsePageSize(tc.input)
			if tc.wantOK && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !tc.wantOK && err == nil {
				t.Fatal("expected error, got nil")
			}
			if tc.wantOK {
				if w != tc.wantW {
					t.Errorf("width = %f, want %f", w, tc.wantW)
				}
				if h != tc.wantH {
					t.Errorf("height = %f, want %f", h, tc.wantH)
				}
			}
		})
	}
}

// TestOpen_DirectoryEntriesInPointFiles verifies that ZIP directory entries
// under the point/ path are skipped without error.
func TestOpen_DirectoryEntriesInPointFiles(t *testing.T) {
	pageListJSON, _ := json.Marshal([]string{"page1"})
	noteInfo := &pb.NoteInfo{
		Title:        "Dir Test",
		PageNameList: string(pageListJSON),
	}
	noteInfoData, _ := proto.Marshal(noteInfo)

	buf := &bytes.Buffer{}
	zw := zip.NewWriter(buf)

	writeZIPEntry(zw, "noteD/note/pb/note_info", noteInfoData)

	vp := &pb.VirtualPage{
		PageId:     "page1",
		PageSize:   "100.0x200.0",
		OrderIndex: 0,
	}
	vpData, _ := proto.Marshal(vp)
	writeZIPEntry(zw, "noteD/virtual/page/pb/page1", vpData)

	// Add a directory entry under point/ (like real Boox ZIPs have)
	_, err := zw.Create("noteD/point/page1/")
	if err != nil {
		t.Fatalf("create dir entry: %v", err)
	}

	zw.Close()
	data := buf.Bytes()

	note, err := Open(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("Open failed with directory entries: %v", err)
	}
	if note.Title != "Dir Test" {
		t.Errorf("title = %q, want 'Dir Test'", note.Title)
	}
}

// TestUnwrapField1 verifies the container message unwrapping logic.
func TestUnwrapField1(t *testing.T) {
	// Not wrapped — should return input unchanged
	plain := []byte{0x32, 0x03, 'a', 'b', 'c'} // field 6, bytes "abc"
	result := unwrapField1(plain)
	if !bytes.Equal(result, plain) {
		t.Error("unwrapField1 should return non-wrapped data unchanged")
	}

	// Wrapped — field 1 containing inner bytes
	inner := []byte{0x32, 0x03, 'x', 'y', 'z'} // field 6, bytes "xyz"
	var wrapped []byte
	wrapped = protowire.AppendTag(wrapped, 1, protowire.BytesType)
	wrapped = protowire.AppendBytes(wrapped, inner)
	result = unwrapField1(wrapped)
	if !bytes.Equal(result, inner) {
		t.Errorf("unwrapField1 should extract inner bytes, got %v", result)
	}

	// Empty — should return empty
	result = unwrapField1([]byte{})
	if len(result) != 0 {
		t.Error("unwrapField1 should handle empty input")
	}
}
