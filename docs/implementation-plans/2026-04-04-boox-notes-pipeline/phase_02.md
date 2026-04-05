# Boox Notes Pipeline — Phase 2: Stroke Renderer

**Goal:** Render parsed Boox pages to visually faithful JPEG images using `fogleman/gg` for 2D rendering.

**Architecture:** New package `internal/booxrender/` with a single public function `RenderPage(*booxnote.Page) (image.Image, error)` that creates a canvas at page native resolution, iterates shapes in z-order, renders strokes with pressure-driven width variation, and returns an `image.Image`. Follows the same output pattern as Supernote rendering (`gosnote.RenderObjects` → `image.Image` → `jpeg.Encode` at quality 90).

**Tech Stack:** `github.com/fogleman/gg` (2D rendering), `image/jpeg` (encoding), `math` (affine transforms, float operations).

**Scope:** 7 phases from original design (phase 2 of 7)

**Codebase verified:** 2026-04-04

**Reference documentation:**
- Shape types: `/home/jtd/booxreverse/BOOX_STROKE_FORMAT.md` (Shape Types section)
- Supernote render pattern: `/home/jtd/ultrabridge/internal/processor/worker.go:149-154`
- Web handler render pattern: `/home/jtd/ultrabridge/internal/web/handler.go:561-619`

---

## Acceptance Criteria Coverage

This phase implements and tests:

### boox-notes-pipeline.AC2: Renderer produces visually faithful page images
- **boox-notes-pipeline.AC2.1 Success:** Renderer produces JPEG at page native resolution (e.g., 1860×2480)
- **boox-notes-pipeline.AC2.2 Success:** Stroke width varies with pressure values from point data
- **boox-notes-pipeline.AC2.3 Success:** Different pen types (pencil, fountain, brush, marker, calligraphy) produce visually distinct rendering
- **boox-notes-pipeline.AC2.4 Success:** Colors render correctly from ARGB packed int
- **boox-notes-pipeline.AC2.5 Success:** Affine transforms from matrixValues are applied to strokes
- **boox-notes-pipeline.AC2.6 Success:** Geometric shapes (circle, rectangle, line) render from bounding rect/vertices
- **boox-notes-pipeline.AC2.7 Edge:** Renderer handles shapes with empty point data gracefully (skip, no crash)
- **boox-notes-pipeline.AC2.8 Edge:** Renderer handles pages with >500 shapes without excessive memory or time

---

<!-- START_SUBCOMPONENT_A (tasks 1-1) -->
<!-- START_TASK_1 -->
### Task 1: Add fogleman/gg dependency

**Files:**
- Modify: `/home/jtd/ultrabridge/go.mod`

**Step 1: Add dependency**

Run:
```bash
go -C /home/jtd/ultrabridge get github.com/fogleman/gg
```

Expected: Dependency added to go.mod and go.sum without errors.

**Step 2: Verify**

Run:
```bash
go -C /home/jtd/ultrabridge build ./...
```

Expected: Builds without errors.

**Commit:** `chore(booxrender): add fogleman/gg dependency for 2D rendering`
<!-- END_TASK_1 -->
<!-- END_SUBCOMPONENT_A -->

<!-- START_SUBCOMPONENT_B (tasks 2-4) -->
<!-- START_TASK_2 -->
### Task 2: Core page renderer with pressure-sensitive strokes

**Verifies:** boox-notes-pipeline.AC2.1, boox-notes-pipeline.AC2.2, boox-notes-pipeline.AC2.7

**Files:**
- Create: `/home/jtd/ultrabridge/internal/booxrender/render.go`

**Implementation:**

Create the main renderer. Key design decisions:
- Canvas created at exact page dimensions (Width×Height from `booxnote.Page`)
- White background (no template rendering in initial implementation, per design plan)
- Shapes iterated in z-order (already sorted by parser)
- `gg` does NOT support variable-width strokes natively — implement by drawing individual line segments between consecutive points, adjusting `SetLineWidth()` per segment based on interpolated pressure
- Use `gg.LineCapRound` and `gg.LineJoinRound` for natural stroke appearance

