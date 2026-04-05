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
