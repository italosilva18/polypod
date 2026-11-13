package localcontrol

import (
	"fmt"
	"strings"
)

// qrVersionSpec holds ISO/IEC 18004 parameters for Byte Mode at ECC Level L.
type qrVersionSpec struct {
	version       int
	size          int
	totalCodeword int
	dataCodeword  int
	ecPerBlock    int
	blocks        int
	alignCenters  []int
}

var qrSpecsLevelL = []qrVersionSpec{
	{version: 1, size: 21, totalCodeword: 26, dataCodeword: 19, ecPerBlock: 7, blocks: 1},
	{version: 2, size: 25, totalCodeword: 44, dataCodeword: 34, ecPerBlock: 10, blocks: 1, alignCenters: []int{6, 18}},
	{version: 3, size: 29, totalCodeword: 70, dataCodeword: 55, ecPerBlock: 15, blocks: 1, alignCenters: []int{6, 22}},
	{version: 4, size: 33, totalCodeword: 100, dataCodeword: 80, ecPerBlock: 20, blocks: 1, alignCenters: []int{6, 26}},
	{version: 5, size: 37, totalCodeword: 134, dataCodeword: 108, ecPerBlock: 26, blocks: 1, alignCenters: []int{6, 30}},
	{version: 6, size: 41, totalCodeword: 172, dataCodeword: 136, ecPerBlock: 18, blocks: 2, alignCenters: []int{6, 34}},
}

// EncodeQRMatrix encodes text (up to 134 UTF-8 bytes) into a standard ISO/IEC
// 18004 QR Code boolean matrix (true = dark module, false = light module).
func EncodeQRMatrix(text string) ([][]bool, error) {
	payload := []byte(text)
	var spec *qrVersionSpec
	for i := range qrSpecsLevelL {
		// Byte mode overhead: 4 bits mode (0100) + 8 bits char count = 12 bits -> max dataCodeword - 2 bytes.
		if len(payload)+2 <= qrSpecsLevelL[i].dataCodeword {
			spec = &qrSpecsLevelL[i]
			break
		}
	}
	if spec == nil {
		return nil, fmt.Errorf("URL excede capacidade máxima do QR Code local (%d bytes > 134 bytes)", len(payload))
	}

	dataWords := encodeByteModeCodewords(payload, spec.dataCodeword)
	fullStream := buildErrorCorrectedStream(dataWords, spec)

	n := spec.size
	grid := make([][]bool, n)
	reserved := make([][]bool, n)
	for r := 0; r < n; r++ {
		grid[r] = make([]bool, n)
		reserved[r] = make([]bool, n)
	}

	placeFinder(grid, reserved, 0, 0)
	placeFinder(grid, reserved, 0, n-7)
	placeFinder(grid, reserved, n-7, 0)

	if len(spec.alignCenters) > 0 {
		for _, ar := range spec.alignCenters {
			for _, ac := range spec.alignCenters {
				// Skip corners occupied by finder patterns.
				if (ar == 6 && ac == 6) || (ar == 6 && ac == n-7) || (ar == n-7 && ac == 6) {
					continue
				}
				placeAlignment(grid, reserved, ar, ac)
			}
		}
	}

	// Timing patterns
	for i := 8; i < n-8; i++ {
		val := i%2 == 0
		if !reserved[6][i] {
			grid[6][i] = val
			reserved[6][i] = true
		}
		if !reserved[i][6] {
			grid[i][6] = val
			reserved[i][6] = true
		}
	}

	// Dark module + reserve format info strips
	grid[4*spec.version+9][8] = true
	reserved[4*spec.version+9][8] = true
	reserveFormatStrips(reserved, n)

	// Place data + EC bits in upward/downward 2-column zig-zag
	placeDataBits(grid, reserved, fullStream)

	// Apply mask 0: (row + col) % 2 == 0 on non-reserved modules
	const maskID = 0
	for r := 0; r < n; r++ {
		for c := 0; c < n; c++ {
			if !reserved[r][c] && (r+c)%2 == 0 {
				grid[r][c] = !grid[r][c]
			}
		}
	}

	// Write BCH(15,5) format bits for ECC Level L (0b01) + mask 0 (0b000)
	writeFormatBits(grid, computeFormatBits(0b01, maskID))
	return grid, nil
}

