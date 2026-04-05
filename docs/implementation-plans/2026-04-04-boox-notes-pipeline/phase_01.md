# Boox Notes Pipeline — Phase 1: Boox .note Parser

**Goal:** Pure-Go library that reads the Boox .note ZIP format and produces structured data for rendering and OCR.

**Architecture:** New package `internal/booxnote/` with protobuf-generated types, a ZIP reader, binary point file reader, and a top-level `Open()` function that returns a parsed `Note` struct with pages, shapes, and stroke points. Follows the same external-parser pattern as `go-sn` for Supernote notes.

**Tech Stack:** `archive/zip`, `google.golang.org/protobuf` (proto.Unmarshal), `encoding/binary` (big-endian point data), `encoding/json` (boundingRect/matrixValues/pageSize fields).

**Scope:** 7 phases from original design (phase 1 of 7)

**Codebase verified:** 2026-04-04

**Reference documentation:**
- Protobuf schema: `/home/jtd/booxreverse/BOOX_STROKE_FORMAT.md`
- Decompiled proto classes: `/home/jtd/booxreverse/noteair5c/decompiled/com.onyx.android.ksync/sources/com/onyx/android/sdk/scribble/data/proto/`
- Project testing patterns: `/home/jtd/ultrabridge/internal/processor/processor_test.go` (lifecycle), `/home/jtd/ultrabridge/internal/processor/worker_test.go` (mocks)
- Project CLAUDE.md: `/home/jtd/ultrabridge/CLAUDE.md`

---

## Acceptance Criteria Coverage

This phase implements and tests:

### boox-notes-pipeline.AC1: Parser reads Boox .note format
- **boox-notes-pipeline.AC1.1 Success:** Parser extracts note title from note/pb/note_info protobuf
- **boox-notes-pipeline.AC1.2 Success:** Parser extracts page list with dimensions from virtual/page/pb
- **boox-notes-pipeline.AC1.3 Success:** Parser deserializes ShapeInfoProtoList from nested shape ZIP, extracting shapeType, color, thickness, boundingRect, matrixValues
- **boox-notes-pipeline.AC1.4 Success:** Parser reads V1 point files: 76-byte header, xref table, 16-byte TinyPoint records (x, y, pressure, size, time)
- **boox-notes-pipeline.AC1.5 Success:** Parser correlates shapes to point data via matching UUIDs
- **boox-notes-pipeline.AC1.6 Failure:** Parser returns clear error for corrupt/truncated ZIP
- **boox-notes-pipeline.AC1.7 Failure:** Parser returns clear error for unsupported point file format version
- **boox-notes-pipeline.AC1.8 Edge:** Parser handles notes with zero shapes (blank pages)
- **boox-notes-pipeline.AC1.9 Edge:** Parser handles multi-page notes

---

<!-- START_SUBCOMPONENT_A (tasks 1-2) -->
<!-- START_TASK_1 -->
### Task 1: Add protobuf dependency and create .proto schema file

**Files:**
- Modify: `/home/jtd/ultrabridge/go.mod` (add google.golang.org/protobuf dependency)
- Create: `/home/jtd/ultrabridge/internal/booxnote/proto/boox.proto`

**Step 1: Add protobuf dependency**

Run:
```bash
go -C /home/jtd/ultrabridge get google.golang.org/protobuf
```

Expected: Dependency added to go.mod and go.sum without errors.

**Step 2: Create the .proto schema file**

Create `/home/jtd/ultrabridge/internal/booxnote/proto/boox.proto` with the following content. This schema is reconstructed from decompiled Java protobuf classes at `/home/jtd/booxreverse/noteair5c/decompiled/com.onyx.android.ksync/sources/com/onyx/android/sdk/scribble/data/proto/` and validated against the format documentation at `/home/jtd/booxreverse/BOOX_STROKE_FORMAT.md`.

