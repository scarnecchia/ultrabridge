package booxrender

import (
	"math"

	"github.com/fogleman/gg"

	"github.com/sysop/ultrabridge/internal/booxnote"
)

func renderGeometric(dc *gg.Context, s *booxnote.Shape) {
	if s.BoundingRect == nil {
		return // no geometry data
	}

	dc.Push()
	applyTransform(dc, s.MatrixValues)

	r, g, b, a := decodeARGB(s.Color)
	dc.SetRGBA(r, g, b, a)
	dc.SetLineWidth(float64(s.Thickness))
	dc.SetLineCap(gg.LineCapRound)

	br := s.BoundingRect
	x := br.Left
	y := br.Top
	w := br.Right - br.Left
	h := br.Bottom - br.Top

	switch s.ShapeType {
	case 0: // Circle
		cx := x + w/2
		cy := y + h/2
		rx := w / 2
		dc.DrawEllipse(cx, cy, rx, h/2)
	case 1: // Rectangle
		dc.DrawRectangle(x, y, w, h)
	case 7: // Line
		dc.DrawLine(br.Left, br.Top, br.Right, br.Bottom)
	case 8: // Triangle
		dc.MoveTo(x+w/2, y)
		dc.LineTo(x, y+h)
		dc.LineTo(x+w, y+h)
		dc.ClosePath()
	case 28: // Arrow line
		dc.DrawLine(br.Left, br.Top, br.Right, br.Bottom)
		// Arrow head: simple V at endpoint
		angle := math.Atan2(br.Bottom-br.Top, br.Right-br.Left)
		headLen := math.Min(15, w/4)
		dc.MoveTo(br.Right, br.Bottom)
		dc.LineTo(
			br.Right-headLen*math.Cos(angle-math.Pi/6),
			br.Bottom-headLen*math.Sin(angle-math.Pi/6),
		)
		dc.MoveTo(br.Right, br.Bottom)
		dc.LineTo(
			br.Right-headLen*math.Cos(angle+math.Pi/6),
			br.Bottom-headLen*math.Sin(angle+math.Pi/6),
		)
	default:
		// Polyline, polygon, curve — fallback to bounding rect outline
		dc.DrawRectangle(x, y, w, h)
	}

	// Fill if fillColor is non-zero, otherwise stroke.
	if s.FillColor != 0 {
		fr, fg, fb, fa := decodeARGB(s.FillColor)
		dc.SetRGBA(fr, fg, fb, fa)
		dc.FillPreserve()
		dc.SetRGBA(r, g, b, a)
	}
	dc.Stroke()

	dc.Pop()
}