// RenderQRANSI renders text as a compact, scannable UTF-8 half-block QR Code
// with a 2-module quiet zone for terminal display.
func RenderQRANSI(text string) (string, error) {
	mat, err := EncodeQRMatrix(text)
	if err != nil {
		return "", err
	}
	const quiet = 2
	n := len(mat)
	total := n + 2*quiet

	isDark := func(r, c int) bool {
		mr, mc := r-quiet, c-quiet
		if mr < 0 || mr >= n || mc < 0 || mc >= n {
			return false // Quiet zone is light (white)
		}
		return mat[mr][mc]
	}

	// Render using high-contrast inverted palette (white quiet zone, dark modules)
	// via Unicode upper/lower half blocks so every 2 vertical modules occupy 1 row.
	var sb strings.Builder
	for r := 0; r < total; r += 2 {
		sb.WriteString("  ")
		for c := 0; c < total; c++ {
			topDark := isDark(r, c)
			botDark := false
			if r+1 < total {
				botDark = isDark(r+1, c)
			}
			// Using ANSI black-on-bright-white ensures universal phone camera readability
			// regardless of whether the user's terminal theme is dark or light.
			switch {
			case topDark && botDark:
				sb.WriteString("\x1b[30;107m█\x1b[0m")
			case topDark && !botDark:
				sb.WriteString("\x1b[30;107m▀\x1b[0m")
			case !topDark && botDark:
				sb.WriteString("\x1b[30;107m▄\x1b[0m")
			default:
				sb.WriteString("\x1b[30;107m \x1b[0m")
			}
		}
		sb.WriteByte('\n')
	}
	return sb.String(), nil
}

func encodeByteModeCodewords(payload []byte, capacity int) []byte {
	bits := make([]uint8, 0, capacity*8)
	appendBits := func(val int, count int) {
		for i := count - 1; i >= 0; i-- {
			bits = append(bits, uint8((val>>i)&1))
		}
	}
	appendBits(0b0100, 4) // Byte mode indicator
	appendBits(len(payload), 8)
	for _, b := range payload {
		appendBits(int(b), 8)
	}
	maxBits := capacity * 8
	for i := 0; i < 4 && len(bits) < maxBits; i++ {
		bits = append(bits, 0)
	}
	for len(bits)%8 != 0 {
		bits = append(bits, 0)
	}
	out := make([]byte, 0, capacity)
	for i := 0; i < len(bits); i += 8 {
		var w byte
		for j := 0; j < 8; j++ {
			w = (w << 1) | bits[i+j]
		}
		out = append(out, w)
	}
	padBytes := []byte{0xEC, 0x11}
	for p := 0; len(out) < capacity; p++ {
		out = append(out, padBytes[p%2])
	}
	return out
}

func buildErrorCorrectedStream(data []byte, spec *qrVersionSpec) []byte {
	if spec.blocks == 1 {
		ec := reedSolomonEncode(data, spec.ecPerBlock)
		out := make([]byte, 0, len(data)+len(ec))
		out = append(out, data...)
		out = append(out, ec...)
		return out
	}
	// Equal-sized block interleaving (used by Version 6 ECC-L: 2 blocks of 68 data + 18 EC)
	perBlock := spec.dataCodeword / spec.blocks
	dataBlocks := make([][]byte, spec.blocks)
	ecBlocks := make([][]byte, spec.blocks)
	for b := 0; b < spec.blocks; b++ {
		slice := data[b*perBlock : (b+1)*perBlock]
		dataBlocks[b] = slice
		ecBlocks[b] = reedSolomonEncode(slice, spec.ecPerBlock)
	}
	out := make([]byte, 0, spec.totalCodeword)
	for i := 0; i < perBlock; i++ {
		for b := 0; b < spec.blocks; b++ {
			out = append(out, dataBlocks[b][i])
		}
	}
	for i := 0; i < spec.ecPerBlock; i++ {
		for b := 0; b < spec.blocks; b++ {
			out = append(out, ecBlocks[b][i])
		}
	}
	return out
}

func gfMul(a, b byte) byte {
	var p byte
	for i := 0; i < 8; i++ {
		if (b & 1) != 0 {
			p ^= a
		}
		hi := (a & 0x80) != 0
		a <<= 1
		if hi {
			a ^= 0x1D // x^8 + x^4 + x^3 + x^2 + 1
		}
		b >>= 1
	}
	return p
}

