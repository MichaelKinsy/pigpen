package ts

// Length is a position or extent (length.h): bytes are UTF-16LE bytes, rows count '\n', columns
// count bytes since the last '\n'.
type Length struct {
	bytes uint32
	row   uint32
	col   uint32
}

var lengthUndefined = Length{0, 0, 1}

func (l Length) isUndefined() bool { return l.bytes == 0 && l.col != 0 }

func pointAdd(a, b Length) (row, col uint32) {
	if b.row > 0 {
		return a.row + b.row, b.col
	}
	return a.row, a.col + b.col
}

func lengthAdd(a, b Length) Length {
	r, c := pointAdd(a, b)
	return Length{a.bytes + b.bytes, r, c}
}

func lengthSub(a, b Length) Length {
	bytes := uint32(0)
	if a.bytes >= b.bytes {
		bytes = a.bytes - b.bytes
	}
	var row, col uint32
	if a.row > b.row {
		row, col = a.row-b.row, a.col
	} else {
		row = 0
		if a.col >= b.col {
			col = a.col - b.col
		}
	}
	return Length{bytes, row, col}
}