```protobuf
syntax = "proto3";

package booxpb;

option go_package = "github.com/sysop/ultrabridge/internal/booxnote/proto";

// NoteInfo — parsed from {noteId}/note/pb/note_info within the ZIP.
// Source: NoteInfoProto.java (NoteInfo inner class)
message NoteInfo {
  string uniqueId = 1;
  int64 createdAt = 2;
  // Field 3 not observed in decompiled code
  string parentUniqueId = 4;
  string subPageName = 5;
  string title = 6;
  string extraAttributes = 7;
  int32 type = 8;
  float strokeWidth = 9;
  float eraserWidth = 10;
  string notePenInfo = 11;
  string notePageInfo = 12;
  string noteBackground = 13;
  string deviceInfo = 14;
  int32 strokeColor = 15;
  int32 currentShapeType = 16;
  string background = 17;
  string lineLayoutBackground = 18;
  int32 position = 19;
  string pageNameList = 20;       // JSON array of page ID strings
  string richTextPageNameList = 21;
  float pageOriginWidth = 22;
  float pageOriginHeight = 23;
  string source = 24;
  string associateDate = 25;
  int32 asyncStatus = 26;
  int32 encryptionType = 27;
  string digest = 28;
  string associationId = 29;
  int32 associationType = 30;
  int32 status = 31;
  int64 cloudNoteSize = 32;
  int64 cloudUpdatedAt = 33;
  string commitId = 34;
  int64 embeddedAt = 35;
  string syncFrom = 36;
  int32 noteSyncStatus = 37;
  bool favorite = 38;
  // Fields 39-40 not observed
  string groupId = 41;
  string thumbnailRes = 42;
  string activeScene = 43;
  string removePageList = 44;
  int32 miniRequiredVersion = 45;
}

// VirtualDoc — parsed from {noteId}/virtual/doc/pb/{docId}.
// Source: VirtualDocPageProto.java (VirtualDoc inner class)
message VirtualDoc {
  string uniqueId = 1;
  int64 createdAt = 2;
  int64 updatedAt = 3;
  string viewportId = 4;
  float actualScale = 5;
  float offsetX = 6;
  float offsetY = 7;
  string coverPageId = 8;
  string defaultTemplate = 9;
  string docOptions = 10;
}

// VirtualPage — parsed from {noteId}/virtual/page/pb/{pageId}.
// Source: VirtualDocPageProto.java (VirtualPage inner class)
message VirtualPage {
  string pageId = 1;
  int64 createdAt = 2;
  int64 updatedAt = 3;
  float orderIndex = 4;
  int32 status = 5;
  string pageSize = 6;            // JSON: e.g. "1860.0x2480.0" or "{\"width\":1860,\"height\":2480}"
  string pageSizeWithMargin = 7;
  string contentPageSize = 8;
  string contentType = 9;
  string contentRelativePath = 10;
  string contentPageId = 11;
  string contentId = 12;
  int32 contentStatus = 13;
}

// VirtualPageList — wrapper for repeated VirtualPage.
// Source: VirtualDocPageProto.java (VirtualPageList inner class)
message VirtualPageList {
  repeated VirtualPage proto = 1;
}

// NotePageModel — parsed from {noteId}/pageModel/pb/{pageModelId}.
// Source: NotePageModelProto.java
message NotePageModel {
  string uniqueId = 1;
  string layerList = 2;           // JSON array
  string currentLayerId = 3;
  string title = 4;
  int64 createdAt = 5;
  int64 updatedAt = 6;
  string pageSize = 7;
}

// NotePageModelList — wrapper for repeated NotePageModel.
message NotePageModelList {
  repeated NotePageModel proto = 1;
}

// ShapeInfoProto — stroke/shape metadata.
// Source: NoteShapeDocProto.java, BOOX_STROKE_FORMAT.md
message ShapeInfoProto {
  string uniqueId = 1;
  int64 createdAt = 2;
  int64 updatedAt = 3;
  int32 color = 4;                // ARGB packed int
  float thickness = 5;
  int32 zorder = 6;
  string boundingRect = 7;        // JSON: {"left":f,"top":f,"right":f,"bottom":f}
  string matrixValues = 8;        // JSON: affine transform matrix values
  string textStyle = 9;
  string text = 10;
  string createArgs = 11;
  int32 shapeType = 12;           // see ShapeType constants
  string groupId = 13;
  string resource = 14;
  int32 shapeStatus = 15;
  string revisionId = 16;
  string lineStyle = 17;
  string infoRevisionId = 18;
  string tagIdList = 19;
  string extra = 20;
  string linkIdList = 21;
  string richText = 22;
  int32 fillColor = 23;           // ARGB packed int
  bytes pointList = 24;           // inline stroke data (often empty; see point files)
  string connectionBean = 25;
  string optionsRepo = 26;
}

// ShapeInfoProtoList — wrapper for repeated ShapeInfoProto.
// Serialized inside nested ZIP at shape/{pageId}#...zip
message ShapeInfoProtoList {
  repeated ShapeInfoProto proto = 1;
}
```

