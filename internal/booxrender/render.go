package booxrender

import (
	"image"
	"image/color"
	"math"

	"github.com/fogleman/gg"

	"github.com/sysop/ultrabridge/internal/booxnote"
)

// Scribble shape types that contain stroke data (from BOOX_STROKE_FORMAT.md).
var scribbleTypes = map[int32]bool{
	2: true, 3: true, 4: true, 5: true, 15: true,
	21: true, 22: true, 47: true, 60: true, 61: true,
}

// Geometric shape types rendered from bounding rect.
var geometricTypes = map[int32]bool{
	0: true, 1: true, 7: true, 8: true, 17: true,
	28: true, 31: true, 39: true,
}

// RenderPage renders a parsed Boox page to an image at native resolution.
func RenderPage(page *booxnote.Page) (image.Image, error) {
	w := int(page.Width)
	h := int(page.Height)
	if w <= 0 || h <= 0 {
		w, h = 1860, 2480 // fallback default
	}

	dc := gg.NewContext(w, h)
	dc.SetColor(color.White)
	dc.Clear()

	for _, shape := range page.Shapes {
		renderShape(dc, shape)
	}

	return dc.Image(), nil
}

func renderShape(dc *gg.Context, s *booxnote.Shape) {
	if scribbleTypes[s.ShapeType] {
		renderScribble(dc, s)
	} else if geometricTypes[s.ShapeType] {
		renderGeometric(dc, s)
	}
	// Other types (text, image, audio, etc.) skipped silently — AC2.7.
}

// renderScribble draws a pressure-sensitive stroke from point data.
func renderScribble(dc *gg.Context, s *booxnote.Shape) {
	if len(s.Points) < 2 {
		return // AC2.7: skip shapes with empty/insufficient point data
	}

	dc.Push()
	applyTransform(dc, s.MatrixValues)

	r, g, b, a := decodeARGB(s.Color)
	penStyle := getPenStyle(s.ShapeType)

	dc.SetRGBA(r, g, b, a*penStyle.AlphaMultiplier)
	dc.SetLineCap(gg.LineCapRound)
	dc.SetLineJoin(gg.LineJoinRound)

	// Draw segment-by-segment with per-segment width from pressure.
	for i := 0; i < len(s.Points)-1; i++ {
		p0 := s.Points[i]
		p1 := s.Points[i+1]

		// Interpolate pressure between points for smooth width transition.
		pressure := (float64(p0.Pressure) + float64(p1.Pressure)) / 2.0
		width := pressureToWidth(pressure, float64(s.Thickness), penStyle)

		dc.SetLineWidth(width)
		dc.MoveTo(float64(p0.X), float64(p0.Y))
		dc.LineTo(float64(p1.X), float64(p1.Y))
		dc.Stroke()
	}

	dc.Pop()
}

// pressureToWidth maps pressure (0-4095 typical EMR range) to pixel width, modulated by pen type.
func pressureToWidth(pressure, thickness float64, ps penStyle) float64 {
	// Normalize pressure to 0-1 range.
	normalized := math.Min(pressure/4095.0, 1.0)
	// Apply pen-type curve: different pens respond differently to pressure.
	curved := math.Pow(normalized, ps.PressureExponent)
	// Scale by base thickness and pen width range.
	width := ps.MinWidthFactor*thickness + curved*(ps.MaxWidthFactor-ps.MinWidthFactor)*thickness
	return math.Max(width, 0.5) // minimum visible width
}
