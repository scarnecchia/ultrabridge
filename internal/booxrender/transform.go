package booxrender

import "github.com/fogleman/gg"

// applyTransform applies a Boox matrixValues affine transform to the gg context.
// matrixValues is a 9-element array representing a 3x3 matrix:
// [scaleX, skewX, transX, skewY, scaleY, transY, persp0, persp1, persp2]
//
// The affine transformation matrix is:
// | scaleX skewX  transX |
// | skewY  scaleY transY |
// | 0      0      1      |
//
// To avoid incorrect decomposition, we apply transformations in the order that
// preserves mathematical equivalence: Translate, then Shear, then Scale.
// This is the reverse order in which they appear in the matrix, which correctly
// applies the transformation when points are drawn after pushing these operations.
func applyTransform(dc *gg.Context, mv []float64) {
	if len(mv) < 6 {
		return // no transform or incomplete
	}
	// Check if it's identity (optimization for the common case).
	if mv[0] == 1 && mv[1] == 0 && mv[2] == 0 &&
		mv[3] == 0 && mv[4] == 1 && mv[5] == 0 {
		return
	}

	// Apply transformations in reverse order: Translate, then Shear, then Scale.
	// This ensures that when points are subsequently drawn, they are transformed
	// by the combined matrix in the mathematically correct way.
	// Finally translate
	if mv[2] != 0 || mv[5] != 0 {
		dc.Translate(mv[2], mv[5])
	}
	// Then shear
	if mv[1] != 0 || mv[3] != 0 {
		dc.Shear(mv[1], mv[3])
	}
	// Scale first
	if mv[0] != 1 || mv[4] != 1 {
		dc.Scale(mv[0], mv[4])
	}
}