**Step 3: Verify proto file syntax**

Run:
```bash
go -C /home/jtd/ultrabridge build ./internal/booxnote/proto/
```

This will fail (no Go files yet) — that's expected. The proto file is just a schema reference at this point.

**Commit:** `chore(booxnote): add protobuf dependency and .proto schema for Boox note format`
<!-- END_TASK_1 -->

<!-- START_TASK_2 -->
### Task 2: Generate Go code from .proto schema

**Files:**
- Create: `/home/jtd/ultrabridge/internal/booxnote/proto/boox.pb.go` (generated)
- Create: `/home/jtd/ultrabridge/internal/booxnote/proto/generate.go` (go:generate directive)

**Step 1: Create generate.go with go:generate directive**

Create `/home/jtd/ultrabridge/internal/booxnote/proto/generate.go`:

```go
package proto

//go:generate protoc --go_out=. --go_opt=paths=source_relative boox.proto
```

**Step 2: Generate the Go code**

Run:
```bash
go -C /home/jtd/ultrabridge generate ./internal/booxnote/proto/
```

Expected: `boox.pb.go` generated in `internal/booxnote/proto/`. If `protoc` or `protoc-gen-go` is not installed on the build machine, install them first:
```bash
# protoc-gen-go (Go plugin)
go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
```

For `protoc` itself, it must be installed on the system. If unavailable, the generated `.pb.go` file will be committed to the repo so builds don't require protoc.

**Step 3: Verify build**

Run:
```bash
go -C /home/jtd/ultrabridge build ./internal/booxnote/proto/
```

Expected: Builds without errors.

**Commit:** `chore(booxnote): generate Go protobuf code from Boox schema`
<!-- END_TASK_2 -->
<!-- END_SUBCOMPONENT_A -->

<!-- START_SUBCOMPONENT_B (tasks 3-5) -->
<!-- START_TASK_3 -->
### Task 3: Create Note types and ZIP opener

**Files:**
- Create: `/home/jtd/ultrabridge/internal/booxnote/note.go`

**Implementation:**

Create the main parser types and `Open()` function. The `Open()` function accepts an `io.ReaderAt` + size (to support both files and in-memory bytes) and returns a parsed `Note` struct.

Key types to define:

