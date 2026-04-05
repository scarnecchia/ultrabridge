package booxrender

import "github.com/fogleman/gg"

// applyTransform applies a Boox matrixValues affine transform to the gg context.
// matrixValues is a 9-element array representing a 3x3 matrix:
// [scaleX, skewX, transX, skewY, scaleY, transY, persp0, persp1, persp2]
//
// We decompose it into scale, shear, and translate operations in the correct order.
// The Boox matrix format is: scaleX, skewX, transX, skewY, scaleY, transY, ...
// We apply transformations in reverse order: scale, shear, then translate.
func applyTransform(dc *gg.Context, mv []float64) {
	if len(mv) < 6 {
		return // no transform or incomplete
	}
	// Check if it's identity (optimization for the common case).
	if mv[0] == 1 && mv[1] == 0 && mv[2] == 0 &&
		mv[3] == 0 && mv[4] == 1 && mv[5] == 0 {
		return
	}

	// Apply transformations in composition order
	// Scale first
	if mv[0] != 1 || mv[4] != 1 {
		dc.Scale(mv[0], mv[4])
	}
	// Then shear
	if mv[1] != 0 || mv[3] != 0 {
		dc.Shear(mv[1], mv[3])
	}
	// Finally translate
	if mv[2] != 0 || mv[5] != 0 {
		dc.Translate(mv[2], mv[5])
	}
}
