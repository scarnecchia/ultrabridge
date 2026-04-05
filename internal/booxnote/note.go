package booxnote

import (
	"archive/zip"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"sort"
	"strconv"
	"strings"

	"google.golang.org/protobuf/encoding/protowire"
)

// Note represents a fully parsed Boox .note file.
type Note struct {
	NoteID string   // top-level directory name in the ZIP
	Title  string   // from note_info protobuf
	Pages  []*Page  // ordered by orderIndex
}

// Page represents a single page within a note.
type Page struct {
	PageID     string
	Width      float64
	Height     float64
	OrderIndex float32 // from VirtualPage protobuf; used for sorting
	Shapes     []*Shape // ordered by zorder
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

	// Parse each page. First try using pageNames as VirtualPage file IDs.
	// If that fails, scan virtual/page/pb/ directory for all page files,
	// since some firmware uses different IDs for VirtualPage files vs pageNameList.
	vpPrefix := noteID + "/virtual/page/pb/"
	vpFiles := findEntries(entries, vpPrefix)

	if len(vpFiles) > 0 {
		// Scan all VirtualPage files — the pageId field inside maps to shape/point dirs.
		for _, vpPath := range vpFiles {
			pg, err := parsePage(entries, noteID, vpPath)
			if err != nil {
				return nil, fmt.Errorf("booxnote: page %s: %w", vpPath, err)
			}
			note.Pages = append(note.Pages, pg)
		}
	} else {
		// Fallback: try pageNames directly (older firmware).
		for _, pageName := range pageNames {
			pg, err := parsePage(entries, noteID, pageName)
			if err != nil {
				return nil, fmt.Errorf("booxnote: page %s: %w", pageName, err)
			}
			note.Pages = append(note.Pages, pg)
		}
	}
	_ = pageNames // used in fallback path

	// Sort pages by orderIndex for consistent ordering.
	sort.Slice(note.Pages, func(i, j int) bool {
		return note.Pages[i].OrderIndex < note.Pages[j].OrderIndex
	})

	return note, nil
}

// parseNoteInfo reads the note_info protobuf using low-level wire parsing.
// We avoid proto.Unmarshal because real Boox devices produce string fields
// with non-UTF-8 bytes, which Go's proto3 unmarshaler rejects.
func parseNoteInfo(entries map[string]*zip.File, path string, note *Note) ([]string, error) {
	data, err := readEntry(entries, path)
	if err != nil {
		return nil, fmt.Errorf("booxnote: read note_info: %w", err)
	}

	// The note_info file may contain a wrapper message with the actual NoteInfo
	// nested in field 1. Unwrap if the top-level only contains field 1 bytes.
	inner := unwrapField1(data)

	// NoteInfo field numbers: title=6, pageNameList=20
	var title, pageListJSON string
	for len(inner) > 0 {
		num, typ, n := protowire.ConsumeTag(inner)
		if n < 0 {
			break
		}
		inner = inner[n:]

		switch typ {
		case protowire.VarintType:
			_, n = protowire.ConsumeVarint(inner)
		case protowire.Fixed32Type:
			_, n = protowire.ConsumeFixed32(inner)
		case protowire.Fixed64Type:
			_, n = protowire.ConsumeFixed64(inner)
		case protowire.BytesType:
			var v []byte
			v, n = protowire.ConsumeBytes(inner)
			if n >= 0 {
				switch num {
				case 6: // title
					title = string(v)
				case 20: // pageNameList
					pageListJSON = string(v)
				}
			}
		default:
			n = 0
		}
		if n < 0 {
			break
		}
		inner = inner[n:]
	}

	note.Title = title

	// pageNameList may be a bare JSON array ["id1","id2"] or a JSON object
	// {"pageNameList":["id1","id2"]} depending on device firmware.
	var pageNames []string
	if pageListJSON != "" {
		if err := json.Unmarshal([]byte(pageListJSON), &pageNames); err != nil {
			// Try wrapped format: {"pageNameList": [...]}
			var wrapped struct {
				PageNameList []string `json:"pageNameList"`
			}
			if err2 := json.Unmarshal([]byte(pageListJSON), &wrapped); err2 != nil {
				return nil, fmt.Errorf("booxnote: parse pageNameList: %w", err)
			}
			pageNames = wrapped.PageNameList
		}
	}

	return pageNames, nil
}

// unwrapField1 checks if the data is a wrapper message containing a single field 1
// (length-delimited). If so, returns the inner bytes. Otherwise returns data unchanged.
// Some Boox firmware wraps NoteInfo in an outer message.
func unwrapField1(data []byte) []byte {
	if len(data) == 0 {
		return data
	}
	num, typ, n := protowire.ConsumeTag(data)
	if n < 0 || num != 1 || typ != protowire.BytesType {
		return data // not a wrapper
	}
	inner, n2 := protowire.ConsumeBytes(data[n:])
	if n2 < 0 {
		return data
	}
	// Check if the wrapper consumed all or most of the data
	// (there may be a second field 1 for a second NoteInfo entry)
	if n+n2 <= len(data) {
		return inner
	}
	return data
}