```go
package booxnote

import (
    "archive/zip"
    "encoding/json"
    "fmt"
    "io"
    "path"
    "sort"
    "strings"

    "google.golang.org/protobuf/proto"

    pb "github.com/sysop/ultrabridge/internal/booxnote/proto"
)

// Note represents a fully parsed Boox .note file.
type Note struct {
    NoteID string   // top-level directory name in the ZIP
    Title  string   // from note_info protobuf
    Pages  []*Page  // ordered by orderIndex
}

// Page represents a single page within a note.
type Page struct {
    PageID string
    Width  float64
    Height float64
    Shapes []*Shape // ordered by zorder
}

// Shape represents a single shape (stroke, geometry, text, etc.) on a page.
type Shape struct {
    UniqueID     string
    ShapeType    int32
    Color        int32   // ARGB packed
    FillColor    int32   // ARGB packed
    Thickness    float32
    ZOrder       int32
    BoundingRect *Rect
    MatrixValues []float64
    Text         string
    RevisionID   string
    Points       []TinyPoint // populated from point files or inline pointList
}

// Rect is a bounding rectangle parsed from JSON.
type Rect struct {
    Left   float64 `json:"left"`
    Top    float64 `json:"top"`
    Right  float64 `json:"right"`
    Bottom float64 `json:"bottom"`
}

// TinyPoint is a single stroke sample (16 bytes in the binary format).
type TinyPoint struct {
    X        float32
    Y        float32
    Size     int16
    Pressure int16
    Time     uint32
}

// Open parses a Boox .note ZIP from the given reader.
func Open(r io.ReaderAt, size int64) (*Note, error) {
    zr, err := zip.NewReader(r, size)
    if err != nil {
        return nil, fmt.Errorf("booxnote: open zip: %w", err)
    }

    // Index ZIP entries by name for O(1) lookup.
    entries := make(map[string]*zip.File, len(zr.File))
    var noteID string
    for _, f := range zr.File {
        entries[f.Name] = f
        // Extract noteId from top-level directory.
        if noteID == "" {
            parts := strings.SplitN(f.Name, "/", 2)
            if len(parts) > 1 {
                noteID = parts[0]
            }
        }
    }
    if noteID == "" {
        return nil, fmt.Errorf("booxnote: no note ID directory found in ZIP")
    }

    note := &Note{NoteID: noteID}

    // Parse note_info for title and page name list.
    noteInfoPath := noteID + "/note/pb/note_info"
    pageNames, err := parseNoteInfo(entries, noteInfoPath, note)
    if err != nil {
        return nil, err
    }

    // Parse each page.
    for _, pageName := range pageNames {
        pg, err := parsePage(entries, noteID, pageName)
        if err != nil {
            return nil, fmt.Errorf("booxnote: page %s: %w", pageName, err)
        }
        note.Pages = append(note.Pages, pg)
    }

    return note, nil
}
```

The `parseNoteInfo` helper reads and unmarshals the note_info protobuf, extracts the title, and returns the page name list (JSON array of page ID strings). The `parsePage` helper reads the VirtualPage protobuf for dimensions, the nested shape ZIP for shapes, and point files for stroke data.

Implement these helper functions:

1. `parseNoteInfo(entries, path, note) ([]string, error)` — reads note_info protobuf, sets `note.Title`, returns page names from `pageNameList` JSON field
2. `parsePage(entries, noteID, pageID) (*Page, error)` — reads VirtualPage protobuf for dimensions, calls `parseShapes` and `parsePoints`
3. `readEntry(entries, name) ([]byte, error)` — helper to read a single ZIP entry into bytes

For page dimensions, the `pageSize` field in VirtualPage is a string. Based on decompiled code, it may be in format `"1860.0x2480.0"` (width x height) or a JSON object. Parse both formats:

```go
func parsePageSize(s string) (width, height float64, err error) {
    // Try "WxH" format first.
    if parts := strings.SplitN(s, "x", 2); len(parts) == 2 {
        w, err1 := strconv.ParseFloat(parts[0], 64)
        h, err2 := strconv.ParseFloat(parts[1], 64)
        if err1 == nil && err2 == nil {
            return w, h, nil
        }
    }
    // Try JSON format.
    var dim struct {
        Width  float64 `json:"width"`
        Height float64 `json:"height"`
    }
    if err := json.Unmarshal([]byte(s), &dim); err == nil && dim.Width > 0 && dim.Height > 0 {
        return dim.Width, dim.Height, nil
    }
    return 0, 0, fmt.Errorf("booxnote: cannot parse page size: %q", s)
}
```

**Verification:**

Run:
```bash
go -C /home/jtd/ultrabridge build ./internal/booxnote/
```

Expected: Builds without errors.

**Commit:** `feat(booxnote): add Note types and ZIP opener`
<!-- END_TASK_3 -->

<!-- START_TASK_4 -->
### Task 4: Implement shape parser (nested ZIP deserialization)

**Verifies:** boox-notes-pipeline.AC1.3

**Files:**
- Create: `/home/jtd/ultrabridge/internal/booxnote/shape.go`
- Modify: `/home/jtd/ultrabridge/internal/booxnote/note.go` (wire parseShapes into parsePage)

**Implementation:**

Shape data lives in nested ZIPs within the outer .note ZIP at paths like `{noteId}/shape/{pageId}#...zip`. Each nested ZIP contains a single entry with a serialized `ShapeInfoProtoList` protobuf.