```go
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
```

The `pressureToWidth` function maps pressure (0-4095 typical EMR range) to pixel width, modulated by pen type:

```go
func pressureToWidth(pressure, thickness float64, ps penStyle) float64 {
    // Normalize pressure to 0-1 range.
    normalized := math.Min(pressure/4095.0, 1.0)
    // Apply pen-type curve: different pens respond differently to pressure.
    curved := math.Pow(normalized, ps.PressureExponent)
    // Scale by base thickness and pen width range.
    width := ps.MinWidthFactor*thickness + curved*(ps.MaxWidthFactor-ps.MinWidthFactor)*thickness
    return math.Max(width, 0.5) // minimum visible width
}
```

**Verification:**

Run:
```bash
go -C /home/jtd/ultrabridge build ./internal/booxrender/
```

Expected: Builds without errors.

**Commit:** `feat(booxrender): add core page renderer with pressure-sensitive strokes`
<!-- END_TASK_2 -->

<!-- START_TASK_3 -->
### Task 3: Pen type visual treatments

**Verifies:** boox-notes-pipeline.AC2.3

**Files:**
- Create: `/home/jtd/ultrabridge/internal/booxrender/penstyle.go`

**Implementation:**

Each pen type gets a `penStyle` struct controlling its visual characteristics. From `/home/jtd/booxreverse/BOOX_STROKE_FORMAT.md` shape types:

```go
package booxrender

// penStyle controls per-pen-type visual treatment.
type penStyle struct {
    PressureExponent float64 // Power curve for pressure response
    MinWidthFactor   float64 // Minimum width as fraction of thickness
    MaxWidthFactor   float64 // Maximum width as fraction of thickness
    AlphaMultiplier  float64 // Opacity multiplier (1.0 = full, <1.0 = translucent)
}

var penStyles = map[int32]penStyle{
    // Pencil (type 2): thin, uniform, low pressure response
    2: {PressureExponent: 0.8, MinWidthFactor: 0.3, MaxWidthFactor: 0.7, AlphaMultiplier: 1.0},
    // Oily/gel pen (type 3): moderate pressure response
    3: {PressureExponent: 0.6, MinWidthFactor: 0.4, MaxWidthFactor: 1.0, AlphaMultiplier: 1.0},
    // Fountain pen (type 4): strong pressure response, wide range
    4: {PressureExponent: 0.4, MinWidthFactor: 0.2, MaxWidthFactor: 1.2, AlphaMultiplier: 1.0},
    // Brush (type 5): very wide range, strong response
    5: {PressureExponent: 0.3, MinWidthFactor: 0.1, MaxWidthFactor: 1.5, AlphaMultiplier: 1.0},
    // Marker (type 15): wide, semi-transparent
    15: {PressureExponent: 0.9, MinWidthFactor: 0.8, MaxWidthFactor: 1.2, AlphaMultiplier: 0.4},
    // Neo brush (type 21): wide brush
    21: {PressureExponent: 0.3, MinWidthFactor: 0.15, MaxWidthFactor: 1.6, AlphaMultiplier: 1.0},
    // Charcoal (type 22): textured, moderate
    22: {PressureExponent: 0.5, MinWidthFactor: 0.3, MaxWidthFactor: 1.0, AlphaMultiplier: 0.85},
    // Square pen (type 47): uniform width
    47: {PressureExponent: 1.0, MinWidthFactor: 0.9, MaxWidthFactor: 1.1, AlphaMultiplier: 1.0},
    // Latin calligraphy (type 60): angle-sensitive approximated as strong pressure
    60: {PressureExponent: 0.35, MinWidthFactor: 0.15, MaxWidthFactor: 1.4, AlphaMultiplier: 1.0},
    // Asian calligraphy (type 61): similar to Latin
    61: {PressureExponent: 0.35, MinWidthFactor: 0.15, MaxWidthFactor: 1.4, AlphaMultiplier: 1.0},
}

// Default pen style for unknown shape types.
var defaultPenStyle = penStyle{
    PressureExponent: 0.5,
    MinWidthFactor:   0.3,
    MaxWidthFactor:   1.0,
    AlphaMultiplier:  1.0,
}

func getPenStyle(shapeType int32) penStyle {
    if ps, ok := penStyles[shapeType]; ok {
        return ps
    }
    return defaultPenStyle
}
```

