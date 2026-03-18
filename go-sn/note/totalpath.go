package note

import (
	"encoding/binary"
	"fmt"
)

// Stroke is a single pen-down → pen-up sequence of points.
type Stroke struct {
	Points    []Point  // portrait pixel coordinates
	Pressures []uint16 // raw pressure values (0–4096 scale), same length as Points
}

// Point is a position in portrait pixel space.
type Point struct {
	X, Y float64
}

// isStrokeObject returns true if the 216-byte object header beginning at off
// looks like a pen stroke (has "others\x00\x00" at off+48).
func isStrokeObject(data []byte, off int) bool {
	if off+56 > len(data) {
		return false
	}
	return string(data[off+48:off+56]) == "others\x00\x00"
}

// DecodeTotalPath parses the TOTALPATH block returned by Note.TotalPathData.
// pageW and pageH are the device pixel dimensions (e.g. 1404×1872 for N6).
// Returns all pen strokes found in the block.
func DecodeTotalPath(tp []byte, pageW, pageH int) ([]Stroke, error) {
	if len(tp) < 8 {
		return nil, fmt.Errorf("TOTALPATH block too short (%d bytes)", len(tp))
	}

	// Outer header: [outer_count uint32][first_obj_size uint32]
	// outerCount := binary.LittleEndian.Uint32(tp[0:4])  // total object count
	firstObjSize := int(binary.LittleEndian.Uint32(tp[4:8]))

	var strokes []Stroke

	// Walk objects: first has no size prefix (size is in [4:8]),
	// all subsequent objects are preceded by a 4-byte LE size field.
	objOff := 8 // data start of first object
	objSize := firstObjSize
	first := true

	for objOff < len(tp) {
		if !first {
			// Read this object's size prefix
			if objOff+4 > len(tp) {
				break
			}
			objSize = int(binary.LittleEndian.Uint32(tp[objOff:]))
			objOff += 4
		}
		first = false

		dataStart := objOff
		if dataStart+objSize > len(tp) {
			// Truncated; parse what we can
			objSize = len(tp) - dataStart
		}
		if objSize < 216 {
			objOff = dataStart + objSize
			continue
		}

		if isStrokeObject(tp, dataStart) {
			s, err := decodeStroke(tp, dataStart, objSize, pageW, pageH)
			if err == nil {
				strokes = append(strokes, s)
			}
		}

		objOff = dataStart + objSize
	}

	return strokes, nil
}

// decodeStroke parses one pen-stroke object starting at dataStart.
//
// Object layout (relative to dataStart):
//
//	[0:212]         fixed header
//	[212:216]       point_count (uint32 LE)
//	[216:216+N*8]   N coordinate pairs: (rawX, rawY) uint32 LE each
//	[216+N*8]       pressure_count (uint32 LE, must equal N)
//	[220+N*8:...]   N pressure values (uint16 LE each)
//	...             additional arrays (timing etc.) — not parsed
//
// Coordinate transform (portrait pixel space):
//
//	pixel_Y = rawX * pageH / tpPageH
//	pixel_X = (tpPageW - rawY) * pageW / tpPageW
//
// where tpPageH and tpPageW are read from the header at +128 and +132.
func decodeStroke(tp []byte, dataStart, objSize, pageW, pageH int) (Stroke, error) {
	if dataStart+216 > len(tp) {
		return Stroke{}, fmt.Errorf("object too short for header")
	}

	tpPageH := int(binary.LittleEndian.Uint32(tp[dataStart+128:]))
	tpPageW := int(binary.LittleEndian.Uint32(tp[dataStart+132:]))
	if tpPageH <= 0 || tpPageW <= 0 {
		return Stroke{}, fmt.Errorf("invalid page dimensions in header: %d×%d", tpPageW, tpPageH)
	}

	n := int(binary.LittleEndian.Uint32(tp[dataStart+212:]))
	if n <= 0 || n > 100_000 {
		return Stroke{}, fmt.Errorf("implausible point count: %d", n)
	}

	coordEnd := dataStart + 216 + n*8
	if coordEnd > len(tp) {
		return Stroke{}, fmt.Errorf("coordinate data exceeds buffer: need %d, have %d", coordEnd, len(tp))
	}

	pts := make([]Point, n)
	for i := 0; i < n; i++ {
		base := dataStart + 216 + i*8
		rawX := binary.LittleEndian.Uint32(tp[base:])
		rawY := binary.LittleEndian.Uint32(tp[base+4:])

		pts[i] = Point{
			Y: float64(rawX) * float64(pageH) / float64(tpPageH),
			X: (float64(tpPageW) - float64(rawY)) * float64(pageW) / float64(tpPageW),
		}
	}

	// Pressure data (optional — may be absent in future format versions)
	var pressures []uint16
	if coordEnd+4 <= len(tp) {
		pcount := int(binary.LittleEndian.Uint32(tp[coordEnd:]))
		if pcount == n {
			pressureEnd := coordEnd + 4 + n*2
			if pressureEnd <= len(tp) {
				pressures = make([]uint16, n)
				for i := 0; i < n; i++ {
					pressures[i] = binary.LittleEndian.Uint16(tp[coordEnd+4+i*2:])
				}
			}
		}
	}

	return Stroke{Points: pts, Pressures: pressures}, nil
}
