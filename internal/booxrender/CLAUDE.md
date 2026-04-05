# Boox Stroke Renderer

Last verified: 2026-04-05

## Purpose
Renders parsed Boox note pages to images. Draws pressure-sensitive strokes (scribbles) and geometric shapes using the fogleman/gg 2D graphics library.

## Contracts
- **Exposes**: `RenderPage(page *booxnote.Page) (image.Image, error)`
- **Guarantees**: Produces native-resolution image (page Width x Height, fallback 1860x2480). White background. Shapes rendered in ZOrder. Unknown shape types silently skipped. Single-point strokes and shapes without point data gracefully handled (skipped, not errored).
- **Expects**: Parsed `*booxnote.Page` with Shapes and Points already correlated (output of `booxnote.Open`).

## Dependencies
- **Uses**: `github.com/fogleman/gg` (2D drawing context), `internal/booxnote` (Page, Shape, TinyPoint types)
- **Used by**: `booxpipeline` (processor renders pages to JPEG cache)
- **Boundary**: Pure rendering -- no filesystem I/O (caller encodes and saves the image). No database access.

## Shape Type Classification
- **Scribble types** (stroke data from Points): 2 (pencil), 3 (oily/gel), 4 (fountain), 5 (brush), 15 (marker), 21 (neo brush), 22 (charcoal), 47 (square pen), 60 (Latin calligraphy), 61 (Asian calligraphy)
- **Geometric types** (from BoundingRect): 0 (circle), 1 (rectangle), 7 (line), 8 (triangle), 17, 28 (arrow), 31, 39
- **Other types** (text, image, audio): silently skipped

## Pen Style System
Each scribble type has a `penStyle` controlling:
- `PressureExponent` -- power curve for pressure response (lower = more sensitive)
- `MinWidthFactor` / `MaxWidthFactor` -- width range as fraction of shape thickness
- `AlphaMultiplier` -- opacity (1.0 = opaque, <1.0 = translucent, e.g. marker at 0.4)

Pressure mapped from EMR range (0-4095) to width via: `minWidth + pow(pressure/4095, exponent) * (maxWidth - minWidth) * thickness`

## Color Encoding
Boox stores colors as packed ARGB int32 (`0xAARRGGBB`). `decodeARGB` extracts normalized float64 RGBA components.

## Affine Transforms
`matrixValues` is a 9-element JSON array from the protobuf. First 6 elements form a 2D affine matrix (scaleX, skewX, transX, skewY, scaleY, transY). Identity matrices (no transform) return nil to skip multiplication.

## Gotchas
- Segment-by-segment rendering: each pair of consecutive points drawn as a separate line segment with averaged pressure width (not a single polyline) -- required for pressure-varying stroke width
- Minimum visible width clamped to 0.5px to prevent invisible strokes
- Fill behavior: geometric shapes with non-zero fillColor get filled then stroked; zero fillColor means stroke-only
