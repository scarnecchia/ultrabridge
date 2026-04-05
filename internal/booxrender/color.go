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