Create `shape.go` with:

```go
package booxnote

import (
    "archive/zip"
    "bytes"
    "encoding/json"
    "fmt"
    "io"
    "sort"
    "strings"

    "google.golang.org/protobuf/proto"

    pb "github.com/sysop/ultrabridge/internal/booxnote/proto"
)

// parseShapes reads the nested shape ZIP for a page and returns parsed shapes
// sorted by zorder.
func parseShapes(entries map[string]*zip.File, noteID, pageID string) ([]*Shape, error) {
    // Find the shape ZIP entry. Path pattern: {noteId}/shape/{pageId}#...zip
    var shapeEntry *zip.File
    prefix := noteID + "/shape/" + pageID + "#"
    for name, f := range entries {
        if strings.HasPrefix(name, prefix) {
            shapeEntry = f
            break
        }
    }
    if shapeEntry == nil {
        // No shapes for this page (blank page — AC1.8).
        return nil, nil
    }

    // Read the nested ZIP.
    rc, err := shapeEntry.Open()
    if err != nil {
        return nil, fmt.Errorf("open shape zip: %w", err)
    }
    defer rc.Close()

    data, err := io.ReadAll(rc)
    if err != nil {
        return nil, fmt.Errorf("read shape zip: %w", err)
    }

    innerZR, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
    if err != nil {
        return nil, fmt.Errorf("open inner shape zip: %w", err)
    }

    // The inner ZIP typically has one entry containing the serialized ShapeInfoProtoList.
    if len(innerZR.File) == 0 {
        return nil, nil
    }

    innerRC, err := innerZR.File[0].Open()
    if err != nil {
        return nil, fmt.Errorf("open shape proto entry: %w", err)
    }
    defer innerRC.Close()

    pbData, err := io.ReadAll(innerRC)
    if err != nil {
        return nil, fmt.Errorf("read shape proto: %w", err)
    }

    var list pb.ShapeInfoProtoList
    if err := proto.Unmarshal(pbData, &list); err != nil {
        return nil, fmt.Errorf("unmarshal ShapeInfoProtoList: %w", err)
    }

    shapes := make([]*Shape, 0, len(list.GetProto()))
    for _, sp := range list.GetProto() {
        s := &Shape{
            UniqueID:   sp.GetUniqueId(),
            ShapeType:  sp.GetShapeType(),
            Color:      sp.GetColor(),
            FillColor:  sp.GetFillColor(),
            Thickness:  sp.GetThickness(),
            ZOrder:     sp.GetZorder(),
            Text:       sp.GetText(),
            RevisionID: sp.GetRevisionId(),
        }

        // Parse bounding rect JSON.
        if br := sp.GetBoundingRect(); br != "" {
            var r Rect
            if err := json.Unmarshal([]byte(br), &r); err == nil {
                s.BoundingRect = &r
            }
        }

        // Parse matrix values JSON.
        if mv := sp.GetMatrixValues(); mv != "" {
            var vals []float64
            if err := json.Unmarshal([]byte(mv), &vals); err == nil {
                s.MatrixValues = vals
            }
        }

        // Read inline point data if present.
        if pl := sp.GetPointList(); len(pl) > 0 {
            s.Points = decodeTinyPoints(pl)
        }

        shapes = append(shapes, s)
    }

    // Sort by zorder.
    sort.Slice(shapes, func(i, j int) bool {
        return shapes[i].ZOrder < shapes[j].ZOrder
    })

    return shapes, nil
}
```

Wire `parseShapes` into `parsePage` in `note.go` — call it after reading page dimensions and assign results to `pg.Shapes`.

**Verification:**

Run:
```bash
go -C /home/jtd/ultrabridge build ./internal/booxnote/
```

Expected: Builds without errors.

**Commit:** `feat(booxnote): add nested shape ZIP parser with protobuf deserialization`
<!-- END_TASK_4 -->

<!-- START_TASK_5 -->
### Task 5: Implement V1 point file reader

**Verifies:** boox-notes-pipeline.AC1.4, boox-notes-pipeline.AC1.7

