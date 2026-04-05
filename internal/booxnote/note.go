package booxnote

import (
	"archive/zip"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
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

// parseNoteInfo reads and unmarshals the note_info protobuf, extracts the title,
// and returns the page name list (JSON array of page ID strings).
func parseNoteInfo(entries map[string]*zip.File, path string, note *Note) ([]string, error) {
	data, err := readEntry(entries, path)
	if err != nil {
		return nil, fmt.Errorf("booxnote: read note_info: %w", err)
	}

	var noteInfo pb.NoteInfo
	if err := proto.Unmarshal(data, &noteInfo); err != nil {
		return nil, fmt.Errorf("booxnote: unmarshal note_info: %w", err)
	}

	note.Title = noteInfo.GetTitle()

	// Parse pageNameList JSON to extract page IDs.
	pageListJSON := noteInfo.GetPageNameList()
	var pageNames []string
	if pageListJSON != "" {
		if err := json.Unmarshal([]byte(pageListJSON), &pageNames); err != nil {
			return nil, fmt.Errorf("booxnote: parse pageNameList: %w", err)
		}
	}

	return pageNames, nil
}

// parsePage reads the VirtualPage protobuf for dimensions, parses shapes and points.
func parsePage(entries map[string]*zip.File, noteID, pageID string) (*Page, error) {
	// Read VirtualPage protobuf for dimensions.
	vpPath := noteID + "/virtual/page/pb/" + pageID
	vpData, err := readEntry(entries, vpPath)
	if err != nil {
		return nil, fmt.Errorf("read virtual page: %w", err)
	}

	var vp pb.VirtualPage
	if err := proto.Unmarshal(vpData, &vp); err != nil {
		return nil, fmt.Errorf("unmarshal virtual page: %w", err)
	}

	width, height, err := parsePageSize(vp.GetPageSize())
	if err != nil {
		return nil, err
	}

	pg := &Page{
		PageID: vp.GetPageId(),
		Width:  width,
		Height: height,
	}

	// Parse shapes from nested shape ZIP.
	shapes, err := parseShapes(entries, noteID, pageID)
	if err != nil {
		return nil, fmt.Errorf("parse shapes: %w", err)
	}
	pg.Shapes = shapes

	// Read point files and correlate to shapes.
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

// readPagePoints collects all point files for a page and returns a map of shapeID → []TinyPoint.
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
