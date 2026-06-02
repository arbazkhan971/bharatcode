package tui

type rect struct {
	X int
	Y int
	W int
	H int
}

type layout struct {
	header rect
	chat   rect
	input  rect
	status rect
	footer rect
}

func computeLayout(width int, height int) layout {
	if width < 0 {
		width = 0
	}
	if height < 0 {
		height = 0
	}
	headerH := 1
	inputH := 3
	statusH := 1
	footerH := 1
	chatH := height - headerH - inputH - statusH - footerH
	if chatH < 0 {
		chatH = 0
	}
	y := 0
	l := layout{
		header: rect{X: 0, Y: y, W: width, H: headerH},
	}
	y += headerH
	l.chat = rect{X: 0, Y: y, W: width, H: chatH}
	y += chatH
	l.input = rect{X: 0, Y: y, W: width, H: inputH}
	y += inputH
	l.status = rect{X: 0, Y: y, W: width, H: statusH}
	y += statusH
	l.footer = rect{X: 0, Y: y, W: width, H: footerH}
	return l
}

func (l layout) validFor(width int, height int) bool {
	rects := []rect{l.header, l.chat, l.input, l.status, l.footer}
	y := 0
	for _, r := range rects {
		if r.X != 0 || r.Y != y || r.W != width || r.H < 0 {
			return false
		}
		y += r.H
	}
	return y == height
}