**Files:**
- Create: `/home/jtd/ultrabridge/internal/booxnote/point.go`
- Modify: `/home/jtd/ultrabridge/internal/booxnote/note.go` (wire point reading into parsePage)

**Implementation:**

Point files in the ZIP at `{noteId}/point/{pageId}/{shapeId}#points` follow the V1 format documented in `/home/jtd/booxreverse/BOOX_STROKE_FORMAT.md`:

```
[Header: 76 bytes]
[Point data blocks: variable]
[Xref table: N * 44 bytes]
[Xref offset: 4 bytes (last 4 bytes of file)]
```

Each xref entry (44 bytes):
```
Offset 0:  36 bytes UTF-8 — shape unique ID
Offset 36:  4 bytes int32 — offset into file (to point data block)
Offset 40:  4 bytes int32 — length of point data block
```

Each point data block starts with 4 bytes (attrA: int16, attrB: int16), then N × 16-byte TinyPoint records (big-endian: float32 x, float32 y, int16 size, int16 pressure, uint32 time).

Create `point.go`:

```go
package booxnote

import (
    "archive/zip"
    "encoding/binary"
    "fmt"
    "io"
    "strings"
)

const (
    pointHeaderSize = 76
    xrefEntrySize   = 44
    tinyPointSize   = 16
    pointAttrSize   = 4
)

// xrefEntry maps a shape UUID to its point data block within a point file.
type xrefEntry struct {
    ShapeID string
    Offset  int32
    Length  int32
}

// parsePointFile reads a V1 point file and returns a map of shapeID → []TinyPoint.
func parsePointFile(data []byte) (map[string][]TinyPoint, error) {
    if len(data) < pointHeaderSize+4 {
        return nil, fmt.Errorf("point file too short: %d bytes", len(data))
    }

    // Read xref offset from last 4 bytes.
    xrefOff := int(binary.BigEndian.Uint32(data[len(data)-4:]))
    if xrefOff < pointHeaderSize || xrefOff >= len(data)-4 {
        return nil, fmt.Errorf("invalid xref offset: %d (file size %d)", xrefOff, len(data))
    }

    // Validate this looks like V1 format by checking header contains
    // a reasonable xref offset and the file is large enough.
    xrefData := data[xrefOff : len(data)-4]
    if len(xrefData)%xrefEntrySize != 0 {
        return nil, fmt.Errorf("booxnote: unsupported point file format version")
    }

    nEntries := len(xrefData) / xrefEntrySize
    result := make(map[string][]TinyPoint, nEntries)

    for i := 0; i < nEntries; i++ {
        entry := xrefData[i*xrefEntrySize : (i+1)*xrefEntrySize]

        // Shape ID: 36 bytes UTF-8, null-trimmed.
        shapeID := strings.TrimRight(string(entry[:36]), "\x00")
        offset := int(binary.BigEndian.Uint32(entry[36:40]))
        length := int(binary.BigEndian.Uint32(entry[40:44]))

        if offset < 0 || offset+length > len(data) {
            return nil, fmt.Errorf("point data block out of range for shape %s", shapeID)
        }

        block := data[offset : offset+length]
        points := decodeTinyPoints(block)
        result[shapeID] = points
    }

    return result, nil
}

// decodeTinyPoints decodes a point data block into TinyPoint slice.
// Block format: 4-byte attrs (attrA: int16, attrB: int16) + N × 16-byte points.
func decodeTinyPoints(block []byte) []TinyPoint {
    if len(block) < pointAttrSize {
        return nil
    }
    pointData := block[pointAttrSize:]
    n := len(pointData) / tinyPointSize
    if n == 0 {
        return nil
    }

    points := make([]TinyPoint, n)
    for i := 0; i < n; i++ {
        off := i * tinyPointSize
        rec := pointData[off : off+tinyPointSize]
        points[i] = TinyPoint{
            X:        math.Float32frombits(binary.BigEndian.Uint32(rec[0:4])),
            Y:        math.Float32frombits(binary.BigEndian.Uint32(rec[4:8])),
            Size:     int16(binary.BigEndian.Uint16(rec[8:10])),
            Pressure: int16(binary.BigEndian.Uint16(rec[10:12])),
            Time:     binary.BigEndian.Uint32(rec[12:16]),
        }
    }
    return points
}
```