func reedSolomonGenerator(degree int) []byte {
	gen := []byte{1}
	root := byte(1)
	for i := 0; i < degree; i++ {
		next := make([]byte, len(gen)+1)
		for j, coeff := range gen {
			next[j] ^= coeff
			next[j+1] ^= gfMul(coeff, root)
		}
		gen = next
		root = gfMul(root, 0x02)
	}
	return gen[1:] // omit leading 1
}

func reedSolomonEncode(data []byte, ecCount int) []byte {
	gen := reedSolomonGenerator(ecCount)
	rem := make([]byte, ecCount)
	for _, b := range data {
		factor := b ^ rem[0]
		copy(rem, rem[1:])
		rem[ecCount-1] = 0
		for j := 0; j < ecCount; j++ {
			rem[j] ^= gfMul(gen[j], factor)
		}
	}
	return rem
}

func placeFinder(grid, reserved [][]bool, top, left int) {
	n := len(grid)
	for dr := -1; dr <= 7; dr++ {
		for dc := -1; dc <= 7; dc++ {
			r, c := top+dr, left+dc
			if r < 0 || r >= n || c < 0 || c >= n {
				continue
			}
			inOuter := dr >= 0 && dr <= 6 && dc >= 0 && dc <= 6
			isBorder := dr == 0 || dr == 6 || dc == 0 || dc == 6
			isCenter := dr >= 2 && dr <= 4 && dc >= 2 && dc <= 4
			grid[r][c] = inOuter && (isBorder || isCenter)
			reserved[r][c] = true
		}
	}
}

func placeAlignment(grid, reserved [][]bool, centerR, centerC int) {
	for dr := -2; dr <= 2; dr++ {
		for dc := -2; dc <= 2; dc++ {
			r, c := centerR+dr, centerC+dc
			dark := dr == -2 || dr == 2 || dc == -2 || dc == 2 || (dr == 0 && dc == 0)
			grid[r][c] = dark
			reserved[r][c] = true
		}
	}
}

func reserveFormatStrips(reserved [][]bool, n int) {
	for i := 0; i <= 8; i++ {
		if i < n {
			reserved[8][i] = true
			reserved[i][8] = true
		}
	}
	for i := 0; i < 8; i++ {
		reserved[8][n-1-i] = true
		reserved[n-1-i][8] = true
	}
}

func placeDataBits(grid, reserved [][]bool, codewords []byte) {
	n := len(grid)
	bitIdx := 0
	totalBits := len(codewords) * 8
	upward := true

	for rightCol := n - 1; rightCol >= 1; rightCol -= 2 {
		if rightCol == 6 {
			rightCol = 5 // skip vertical timing column
		}
		for step := 0; step < n; step++ {
			r := step
			if upward {
				r = n - 1 - step
			}
			for dc := 0; dc < 2; dc++ {
				c := rightCol - dc
				if reserved[r][c] {
					continue
				}
				dark := false
				if bitIdx < totalBits {
					dark = ((codewords[bitIdx/8] >> (7 - (bitIdx % 8))) & 1) == 1
					bitIdx++
				}
				grid[r][c] = dark
			}
		}
		upward = !upward
	}
}

func computeFormatBits(eccLevel, mask int) uint16 {
	data := ((eccLevel & 0x3) << 3) | (mask & 0x7)
	rem := data << 10
	for i := 14; i >= 10; i-- {
		if (rem & (1 << i)) != 0 {
			rem ^= 0x537 << (i - 10)
		}
	}
	return uint16(((data << 10) | rem) ^ 0x5412)
}

func writeFormatBits(grid [][]bool, bits uint16) {
	n := len(grid)
	bit := func(i int) bool { return ((bits >> i) & 1) == 1 }

	// Around top-left finder
	tlRows := []int{8, 8, 8, 8, 8, 8, 8, 8, 7, 5, 4, 3, 2, 1, 0}
	tlCols := []int{0, 1, 2, 3, 4, 5, 7, 8, 8, 8, 8, 8, 8, 8, 8}
	for i := 0; i < 15; i++ {
		grid[tlRows[i]][tlCols[i]] = bit(14 - i)
	}

	// Split across bottom-left and top-right
	for i := 0; i < 7; i++ {
		grid[n-1-i][8] = bit(14 - i)
	}
	for i := 7; i < 15; i++ {
		grid[8][n-15+i] = bit(14 - i)
	}
}