**Verification:**

Run:
```bash
go -C /home/jtd/ultrabridge build ./internal/booxrender/
```

Expected: Builds without errors.

**Commit:** `feat(booxrender): add pen type visual treatments for all scribble types`
<!-- END_TASK_3 -->

<!-- START_TASK_4 -->
### Task 4: Color decoding, affine transforms, and geometric shapes

**Verifies:** boox-notes-pipeline.AC2.4, boox-notes-pipeline.AC2.5, boox-notes-pipeline.AC2.6

**Files:**
- Create: `/home/jtd/ultrabridge/internal/booxrender/color.go`
- Create: `/home/jtd/ultrabridge/internal/booxrender/transform.go`
- Create: `/home/jtd/ultrabridge/internal/booxrender/geometry.go`

**Implementation:**

`color.go` — ARGB packed int to RGBA float64:

```go
package booxrender

// decodeARGB extracts RGBA components from a packed ARGB int32.
// Boox stores color as 0xAARRGGBB.
func decodeARGB(argb int32) (r, g, b, a float64) {
    a = float64((argb>>24)&0xFF) / 255.0
    r = float64((argb>>16)&0xFF) / 255.0
    g = float64((argb>>8)&0xFF) / 255.0
    b = float64(argb&0xFF) / 255.0
    return
}
```

`transform.go` — Apply affine transform from matrixValues JSON array. Boox uses Android's standard 3×3 matrix stored as `[scaleX, skewX, transX, skewY, scaleY, transY, persp0, persp1, persp2]`:

```go
package booxrender

import "github.com/fogleman/gg"

// applyTransform applies a Boox matrixValues affine transform to the gg context.
// matrixValues is a 9-element array representing a 3x3 matrix:
// [scaleX, skewX, transX, skewY, scaleY, transY, persp0, persp1, persp2]
//
// gg uses a 3x2 affine matrix via Multiply:
// | a b |     | scaleX skewX  |
// | c d | =>  | skewY  scaleY  |
// | e f |     | transX transY  |
func applyTransform(dc *gg.Context, mv []float64) {
    if len(mv) < 6 {
        return // no transform or incomplete
    }
    // Check if it's identity (optimization for the common case).
    if mv[0] == 1 && mv[1] == 0 && mv[2] == 0 &&
        mv[3] == 0 && mv[4] == 1 && mv[5] == 0 {
        return
    }
    // gg.Multiply takes: a, b, c, d, e, f
    // Mapping: a=scaleX, b=skewY, c=skewX, d=scaleY, e=transX, f=transY
    dc.Multiply(gg.Matrix{
        mv[0], mv[3], // a=scaleX, b=skewY
        mv[1], mv[4], // c=skewX,  d=scaleY
        mv[2], mv[5], // e=transX, f=transY
    })
}
```

Note: Verify the `gg.Matrix` field order against the `gg` library documentation at implementation time. The `gg` context uses `Multiply(m Matrix)` where `Matrix` has fields XX, YX, XY, YY, X0, Y0. Adjust field mapping if needed.

`geometry.go` — Render geometric shapes from bounding rect:

```go
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
```

**Verification:**

Run:
```bash
go -C /home/jtd/ultrabridge build ./internal/booxrender/
```

Expected: Builds without errors.

**Commit:** `feat(booxrender): add ARGB color decoding, affine transforms, and geometric shape rendering`
<!-- END_TASK_4 -->
<!-- END_SUBCOMPONENT_B -->