// parseVirtualPageFields extracts pageId (field 1), pageSize (field 6), and orderIndex (field 4) from raw protobuf wire data.
func parseVirtualPageFields(data []byte) (pageID, pageSize string, orderIndex float32) {
	for len(data) > 0 {
		num, typ, n := protowire.ConsumeTag(data)
		if n < 0 {
			break
		}
		data = data[n:]
		switch typ {
		case protowire.VarintType:
			_, n = protowire.ConsumeVarint(data)
		case protowire.Fixed32Type:
			var v uint32
			v, n = protowire.ConsumeFixed32(data)
			if n >= 0 && num == 4 { // orderIndex
				orderIndex = math.Float32frombits(v)
			}
		case protowire.Fixed64Type:
			_, n = protowire.ConsumeFixed64(data)
		case protowire.BytesType:
			var v []byte
			v, n = protowire.ConsumeBytes(data)
			if n >= 0 {
				switch num {
				case 1:
					pageID = string(v)
				case 6:
					pageSize = string(v)
				}
			}
		default:
			n = 0
		}
		if n < 0 {
			break
		}
		data = data[n:]
	}
	return
}

// findEntries returns all non-directory ZIP entry names matching a prefix.
func findEntries(entries map[string]*zip.File, prefix string) []string {
	var result []string
	for name, f := range entries {
		if strings.HasPrefix(name, prefix) && !f.FileInfo().IsDir() {
			result = append(result, name)
		}
	}
	return result
}

// parsePage reads the VirtualPage protobuf for dimensions, parses shapes and points.
// vpPath is the full ZIP path to the VirtualPage protobuf file.
func parsePage(entries map[string]*zip.File, noteID, vpPath string) (*Page, error) {
	// Read VirtualPage protobuf for dimensions.
	vpData, err := readEntry(entries, vpPath)
	if err != nil {
		return nil, fmt.Errorf("read virtual page: %w", err)
	}

	// VirtualPage field numbers: pageId=1, pageSize=6
	// Some firmware wraps VirtualPage in a container message. Try direct first,
	// then unwrapped if pageSize is empty.
	vpPageID, vpPageSize, vpOrderIndex := parseVirtualPageFields(vpData)
	if vpPageSize == "" {
		vpPageID, vpPageSize, vpOrderIndex = parseVirtualPageFields(unwrapField1(vpData))
	}

	width, height, err := parsePageSize(vpPageSize)
	if err != nil {
		return nil, err
	}

	pg := &Page{
		PageID:     vpPageID,
		Width:      width,
		Height:     height,
		OrderIndex: vpOrderIndex,
	}

	// Parse shapes from nested shape ZIP. Use vpPageID (from protobuf) as the
	// shape/point directory key.
	shapes, err := parseShapes(entries, noteID, vpPageID)
	if err != nil {
		return nil, fmt.Errorf("parse shapes: %w", err)
	}
	pg.Shapes = shapes

	// Read point files and correlate to shapes.
	pointMap, err := readPagePoints(entries, noteID, vpPageID)
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

	return pg, nil
}

// readEntry reads a single ZIP entry into bytes.
func readEntry(entries map[string]*zip.File, name string) ([]byte, error) {
	f, ok := entries[name]
	if !ok {
		return nil, fmt.Errorf("entry not found: %s", name)
	}

	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()

	data, err := io.ReadAll(rc)
	if err != nil {
		return nil, err
	}

	return data, nil
}

// parsePageSize parses the pageSize field which may be in "WxH" or JSON format.
func parsePageSize(s string) (width, height float64, err error) {
	// Try "WxH" format first.
	if parts := strings.SplitN(s, "x", 2); len(parts) == 2 {
		w, err1 := strconv.ParseFloat(parts[0], 64)
		h, err2 := strconv.ParseFloat(parts[1], 64)
		if err1 == nil && err2 == nil {
			return w, h, nil
		}
	}
	// Try JSON {"width":N,"height":N} format.
	var dim struct {
		Width  float64 `json:"width"`
		Height float64 `json:"height"`
	}
	if err := json.Unmarshal([]byte(s), &dim); err == nil && dim.Width > 0 && dim.Height > 0 {
		return dim.Width, dim.Height, nil
	}
	// Try JSON rect {"left":N,"top":N,"right":N,"bottom":N} format.
	var rect struct {
		Left   float64 `json:"left"`
		Top    float64 `json:"top"`
		Right  float64 `json:"right"`
		Bottom float64 `json:"bottom"`
	}
	if err := json.Unmarshal([]byte(s), &rect); err == nil && (rect.Right-rect.Left) > 0 && (rect.Bottom-rect.Top) > 0 {
		return rect.Right - rect.Left, rect.Bottom - rect.Top, nil
	}
	return 0, 0, fmt.Errorf("booxnote: cannot parse page size: %q", s)
}

// readPagePoints collects all point files for a page and returns a map of shapeID → []TinyPoint.
func readPagePoints(entries map[string]*zip.File, noteID, pageID string) (map[string][]TinyPoint, error) {
	result := make(map[string][]TinyPoint)
	prefix := noteID + "/point/" + pageID + "/"

	for name, f := range entries {
		if !strings.HasPrefix(name, prefix) || f.FileInfo().IsDir() {
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