Use `math.Float32frombits` from the standard `math` package (add `"math"` to imports). Do NOT use `unsafe.Pointer`.

Wire point file reading into `parsePage` in `note.go`:
- Find all point file entries matching `{noteId}/point/{pageID}/`
- For each, read and parse with `parsePointFile`
- Merge results into a `map[string][]TinyPoint` keyed by shape UUID
- After parsing shapes, correlate: for each shape with empty Points, look up by shape.UniqueID in the point map

**Verification:**

Run:
```bash
go -C /home/jtd/ultrabridge build ./internal/booxnote/
```

Expected: Builds without errors.

**Commit:** `feat(booxnote): add V1 point file reader with xref table parsing`
<!-- END_TASK_5 -->

<!-- START_TASK_6 -->
### Task 6: Wire shape-to-point correlation in parsePage

**Verifies:** boox-notes-pipeline.AC1.5

**Files:**
- Modify: `/home/jtd/ultrabridge/internal/booxnote/note.go` (complete parsePage implementation)

**Implementation:**

The `parsePage` function should:
1. Read the VirtualPage protobuf for this page's dimensions
2. Call `parseShapes` to get shapes from the nested shape ZIP
3. Read all point files for this page from `{noteId}/point/{pageID}/`
4. For each shape that has empty `Points`, look up its `UniqueID` in the point map and populate `Points`

The correlation logic:

```go
// In parsePage, after parseShapes:
pointMap, err := readPagePoints(entries, noteID, pageID)
if err != nil {
    return nil, fmt.Errorf("read points: %w", err)
}

// Correlate shapes to point data via UUID.
for _, s := range pg.Shapes {
    if len(s.Points) == 0 {
        if pts, ok := pointMap[s.UniqueID]; ok {
            s.Points = pts
        }
    }
}
```

Where `readPagePoints` collects all point files for the page:

```go
func readPagePoints(entries map[string]*zip.File, noteID, pageID string) (map[string][]TinyPoint, error) {
    result := make(map[string][]TinyPoint)
    prefix := noteID + "/point/" + pageID + "/"
    
    for name, f := range entries {
        if !strings.HasPrefix(name, prefix) {
            continue
        }
        rc, err := f.Open()
        if err != nil {
            return nil, fmt.Errorf("open point file %s: %w", name, err)
        }
        data, err := io.ReadAll(rc)
        rc.Close()
        if err != nil {
            return nil, fmt.Errorf("read point file %s: %w", name, err)
        }
        
        points, err := parsePointFile(data)
        if err != nil {
            return nil, fmt.Errorf("parse point file %s: %w", name, err)
        }
        for id, pts := range points {
            result[id] = pts
        }
    }
    return result, nil
}
```

**Verification:**

Run:
```bash
go -C /home/jtd/ultrabridge build ./internal/booxnote/
```

Expected: Builds without errors.

**Commit:** `feat(booxnote): wire shape-to-point UUID correlation in page parser`
<!-- END_TASK_6 -->
<!-- END_SUBCOMPONENT_B -->

<!-- START_SUBCOMPONENT_C (tasks 7-8) -->
<!-- START_TASK_7 -->
### Task 7: Create test helpers for synthetic .note ZIP construction

**Files:**
- Create: `/home/jtd/ultrabridge/internal/booxnote/testhelper_test.go`

**Implementation:**

No real Boox .note export files are available in the repo. Tests construct synthetic ZIP archives in memory using `archive/zip`, `proto.Marshal()`, and manual binary encoding for point files. This gives deterministic, self-contained tests.

Create test helpers:

1. `buildTestNote(t *testing.T, opts noteOpts) *bytes.Reader` — builds a complete .note ZIP in memory with configurable:
   - `noteID`, `title` — note metadata
   - `pages []testPage` — each with pageID, width, height, shapes, points

2. `testPage` struct with `pageID string`, `width/height float64`, `shapes []*pb.ShapeInfoProto`, `pointData map[string][]TinyPoint`

