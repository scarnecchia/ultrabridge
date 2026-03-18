// sndump dumps TOTALPATH non-stroke objects for analysis.
package main

import (
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"log"
	"os"

	"github.com/sysop/go-sn/note"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: sndump file.note")
		os.Exit(1)
	}

	f, err := os.Open(os.Args[1])
	if err != nil {
		log.Fatal(err)
	}
	defer f.Close()

	n, err := note.Load(f)
	if err != nil {
		log.Fatalf("parse: %v", err)
	}

	pageW := n.PageWidth()
	pageH := n.PageHeight()
	fmt.Printf("device: %dx%d\n", pageW, pageH)

	for _, p := range n.Pages {
		tp, err := n.TotalPathData(p)
		if err != nil {
			log.Printf("page %d TotalPathData: %v", p.Index+1, err)
			continue
		}
		if tp == nil {
			fmt.Printf("page %d: no TOTALPATH\n", p.Index+1)
			continue
		}

		fmt.Printf("\n=== page %d ===\n", p.Index+1)
		fmt.Printf("TOTALPATH length: %d bytes\n", len(tp))
		fmt.Printf("outer_count: %d\n", binary.LittleEndian.Uint32(tp[0:4]))
		fmt.Printf("first_obj_size: %d\n", binary.LittleEndian.Uint32(tp[4:8]))

		dumpObjects(tp, pageW, pageH)
	}
}

func dumpObjects(tp []byte, pageW, pageH int) {
	firstObjSize := int(binary.LittleEndian.Uint32(tp[4:8]))
	objOff := 8
	objSize := firstObjSize
	first := true
	objIdx := 0

	for objOff < len(tp) {
		if !first {
			if objOff+4 > len(tp) {
				break
			}
			objSize = int(binary.LittleEndian.Uint32(tp[objOff:]))
			objOff += 4
		}
		first = false

		dataStart := objOff
		if dataStart+objSize > len(tp) {
			objSize = len(tp) - dataStart
		}

		isStroke := objSize >= 56 && string(tp[dataStart+48:dataStart+56]) == "others\x00\x00"

		if !isStroke && objSize >= 216 {
			fmt.Printf("\n--- obj %d (NON-STROKE) ds=%d size=%d ---\n", objIdx, dataStart, objSize)
			dumpNonStroke(tp, dataStart, objSize, pageW, pageH)
		} else {
			fmt.Printf("obj %d: stroke ds=%d size=%d\n", objIdx, dataStart, objSize)
		}

		objOff = dataStart + objSize
		objIdx++
	}
}

func dumpNonStroke(tp []byte, ds, size, pageW, pageH int) {
	if ds+size > len(tp) {
		return
	}
	obj := tp[ds : ds+size]

	// Print header bytes
	fmt.Printf("  bytes[0:16]:  %s\n", hex.EncodeToString(obj[0:16]))
	fmt.Printf("  bytes[16:32]: %s\n", hex.EncodeToString(obj[16:32]))
	fmt.Printf("  bytes[32:48]: %s\n", hex.EncodeToString(obj[32:48]))
	fmt.Printf("  bytes[48:64]: %s\n", hex.EncodeToString(obj[48:64]))
	fmt.Printf("  bytes[64:80]: %s\n", hex.EncodeToString(obj[64:80]))
	fmt.Printf("  bytes[80:96]: %s\n", hex.EncodeToString(obj[80:96]))
	fmt.Printf("  bytes[96:128]:\n")
	for off := 96; off < 128 && off+4 <= size; off += 4 {
		v := binary.LittleEndian.Uint32(obj[off:])
		fmt.Printf("    [%d]=%d (0x%08X)\n", off, v, v)
	}
	fmt.Printf("  bytes[128:144]:\n")
	for off := 128; off < 144 && off+4 <= size; off += 4 {
		v := binary.LittleEndian.Uint32(obj[off:])
		fmt.Printf("    [%d]=%d (0x%08X)\n", off, v, v)
	}

	// Byte-8 discriminator
	b8 := binary.LittleEndian.Uint32(obj[8:])
	fmt.Printf("  byte8_u32=%d\n", b8)

	// tpPageH / tpPageW
	if size >= 136 {
		tpH := int(binary.LittleEndian.Uint32(obj[128:]))
		tpW := int(binary.LittleEndian.Uint32(obj[132:]))
		fmt.Printf("  tpPageH=%d tpPageW=%d\n", tpH, tpW)
	}

	// point_count at +212
	if size >= 216 {
		n := int(binary.LittleEndian.Uint32(obj[212:]))
		fmt.Printf("  point_count=%d\n", n)

		if n > 0 && n <= 1000 && size >= 216+n*8 {
			tpH := float64(binary.LittleEndian.Uint32(obj[128:]))
			tpW := float64(binary.LittleEndian.Uint32(obj[132:]))
			fmt.Printf("  points (TOTALPATH → pixel):\n")
			for i := 0; i < n; i++ {
				base := 216 + i*8
				rawX := float64(binary.LittleEndian.Uint32(obj[base:]))
				rawY := float64(binary.LittleEndian.Uint32(obj[base+4:]))
				pxY := rawX * float64(pageH) / tpH
				pxX := (tpW - rawY) * float64(pageW) / tpW
				fmt.Printf("    [%d] rawX=%6.0f rawY=%6.0f → pixel X=%.1f Y=%.1f\n", i, rawX, rawY, pxX, pxY)
			}
		}
	}
}