<!-- START_SUBCOMPONENT_C (tasks 5-5) -->
<!-- START_TASK_5 -->
### Task 5: Tests for renderer — all AC2 criteria

**Verifies:** boox-notes-pipeline.AC2.1, boox-notes-pipeline.AC2.2, boox-notes-pipeline.AC2.3, boox-notes-pipeline.AC2.4, boox-notes-pipeline.AC2.5, boox-notes-pipeline.AC2.6, boox-notes-pipeline.AC2.7, boox-notes-pipeline.AC2.8

**Files:**
- Create: `/home/jtd/ultrabridge/internal/booxrender/render_test.go`
- Create: `/home/jtd/ultrabridge/internal/booxrender/color_test.go`

**Testing:**

Follow project testing patterns: standard `testing` package, manual assertions. Tests construct `booxnote.Page` structs directly (no ZIP needed — renderer takes parsed data).

`render_test.go` tests:

- **boox-notes-pipeline.AC2.1:** `TestRenderPage_Resolution` — create page with Width=1860, Height=2480, one simple stroke. Call `RenderPage`, verify returned `image.Image` has `Bounds().Dx() == 1860` and `Bounds().Dy() == 2480`.

- **boox-notes-pipeline.AC2.2:** `TestRenderPage_PressureVariation` — create page with one scribble shape (type 2, pencil) containing 3 points: low pressure (500), high pressure (3500), low pressure (500). Render and verify the image is not blank (non-white pixels exist). This verifies strokes are drawn; visual pressure variation is confirmed by inspection.

- **boox-notes-pipeline.AC2.3:** `TestRenderPage_PenTypes` — for each pen type (2, 4, 5, 15, 60), create a page with identical stroke data but different shapeType. Render each and verify all produce non-blank images. Verify marker (type 15) produces an image with semi-transparent pixels (alpha < 255 in the rendered region) — check a pixel in the stroke area.

- **boox-notes-pipeline.AC2.4:** `TestDecodeARGB` — table-driven test for `decodeARGB()`:
  - Input `0xFF000000` (opaque black) → r=0, g=0, b=0, a=1.0
  - Input `0xFFFF0000` (opaque red) → r=1.0, g=0, b=0, a=1.0
  - Input `0x80008000` (50% transparent green) → r=0, g≈0.5, b=0, a≈0.5
  - Input `0x00000000` (fully transparent) → a=0

- **boox-notes-pipeline.AC2.5:** `TestRenderPage_AffineTransform` — create page with one shape and matrixValues representing a translation (e.g., [1,0,100,0,1,200,0,0,1] = translate by 100,200). Render and verify non-white pixels appear in the translated region (not at origin).

- **boox-notes-pipeline.AC2.6:** `TestRenderPage_GeometricShapes` — create page with circle (type 0), rectangle (type 1), and line (type 7) shapes with known bounding rects. Render and verify non-blank image.

- **boox-notes-pipeline.AC2.7:** `TestRenderPage_EmptyPoints` — create page with shape that has ShapeType=2 (scribble) but empty Points slice. Verify `RenderPage` returns without error and image is blank (all white).

- **boox-notes-pipeline.AC2.8:** `TestRenderPage_ManyShapes` — create page with 600 simple shapes (each with 10 points). Call `RenderPage` and verify it completes without error. Use `testing.T` timeout or measure duration to ensure it completes in reasonable time (< 10 seconds).

`color_test.go` tests:

- `TestDecodeARGB_EdgeCases` — test 0x00FFFFFF (transparent white), 0xFFFFFFFF (opaque white), negative int32 values (high bit set).

**Verification:**

Run:
```bash
go -C /home/jtd/ultrabridge test ./internal/booxrender/ -v
```

Expected: All tests pass.

**Commit:** `test(booxrender): add renderer tests covering AC2.1-AC2.8`
<!-- END_TASK_5 -->
<!-- END_SUBCOMPONENT_C -->