3. `buildPointFile(entries map[string][]TinyPoint) []byte` — builds a V1 point file with header, point data blocks, xref table, and xref offset

4. `buildShapeZIP(shapes []*pb.ShapeInfoProto) []byte` — builds the nested shape ZIP containing serialized ShapeInfoProtoList

The test helper must produce ZIP archives matching the exact directory structure described in the design plan:
```
{noteId}/note/pb/note_info
{noteId}/virtual/page/pb/{pageId}
{noteId}/shape/{pageId}#shapes.zip
{noteId}/point/{pageId}/{shapeId}#points
```

**Verification:**

Run:
```bash
go -C /home/jtd/ultrabridge test ./internal/booxnote/ -run TestNothing -v
```

Expected: Compiles (even if no test functions run yet).

**Commit:** `test(booxnote): add synthetic ZIP test helpers for .note format`
<!-- END_TASK_7 -->

<!-- START_TASK_8 -->
### Task 8: Tests for parser — all AC1 criteria

**Verifies:** boox-notes-pipeline.AC1.1, boox-notes-pipeline.AC1.2, boox-notes-pipeline.AC1.3, boox-notes-pipeline.AC1.4, boox-notes-pipeline.AC1.5, boox-notes-pipeline.AC1.6, boox-notes-pipeline.AC1.7, boox-notes-pipeline.AC1.8, boox-notes-pipeline.AC1.9

**Files:**
- Create: `/home/jtd/ultrabridge/internal/booxnote/note_test.go`
- Create: `/home/jtd/ultrabridge/internal/booxnote/point_test.go`

**Testing:**

Tests use the synthetic ZIP helpers from Task 7. Follow project patterns: standard `testing` package, manual assertions (`if got != want { t.Errorf(...) }`), no testify.

`note_test.go` tests:

- **boox-notes-pipeline.AC1.1:** `TestOpen_ExtractsTitle` — build note with title "My Test Note", verify `note.Title == "My Test Note"`
- **boox-notes-pipeline.AC1.2:** `TestOpen_ExtractsPageDimensions` — build note with page 1860×2480, verify `page.Width == 1860` and `page.Height == 2480`
- **boox-notes-pipeline.AC1.3:** `TestOpen_DeserializesShapes` — build note with shapes of known type/color/thickness/boundingRect/matrixValues, verify each field on parsed `Shape` structs
- **boox-notes-pipeline.AC1.5:** `TestOpen_CorrelatesShapesToPoints` — build note with shape UUID "abc123" and point file with matching UUID, verify `shape.Points` is populated with correct coordinates
- **boox-notes-pipeline.AC1.6:** `TestOpen_CorruptZIP` — pass truncated/garbage bytes to `Open()`, verify returns non-nil error with descriptive message
- **boox-notes-pipeline.AC1.8:** `TestOpen_BlankPage` — build note with a page that has no shape ZIP entry, verify `page.Shapes` is nil/empty (no error)
- **boox-notes-pipeline.AC1.9:** `TestOpen_MultiplePages` — build note with 3 pages, verify `len(note.Pages) == 3` and each has correct dimensions

`point_test.go` tests:

- **boox-notes-pipeline.AC1.4:** `TestParsePointFile_V1` — manually build a V1 point file with known header (76 bytes), known point data (3 points with specific x/y/pressure/size/time), xref table, and xref offset. Parse and verify each TinyPoint field matches expected values. Verify big-endian byte order is correct.
- **boox-notes-pipeline.AC1.7:** `TestParsePointFile_UnsupportedVersion` — build a point file where xref data length is not a multiple of 44 bytes (simulating a different format version), verify error message contains "unsupported point file format version"
- **TestDecodeTinyPoints_EmptyBlock** — pass block shorter than 4 bytes, verify returns nil (not panic)

**Verification:**

Run:
```bash
go -C /home/jtd/ultrabridge test ./internal/booxnote/ -v
```

Expected: All tests pass.

**Commit:** `test(booxnote): add comprehensive parser tests covering AC1.1-AC1.9`
<!-- END_TASK_8 -->
<!-- END_SUBCOMPONENT_C -->
